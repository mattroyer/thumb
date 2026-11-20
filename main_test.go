package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const testDID = "did:plc:testuser123"

// fakePDS is a tiny in-memory PDS: enough XRPC to exercise every command.
type fakePDS struct {
	mu      sync.Mutex
	access  string
	records []fakeRec
	n       int
}

type fakeRec struct {
	coll, rkey, cid string
	value           map[string]any
}

func (f *fakePDS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	var body map[string]any
	if r.Method == "POST" {
		json.NewDecoder(r.Body).Decode(&body)
	}
	nsid := strings.TrimPrefix(r.URL.Path, "/xrpc/")
	authed := r.Header.Get("Authorization") == "Bearer "+f.access

	switch nsid {
	case "com.atproto.repo.listRecords":
		q := r.URL.Query()
		var matching []fakeRec
		for _, rec := range f.records {
			if rec.coll == q.Get("collection") {
				matching = append(matching, rec)
			}
		}
		sort.Slice(matching, func(i, j int) bool { return matching[i].rkey > matching[j].rkey }) // newest first, like a real PDS
		var out []map[string]any
		for _, rec := range matching {
			if len(out) == atoi(q.Get("limit")) {
				break
			}
			out = append(out, map[string]any{
				"uri": fmt.Sprintf("at://%s/%s/%s", testDID, rec.coll, rec.rkey),
				"cid": rec.cid, "value": rec.value,
			})
		}
		reply(200, map[string]any{"records": out})
	case "com.atproto.repo.getRecord":
		reply(200, map[string]any{"value": map[string]any{"displayName": "Test \x1b[31mUser"}})
	case "com.atproto.server.createSession":
		reply(200, map[string]any{"did": testDID, "handle": "test.example",
			"accessJwt": f.access, "refreshJwt": "refresh-1"})
	case "com.atproto.server.refreshSession":
		f.access = "access-2"
		reply(200, map[string]any{"accessJwt": "access-2", "refreshJwt": "refresh-2"})
	case "com.atproto.server.deleteSession":
		reply(200, map[string]any{})
	default:
		if !authed {
			reply(400, map[string]any{"error": "ExpiredToken", "message": "Token has expired"})
			return
		}
		switch nsid {
		case "com.atproto.repo.createRecord":
			f.n++
			rkey := fmt.Sprintf("3kplan%04d", f.n)
			coll := body["collection"].(string)
			f.records = append(f.records, fakeRec{coll, rkey, fmt.Sprintf("cid%d", f.n),
				body["record"].(map[string]any)})
			reply(200, map[string]any{"uri": "at://" + testDID + "/" + coll + "/" + rkey})
		case "com.atproto.repo.putRecord":
			coll, rkey := body["collection"].(string), body["rkey"].(string)
			found := false
			for i := range f.records {
				if f.records[i].coll == coll && f.records[i].rkey == rkey {
					if swap, ok := body["swapRecord"]; ok && swap != f.records[i].cid {
						reply(400, map[string]any{"error": "InvalidSwap", "message": "swap mismatch"})
						return
					}
					f.records[i].value = body["record"].(map[string]any)
					f.records[i].cid += "x"
					found = true
				}
			}
			if !found { // putRecord creates the record if it doesn't exist
				f.n++
				f.records = append(f.records, fakeRec{coll, rkey, fmt.Sprintf("cid%d", f.n),
					body["record"].(map[string]any)})
			}
			reply(200, map[string]any{"uri": "at://" + testDID + "/" + coll + "/" + rkey})
		case "com.atproto.repo.deleteRecord":
			kept := f.records[:0]
			for _, rec := range f.records {
				if rec.coll != body["collection"] || rec.rkey != body["rkey"] {
					kept = append(kept, rec)
				}
			}
			f.records = kept
			reply(200, map[string]any{})
		default:
			reply(404, map[string]any{"error": "MethodNotImplemented", "message": nsid})
		}
	}
}

func atoi(s string) int {
	n := 0
	fmt.Sscan(s, &n)
	return n
}

