package main

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

const profileCollection = "app.bsky.actor.profile"

// cmdName shows or sets the display name thumb prints next to "Name:". It
// lives in your account's standard profile record (the same one Bluesky
// uses), so thumb edits just that field and keeps everything else in it.
func (a *app) cmdName(args []string) error {
	fs := a.flags("name", `thumb name ["Your Name"]   (thumb name "" clears it)`)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	s, err := loadSession()
	if err != nil {
		return err
	}

	var cur struct {
		CID   string         `json:"cid"`
		Value map[string]any `json:"value"`
	}
	err = doJSON("GET", s.PDS+"/xrpc/com.atproto.repo.getRecord", url.Values{
		"repo": {s.DID}, "collection": {profileCollection}, "rkey": {"self"},
	}, nil, "", &cur)
	var xe *xrpcError
	exists := err == nil
	if err != nil && !(errors.As(err, &xe) && xe.Code == "RecordNotFound") {
		return err
	}

	if len(pos) == 0 {
		if name, _ := cur.Value["displayName"].(string); strings.TrimSpace(name) != "" {
			fmt.Fprintln(a.out, sanitize(name))
		} else {
			fmt.Fprintln(a.out, `No name set. Set one with: thumb name "Your Name"`)
		}
		return nil
	}

	name := strings.TrimSpace(strings.Join(pos, " "))
	if sanitize(name) != name || strings.Contains(name, "\n") {
		return errors.New("a name can't contain control characters or line breaks")
	}
	if n := utf8.RuneCountInString(name); n > 64 {
		return fmt.Errorf("that name is %d characters; the limit is 64", n)
	}

	value := cur.Value
	if value == nil {
		value = map[string]any{}
	}
	value["$type"] = profileCollection
	if name == "" {
		delete(value, "displayName")
	} else {
		value["displayName"] = name
	}
	body := map[string]any{
		"repo": s.DID, "collection": profileCollection, "rkey": "self", "record": value,
	}
	if exists {
		body["swapRecord"] = cur.CID // fail rather than overwrite a change made elsewhere
	}
	if err := xrpcAuthed(s, "POST", "com.atproto.repo.putRecord", nil, body, nil); err != nil {
		return err
	}
	if name == "" {
		fmt.Fprintln(a.out, "Name cleared.")
	} else {
		fmt.Fprintf(a.out, "Name set to %s.\n", name)
	}
	return nil
}
