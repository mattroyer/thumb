package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	publicAPI    = envOr("THUMB_APPVIEW", "https://public.api.bsky.app")
	plcDirectory = envOr("THUMB_PLC", "https://plc.directory")
)

var handleRE = regexp.MustCompile(
	`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

type ident struct {
	DID      string
	PDS      string
	Handle   string
	Verified bool
}

func normalize(user string) string {
	user = strings.ToLower(strings.TrimLeft(strings.TrimSpace(user), "@"))
	if rest, ok := strings.CutPrefix(user, "at://"); ok {
		user, _, _ = strings.Cut(rest, "/")
	}
	// A pasted profile link: https://bsky.app/profile/alice.bsky.social
	for _, prefix := range []string{"https://", "http://", ""} {
		if rest, ok := strings.CutPrefix(user, prefix+"bsky.app/profile/"); ok {
			user, _, _ = strings.Cut(rest, "/")
			user = strings.TrimLeft(user, "@")
			break
		}
	}
	return user
}

// suggestHandle guesses what someone meant when a handle doesn't resolve,
// and returns the guess only if it actually resolves.
func suggestHandle(user string) (suggestion, hint string) {
	var candidate string
	switch {
	case strings.HasSuffix(user, ".bsky.app"):
		candidate = strings.TrimSuffix(user, ".bsky.app") + ".bsky.social"
		hint = "bsky.app is Bluesky's website; Bluesky handles end in .bsky.social"
	case !strings.Contains(user, ".") && !strings.HasPrefix(user, "did:") && user != "":
		candidate = user + ".bsky.social"
		hint = "handles include a domain"
	default:
		return "", ""
	}
	if _, err := resolveHandle(candidate); err != nil {
		return "", ""
	}
	return candidate, hint
}

// resolveHandle maps a handle to a DID, trying in order: the DNS TXT record
// _atproto.<handle>, https://<handle>/.well-known/atproto-did, and finally the
// public AppView. It is a variable so tests can replace it.
var resolveHandle = func(handle string) (string, error) {
	if strings.HasPrefix(handle, "did:") {
		return handle, nil
	}
	if !handleRE.MatchString(handle) {
		return "", fmt.Errorf("not a valid handle: %q", handle)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if txts, err := net.DefaultResolver.LookupTXT(ctx, "_atproto."+handle); err == nil {
		for _, t := range txts {
			if did, ok := strings.CutPrefix(strings.TrimSpace(t), "did="); ok && strings.HasPrefix(did, "did:") {
				return did, nil
			}
		}
	}

	if did, err := wellKnownDID(handle); err == nil {
		return did, nil
	}

	var r struct {
		DID string `json:"did"`
	}
	err := doJSON("GET", publicAPI+"/xrpc/com.atproto.identity.resolveHandle",
		url.Values{"handle": {handle}}, nil, "", &r)
	if err != nil {
		return "", fmt.Errorf("%s: no such user (%v)", handle, err)
	}
	return r.DID, nil
}

func wellKnownDID(handle string) (string, error) {
	c := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", "https://"+handle+"/.well-known/atproto-did", nil)
	req.Header.Set("User-Agent", "thumb/"+version)
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	did := strings.TrimSpace(string(b))
	if !strings.HasPrefix(did, "did:") {
		return "", fmt.Errorf("not a DID")
	}
	return did, nil
}

// resolveDID fetches a DID document and returns the PDS endpoint and the
// handle the document claims. A variable so tests can replace it.
var resolveDID = func(did string) (pds, handle string, err error) {
	var docURL string
	switch {
	case strings.HasPrefix(did, "did:plc:"):
		docURL = plcDirectory + "/" + did
	case strings.HasPrefix(did, "did:web:"):
		host, _ := url.PathUnescape(strings.TrimPrefix(did, "did:web:"))
		if strings.Contains(host, ":") && !strings.Contains(host, "%") && strings.Count(host, ":") > 1 {
			return "", "", fmt.Errorf("unsupported did:web with path: %s", did)
		}
		docURL = "https://" + host + "/.well-known/did.json"
	default:
		return "", "", fmt.Errorf("unsupported DID method: %s", did)
	}

	var doc struct {
		AlsoKnownAs []string `json:"alsoKnownAs"`
		Service     []struct {
			ID              string `json:"id"`
			ServiceEndpoint any    `json:"serviceEndpoint"`
		} `json:"service"`
	}
	if err := doJSON("GET", docURL, nil, nil, "", &doc); err != nil {
		return "", "", fmt.Errorf("resolving %s: %v", did, err)
	}
	for _, s := range doc.Service {
		if ep, ok := s.ServiceEndpoint.(string); ok && strings.HasSuffix(s.ID, "#atproto_pds") {
			pds = strings.TrimRight(ep, "/")
			break
		}
	}
	for _, aka := range doc.AlsoKnownAs {
		if h, ok := strings.CutPrefix(aka, "at://"); ok {
			handle = h
			break
		}
	}
	if pds == "" {
		return "", "", fmt.Errorf("%s has no PDS listed in its DID document", did)
	}
	return pds, handle, nil
}

func lookup(user string) (*ident, error) {
	user = normalize(user)
	did, err := resolveHandle(user)
	if err != nil {
		if s, hint := suggestHandle(user); s != "" {
			return nil, fmt.Errorf("%s isn't a handle. Did you mean %s? (%s)", user, s, hint)
		}
		return nil, err
	}
	pds, docHandle, err := resolveDID(did)
	if err != nil {
		return nil, err
	}
	// Bidirectional check: the DID document must claim the handle we started from.
	verified := strings.HasPrefix(user, "did:") || strings.EqualFold(docHandle, user)
	h := docHandle
	if h == "" {
		h = user
	}
	return &ident{DID: did, PDS: pds, Handle: h, Verified: verified}, nil
}
