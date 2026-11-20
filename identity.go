package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
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

// DIDs thumb accepts. did:plc IDs are 24 base32 characters. did:web is
// limited to a plain public hostname: no ports and no paths, as the AT
// Protocol requires. Anything else is refused before it can be turned into a
// URL, so a lookup can't be steered at "did:web:localhost%3A8080" and friends.
var (
	plcRE = regexp.MustCompile(`^did:plc:[a-z2-7]{24}$`)
	webRE = regexp.MustCompile(
		`^did:web:([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

func validDID(did string) bool { return plcRE.MatchString(did) || webRE.MatchString(did) }

// pickTXT returns the DID from _atproto TXT records. Exactly one did= record
// is required; several mean the handle is misconfigured (or tampered with).
func pickTXT(txts []string) (string, bool) {
	var found []string
	for _, t := range txts {
		if did, ok := strings.CutPrefix(strings.TrimSpace(t), "did="); ok {
			found = append(found, did)
		}
	}
	if len(found) != 1 || !validDID(found[0]) {
		return "", false
	}
	return found[0], true
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
		if did, ok := pickTXT(txts); ok {
			return did, nil
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
	// Same transport (and safety rules) as every other request, shorter timeout.
	c := *httpClient
	c.Timeout = 5 * time.Second
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
	if !validDID(did) {
		return "", fmt.Errorf("not a DID")
	}
	return did, nil
}

// checkPDS makes sure a PDS address from a DID document is a plain HTTPS
// origin. Your app password and login tokens are sent there, so plain HTTP
// is refused unless THUMB_ALLOW_HTTP_PDS=1 (for local test networks only).
func checkPDS(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		strings.Trim(u.Path, "/") != "" {
		return "", fmt.Errorf("DID document lists an invalid PDS address: %q", endpoint)
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && os.Getenv("THUMB_ALLOW_HTTP_PDS") == "1":
	default:
		return "", fmt.Errorf("refusing PDS %q: it must use https", endpoint)
	}
	return u.Scheme + "://" + u.Host, nil
}

// resolveDID fetches a DID document and returns the PDS endpoint and the
// handle the document claims. A variable so tests can replace it.
var resolveDID = func(did string) (pds, handle string, err error) {
	var docURL string
	switch {
	case plcRE.MatchString(did):
		docURL = plcDirectory + "/" + did
	case webRE.MatchString(did):
		docURL = "https://" + strings.TrimPrefix(did, "did:web:") + "/.well-known/did.json"
	default:
		return "", "", fmt.Errorf("unsupported or malformed DID: %q", did)
	}

	var doc struct {
		ID          string   `json:"id"`
		AlsoKnownAs []string `json:"alsoKnownAs"`
		Service     []struct {
			ID              string `json:"id"`
			ServiceEndpoint any    `json:"serviceEndpoint"`
		} `json:"service"`
	}
	if err := doJSON("GET", docURL, nil, nil, "", &doc); err != nil {
		return "", "", fmt.Errorf("resolving %s: %v", did, err)
	}
	if doc.ID != did {
		return "", "", fmt.Errorf("DID document for %s describes a different DID", did)
	}
	for _, s := range doc.Service {
		if ep, ok := s.ServiceEndpoint.(string); ok && strings.HasSuffix(s.ID, "#atproto_pds") {
			if pds, err = checkPDS(ep); err != nil {
				return "", "", err
			}
			break
		}
	}
	for _, aka := range doc.AlsoKnownAs {
		if h, ok := strings.CutPrefix(aka, "at://"); ok {
			handle = strings.ToLower(h)
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
	if !validDID(did) {
		return nil, fmt.Errorf("%q is not a valid DID", did)
	}
	pds, docHandle, err := resolveDID(did)
	if err != nil {
		return nil, err
	}

	// A handle is only trusted when both directions agree: the handle points
	// to the DID, and the DID's document claims the handle back. When you
	// look someone up by DID, the claimed handle gets checked the other way.
	verified := false
	if strings.HasPrefix(user, "did:") {
		if docHandle != "" && handleRE.MatchString(docHandle) {
			back, err := resolveHandle(docHandle)
			verified = err == nil && back == did
		}
	} else {
		verified = docHandle == user
	}
	// Show only what we can vouch for: the verified handle, or else exactly
	// what you typed (marked unverified by render).
	h := user
	if verified && docHandle != "" {
		h = docHandle
	}
	return &ident{DID: did, PDS: pds, Handle: h, Verified: verified}, nil
}