func setup(t *testing.T) *fakePDS {
	t.Helper()
	pds := &fakePDS{access: "access-1"}
	srv := httptest.NewServer(pds)
	t.Cleanup(srv.Close)

	oldH, oldD := resolveHandle, resolveDID
	resolveHandle = func(h string) (string, error) {
		if h == "nobody.example" {
			return "", fmt.Errorf("%s: no such user", h)
		}
		return testDID, nil
	}
	resolveDID = func(string) (string, string, error) { return srv.URL, "test.example", nil }
	t.Cleanup(func() { resolveHandle, resolveDID = oldH, oldD })

	t.Setenv("THUMB_CONFIG_DIR", t.TempDir())
	t.Setenv("THUMB_APP_PASSWORD", "xxxx-xxxx-xxxx-xxxx")
	return pds
}

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	a := &app{in: strings.NewReader(""), out: &out, errw: &errb, stdinTTY: true}
	code := a.run(args)
	return code, out.String(), errb.String()
}

func TestFullFlow(t *testing.T) {
	pds := setup(t)

	if _, out, _ := run(t, "test.example"); !strings.Contains(out, "No Plan.") {
		t.Fatalf("expected No Plan, got %q", out)
	}
	if code, _, e := run(t, "post", "-m", "hi"); code != 1 || !strings.Contains(e, "not logged in") {
		t.Fatalf("post without login: code=%d err=%q", code, e)
	}

	if code, out, e := run(t, "login", "@Test.Example"); code != 0 || !strings.Contains(out, testDID) {
		t.Fatalf("login failed: %d %q %q", code, out, e)
	}
	if fi, _ := os.Stat(sessionPath()); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("session file mode = %v", fi.Mode().Perm())
	}

	run(t, "post", "-m", "* got shadow volumes working\n+ fixed the \x1b[2J bug")

	// Make the stored token stale; the next write must refresh and retry.
	pds.mu.Lock()
	pds.access = "rotated-on-server"
	pds.mu.Unlock()
	if code, _, e := run(t, "post", "-m", "second entry"); code != 0 {
		t.Fatalf("post after expiry: %q", e)
	}
	b, _ := os.ReadFile(sessionPath())
	if !strings.Contains(string(b), "access-2") {
		t.Fatalf("session not refreshed: %s", b)
	}

	_, out, _ := run(t, "test.example")
	if !strings.Contains(out, "second entry") || strings.Contains(out, "shadow") {
		t.Fatalf("latest plan wrong:\n%s", out)
	}
	if !strings.Contains(out, "Name: Test [31mUser") || strings.Contains(out, "\x1b") {
		t.Fatalf("escape codes not stripped:\n%q", out)
	}

	// Flags after the handle work too.
	_, out, _ = run(t, "test.example", "-n", "5")
	if !strings.Contains(out, "shadow volumes") || strings.Contains(out, "\x1b") ||
		strings.Index(out, "second entry") > strings.Index(out, "shadow") {
		t.Fatalf("history wrong:\n%q", out)
	}

	run(t, "post", "--amend", "-m", "second entry (edited)")
	if _, out, _ = run(t, "test.example"); !strings.Contains(out, "(edited)") {
		t.Fatalf("amend failed:\n%s", out)
	}

	run(t, "delete", "latest")
	if _, out, _ = run(t, "test.example"); !strings.Contains(out, "shadow volumes") {
		t.Fatalf("delete failed:\n%s", out)
	}

	if _, out, _ = run(t, "whoami"); !strings.Contains(out, "test.example") {
		t.Fatalf("whoami: %q", out)
	}
	run(t, "logout")
	if _, err := os.Stat(sessionPath()); !os.IsNotExist(err) {
		t.Fatal("logout left the session file behind")
	}
}

func TestUnknownUserExitCode(t *testing.T) {
	setup(t)
	code, out, e := run(t, "test.example", "nobody.example")
	if code != 1 || !strings.Contains(out, "Login: test.example") || !strings.Contains(e, "no such user") {
		t.Fatalf("code=%d out=%q err=%q", code, out, e)
	}
}

