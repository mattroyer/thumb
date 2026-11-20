package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The collection (NSID) holding plan records: the "plan" type under thumbplan.date.
// Every copy of thumb must use the same name to read each other's plans.
var collection = envOr("THUMB_COLLECTION", "date.thumbplan.plan")

// legacyCollection is the placeholder name used before thumbplan.date existed.
// `thumb migrate` moves entries out of it.
const legacyCollection = "dev.thumb.plan"

type planValue struct {
	Type      string `json:"$type"`
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type planRecord struct {
	URI   string    `json:"uri"`
	CID   string    `json:"cid"`
	Value planValue `json:"value"`
}

func (r planRecord) rkey() string {
	return r.URI[strings.LastIndex(r.URI, "/")+1:]
}

// listPlans returns up to limit plan records from a repo, newest first.
func listPlans(pds, did string, limit int) ([]planRecord, error) {
	var r struct {
		Records []planRecord `json:"records"`
	}
	err := doJSON("GET", pds+"/xrpc/com.atproto.repo.listRecords", url.Values{
		"repo":       {did},
		"collection": {collection},
		"limit":      {strconv.Itoa(limit)},
	}, nil, "", &r)
	return r.Records, err
}

func fetchDisplayName(id *ident) string {
	var r struct {
		Value struct {
			DisplayName string `json:"displayName"`
		} `json:"value"`
	}
	err := doJSON("GET", id.PDS+"/xrpc/com.atproto.repo.getRecord", url.Values{
		"repo": {id.DID}, "collection": {"app.bsky.actor.profile"}, "rkey": {"self"},
	}, nil, "", &r)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(r.Value.DisplayName)
}

// sanitize strips control characters (except newline and tab) so a plan can't
// inject terminal escape codes: the classic finger hole (RFC 1288 sec. 3.3).
// Everything that came from the network goes through this before printing,
// including error messages from remote servers.
func sanitize(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return -1
		}
		// Invisible text-direction controls can make a line display
		// differently from what it says ("Trojan Source" style tricks).
		if (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			return -1
		}
		return r
	}, s)
}

func fmtTime(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		if s == "" {
			return "unknown"
		}
		return sanitize(s)
	}
	return t.Local().Format("Mon Jan 02 15:04 2006 (MST)")
}

func render(user string, limit int, long bool) (string, error) {
	id, err := lookup(user)
	if err != nil {
		return "", err
	}
	plans, err := listPlans(id.PDS, id.DID, limit)
	if err != nil {
		return "", err
	}
	name := sanitize(fetchDisplayName(id))
	handle := sanitize(id.Handle)
	if !id.Verified {
		handle += " (handle unverified)"
	}

	var b strings.Builder
	b.WriteString(strings.TrimRight(fmt.Sprintf("Login: %-38sName: %s", handle, name), " ") + "\n")
	if long {
		fmt.Fprintf(&b, "DID:   %s\nPDS:   %s\n", sanitize(id.DID), sanitize(id.PDS))
	}
	if len(plans) == 0 {
		b.WriteString("No Plan.\n")
		return b.String(), nil
	}
	fmt.Fprintf(&b, "Last plan update: %s\nPlan:\n", fmtTime(plans[0].Value.CreatedAt))
	for i, p := range plans {
		if len(plans) > 1 {
			if i > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "--- %s  [%s] ---\n", fmtTime(p.Value.CreatedAt), sanitize(p.rkey()))
		}
		b.WriteString(strings.TrimRight(sanitize(p.Value.Text), " \t\n") + "\n")
	}
	return b.String(), nil
}
