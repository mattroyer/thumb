package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewSinceLastLook(t *testing.T) {
	setup(t)
	run(t, "login", "test.example")
	if _, out, _ := run(t, "new"); !strings.Contains(out, "list is empty") {
		t.Fatalf("empty list: %q", out)
	}

	run(t, "post", "-m", "first")
	run(t, "test.example") // read it: now seen
	if _, out, _ := run(t, "new"); !strings.Contains(out, "Nothing new") {
		t.Fatalf("right after reading: %q", out)
	}

	time.Sleep(5 * time.Millisecond)
	run(t, "post", "-m", "second")
	_, out, _ := run(t, "new")
	if !strings.Contains(out, "New since you last looked:") || !strings.Contains(out, "test.example") {
		t.Fatalf("after a new post: %q", out)
	}
	if _, out2, _ := run(t, "new"); out2 != out {
		t.Fatalf("`thumb new` must not mark anyone as seen: %q then %q", out, out2)
	}

	run(t, "test.example")
	if _, out, _ := run(t, "new"); !strings.Contains(out, "Nothing new") {
		t.Fatalf("after reading again: %q", out)
	}

	// Amending an entry counts as new too.
	time.Sleep(5 * time.Millisecond)
	run(t, "post", "--amend", "-m", "second, amended")
	if _, out, _ := run(t, "new"); !strings.Contains(out, "test.example") {
		t.Fatalf("after an amend: %q", out)
	}

	// Lines from older list files (no time) are skipped; lookups that fail
	// are counted, not fatal.
	run(t, "test.example")
	f, _ := os.OpenFile(thumbedPath(), os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("old.example\nnobody.example\t2026-01-01T00:00:00Z\n")
	f.Close()
	code, out, errOut := run(t, "new")
	if code != 0 || !strings.Contains(out, "Nothing new") || !strings.Contains(errOut, "1 on your list couldn't be checked") {
		t.Fatalf("mixed list: code=%d out=%q err=%q", code, out, errOut)
	}
	if _, out, _ := run(t, "list"); out != "test.example\nold.example\nnobody.example\n" {
		t.Fatalf("list output must stay one handle per line for completion: %q", out)
	}
}

func TestName(t *testing.T) {
	pds := setup(t)
	run(t, "login", "test.example")

	// An existing profile with other fields that must survive the edit.
	pds.mu.Lock()
	pds.records = append(pds.records, fakeRec{"app.bsky.actor.profile", "self", "cid-profile", map[string]any{
		"$type": "app.bsky.actor.profile", "displayName": "Old Name", "description": "I like donuts",
		"avatar": map[string]any{"$type": "blob", "ref": map[string]any{"$link": "bafkrei123"}, "mimeType": "image/png", "size": 12345},
	}})
	pds.mu.Unlock()

	if _, out, _ := run(t, "name"); out != "Old Name\n" {
		t.Fatalf("show name: %q", out)
	}
	if code, out, e := run(t, "name", "Matt", "Royer"); code != 0 || !strings.Contains(out, "Name set to Matt Royer.") {
		t.Fatalf("set name: %d %q %q", code, out, e)
	}
	if _, out, _ := run(t, "test.example"); !strings.Contains(out, "Name: Matt Royer") {
		t.Fatalf("finger after setting name:\n%s", out)
	}
	pds.mu.Lock()
	var v map[string]any
	for _, r := range pds.records {
		if r.coll == "app.bsky.actor.profile" {
			v = r.value
		}
	}
	pds.mu.Unlock()
	if v["description"] != "I like donuts" || v["avatar"] == nil || v["$type"] != "app.bsky.actor.profile" {
		t.Fatalf("other profile fields were lost: %v", v)
	}
	if size := v["avatar"].(map[string]any)["size"]; size != float64(12345) {
		t.Fatalf("avatar size changed: %v", size)
	}

	run(t, "name", "")
	if _, out, _ := run(t, "name"); !strings.Contains(out, "No name set") {
		t.Fatalf("after clearing: %q", out)
	}

	if code, _, e := run(t, "name", "Bad\x1b[2JName"); code == 0 || !strings.Contains(e, "control characters") {
		t.Fatalf("control characters accepted: %q", e)
	}
	if code, _, e := run(t, "name", strings.Repeat("x", 65)); code == 0 || !strings.Contains(e, "limit is 64") {
		t.Fatalf("long name accepted: %q", e)
	}
}

func TestNameWithNoProfileYet(t *testing.T) {
	pds := setup(t)
	pds.noProfile = true
	run(t, "login", "test.example")
	if _, out, _ := run(t, "name"); !strings.Contains(out, "No name set") {
		t.Fatalf("no profile: %q", out)
	}
	if code, _, e := run(t, "name", "Homer Simpson"); code != 0 {
		t.Fatalf("creating profile: %q", e)
	}
	if _, out, _ := run(t, "name"); out != "Homer Simpson\n" {
		t.Fatalf("after creating: %q", out)
	}
}

func TestBareThumbShowsYourPlan(t *testing.T) {
	setup(t)
	if code, _, e := run(t); code != 2 || !strings.Contains(e, "Usage:") {
		t.Fatalf("logged out: code=%d err=%q", code, e)
	}
	run(t, "login", "test.example")
	run(t, "post", "-m", "my own plan")
	code, out, _ := run(t)
	if code != 0 || !strings.HasPrefix(out, "Login: test.example") || !strings.Contains(out, "my own plan") {
		t.Fatalf("logged in: code=%d out=%q", code, out)
	}
	if _, list, _ := run(t, "list"); list != "" {
		t.Fatalf("looking at your own plan shouldn't add you to your list: %q", list)
	}
}

func TestPlanSizeLimit(t *testing.T) {
	setup(t)
	run(t, "login", "test.example")
	if code, _, e := run(t, "post", "-m", strings.Repeat("x", maxPlanBytes+1)); code == 0 || !strings.Contains(e, "the limit is") {
		t.Fatalf("oversized plan accepted: %q", e)
	}
	if code, out, e := run(t, "post", "-m", strings.Repeat("x", maxPlanBytes)); code != 0 || !strings.Contains(out, "Published") {
		t.Fatalf("plan at the limit rejected: %q %q", out, e)
	}
}