func TestFingerGateway(t *testing.T) {
	setup(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go serveFinger(ln, 1)
	defer ln.Close()

	query := func(q string) string {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(5 * time.Second))
		io.WriteString(c, q)
		b, _ := io.ReadAll(c)
		return string(b)
	}
	if out := query("test.example\r\n"); !strings.HasPrefix(out, "Login: test.example") || !strings.Contains(out, "\r\n") {
		t.Fatalf("gateway: %q", out)
	}
	if out := query("/W test.example\r\n"); !strings.Contains(out, "DID:   "+testDID) {
		t.Fatalf("gateway /W: %q", out)
	}
	if out := query("a@b@c\r\n"); !strings.Contains(out, "forwarding") {
		t.Fatalf("gateway forwarding: %q", out)
	}
}

func TestHelpers(t *testing.T) {
	if got := sanitize("ok\tline\n\x1b[2Jbad\x07\u0085"); got != "ok\tline\n[2Jbad" {
		t.Errorf("sanitize = %q", got)
	}
	if got := normalize("  @Alice.BSKY.social "); got != "alice.bsky.social" {
		t.Errorf("normalize = %q", got)
	}
	if got := normalize("at://alice.example/date.thumbplan.plan/abc"); got != "alice.example" {
		t.Errorf("normalize at-uri = %q", got)
	}
	if fmtTime("2026-11-20T14:02:03.123456789Z") == "2026-11-20T14:02:03.123456789Z" {
		t.Error("fmtTime did not parse nanosecond timestamp")
	}
	if fmtTime("garbage") != "garbage" {
		t.Error("fmtTime should pass through unparseable input")
	}
	if code, out, _ := run(t, "version"); code != 0 || !strings.HasPrefix(out, "thumb ") {
		t.Errorf("version: %d %q", code, out)
	}
	if code, _, _ := run(t, "-n", "notanumber", "x"); code != 2 {
		t.Errorf("bad flag should exit 2, got %d", code)
	}
}

func TestHandleSuggestions(t *testing.T) {
	setup(t)
	// Only *.bsky.social handles exist in this fake world.
	resolveHandle = func(h string) (string, error) {
		if strings.HasSuffix(h, ".bsky.social") && h != "ghost.bsky.social" {
			return testDID, nil
		}
		return "", fmt.Errorf("%s: no such user", h)
	}
	cases := []struct{ in, want string }{
		{"matthewroyer.bsky.app", "Did you mean matthewroyer.bsky.social? (bsky.app is Bluesky's website"},
		{"matthewroyer", "Did you mean matthewroyer.bsky.social? (handles include a domain)"},
		{"ghost.bsky.app", "ghost.bsky.app: no such user"}, // no suggestion if the guess doesn't exist either
		{"someone.example.com", "someone.example.com: no such user"},
	}
	for _, c := range cases {
		_, err := lookup(c.in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("lookup(%q) error = %v, want it to contain %q", c.in, err, c.want)
		}
	}
	for _, in := range []string{
		"https://bsky.app/profile/matthewroyer.bsky.social",
		"bsky.app/profile/@matthewroyer.bsky.social/post/3abc",
	} {
		if got := normalize(in); got != "matthewroyer.bsky.social" {
			t.Errorf("normalize(%q) = %q", in, got)
		}
	}
	// The suggestion also reaches the user through `thumb login`.
	if code, _, e := run(t, "login", "matthewroyer.bsky.app"); code != 1 || !strings.Contains(e, "Did you mean matthewroyer.bsky.social?") {
		t.Errorf("login suggestion: code=%d err=%q", code, e)
	}
}

func TestPlanFile(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	plan := filepath.Join(dir, "my.plan")
	t.Setenv("THUMB_PLAN", plan)
	run(t, "login", "test.example")

	// post with no file yet points you at `thumb edit`.
	if code, _, e := run(t, "post"); code != 1 || !strings.Contains(e, "thumb edit") {
		t.Fatalf("post without plan file: %d %q", code, e)
	}

	os.WriteFile(plan, []byte("* wrote this in my own editor\r\n\n"), 0o644)
	if code, out, e := run(t, "post"); code != 0 || !strings.Contains(out, "Published") {
		t.Fatalf("post from file: %d %q %q", code, out, e)
	}
	if _, out, _ := run(t, "post"); !strings.Contains(out, "Same as your latest entry") {
		t.Fatalf("duplicate post not skipped: %q", out)
	}
	if _, out, _ := run(t, "test.example"); !strings.Contains(out, "wrote this in my own editor") {
		t.Fatalf("finger after file post:\n%s", out)
	}

	// -f overrides THUMB_PLAN.
	other := filepath.Join(dir, "other.plan")
	os.WriteFile(other, []byte("from -f"), 0o644)
	if _, out, _ := run(t, "post", "-f", other); !strings.Contains(out, "Published") {
		t.Fatalf("post -f: %q", out)
	}

	if runtime.GOOS == "windows" {
		return // the fake editors below are shell scripts
	}
	// edit on a missing file seeds it from your latest entry, then publishes your change.
	os.Remove(plan)
	editor := filepath.Join(dir, "fake-editor.sh")
	os.WriteFile(editor, []byte("#!/bin/sh\necho '+ added in the editor' >> \"$1\"\n"), 0o755)
	t.Setenv("VISUAL", editor)
	code, out, e := run(t, "edit")
	if code != 0 || !strings.Contains(out, "Published") || !strings.Contains(e, "Created "+plan) {
		t.Fatalf("edit: %d %q %q", code, out, e)
	}
	b, _ := os.ReadFile(plan)
	if string(b) != "from -f\n+ added in the editor\n" {
		t.Fatalf("plan file after edit = %q", b)
	}

	t.Setenv("VISUAL", "true") // an editor that changes nothing
	if _, out, _ := run(t, "edit"); !strings.Contains(out, "No changes") {
		t.Fatalf("unchanged edit: %q", out)
	}
}

func TestMigrate(t *testing.T) {
	pds := setup(t)
	run(t, "login", "test.example")
	if _, out, _ := run(t, "migrate"); !strings.Contains(out, "Nothing to migrate") {
		t.Fatalf("empty migrate: %q", out)
	}

	// Two old entries under the placeholder name, plus one already in the new one.
	pds.mu.Lock()
	old := func(rkey, text string) fakeRec {
		return fakeRec{legacyCollection, rkey, "cid-" + rkey, map[string]any{
			"$type": legacyCollection, "text": text, "createdAt": "2026-11-20T10:00:00.000Z"}}
	}
	pds.records = append(pds.records, old("3kaaa0001", "first ever post"), old("3kaaa0002", "second post"))
	pds.mu.Unlock()
	run(t, "post", "-m", "posted after the rename")

	if _, out, _ := run(t, "migrate", "--dry-run"); !strings.Contains(out, "Moving 2 entries") || !strings.Contains(out, "Dry run") {
		t.Fatalf("dry run: %q", out)
	}
	if code, out, e := run(t, "migrate"); code != 0 || !strings.Contains(out, "Done.") {
		t.Fatalf("migrate: %d %q %q", code, out, e)
	}

	pds.mu.Lock()
	for _, rec := range pds.records {
		if rec.coll != collection || rec.value["$type"] != collection {
			t.Errorf("record left behind or mistyped: %+v", rec)
		}
	}
	pds.mu.Unlock()

	// History reads in the right order, with the migrated entries keeping their keys.
	_, out, _ := run(t, "-n", "5", "test.example")
	a, b, c := strings.Index(out, "posted after the rename"), strings.Index(out, "second post"), strings.Index(out, "first ever post")
	if a < 0 || b < 0 || c < 0 || !(a < b && b < c) || !strings.Contains(out, "[3kaaa0001]") {
		t.Fatalf("history after migrate:\n%s", out)
	}
	if _, out, _ := run(t, "migrate"); !strings.Contains(out, "Nothing to migrate") {
		t.Fatalf("second migrate should be a no-op: %q", out)
	}
}

func TestPlanPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("THUMB_PLAN", "")
	if p, _ := planPath(""); p != filepath.Join(home, ".plan") {
		t.Errorf("default = %q", p)
	}
	if p, _ := planPath("~/notes/plan.txt"); p != filepath.Join(home, "notes", "plan.txt") {
		t.Errorf("tilde = %q", p)
	}
	t.Setenv("THUMB_PLAN", "~/.thumb/.plan")
	if p, _ := planPath(""); p != filepath.Join(home, ".thumb", ".plan") {
		t.Errorf("env = %q", p)
	}
}

func TestSessionDirCreated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "thumb")
	t.Setenv("THUMB_CONFIG_DIR", dir)
	if err := saveSession(&session{AccessJwt: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSession(); err != nil {
		t.Fatal(err)
	}
}
