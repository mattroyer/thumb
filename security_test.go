package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

// A server that answers every request with an XRPC error whose message
// carries terminal escape codes (here: clear screen, set window title).
func hostileServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":"InvalidRequest","message":"\u001b[2J\u001b]0;pwned\u0007gotcha"}`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// Escape codes in a remote server's error message must not reach the terminal.
func TestRemoteErrorsAreSanitized(t *testing.T) {
	setup(t)
	evil := hostileServer(t)
	resolveDID = func(string) (string, string, error) { return evil, "test.example", nil }

	_, _, errOut := run(t, "test.example")
	if strings.ContainsAny(errOut, "\x1b\x07") {
		t.Fatalf("escape codes reached the terminal: %q", errOut)
	}
	if !strings.Contains(errOut, "gotcha") {
		t.Fatalf("error text itself should still be shown: %q", errOut)
	}
}

// Fingering a DID must not display a handle the DID document merely claims.
func TestDIDCannotSpoofHandle(t *testing.T) {
	setup(t)
	// The attacker's DID document claims to be jay.bsky.team...
	resolveDID = func(did string) (string, string, error) { return pdsURL(t), "jay.bsky.team", nil }
	// ...but jay.bsky.team actually belongs to someone else.
	resolveHandle = func(h string) (string, error) {
		if strings.HasPrefix(h, "did:") {
			return h, nil
		}
		if h == "jay.bsky.team" {
			return "did:plc:realjayrealjayrealjayreal", nil
		}
		return "", fmt.Errorf("%s: no such user", h)
	}
	_, out, _ := run(t, "did:plc:attackerattackerattacker")
	if strings.HasPrefix(out, "Login: jay.bsky.team ") {
		t.Fatalf("spoofed handle shown as if verified:\n%s", out)
	}
	if !strings.Contains(out, "unverified") {
		t.Fatalf("expected an unverified marker:\n%s", out)
	}
}

// Login must refuse to send a password when the handle can't be verified.
func TestLoginRequiresVerifiedHandle(t *testing.T) {
	setup(t)
	resolveDID = func(string) (string, string, error) { return pdsURL(t), "someone-else.example", nil }
	code, out, errOut := run(t, "login", "test.example")
	if code == 0 || !strings.Contains(errOut, "verify") {
		t.Fatalf("login should refuse an unverified handle: code=%d out=%q err=%q", code, out, errOut)
	}
}

// pdsURL returns the fake PDS that setup() started.
func pdsURL(t *testing.T) string {
	t.Helper()
	return fakePDSURL
}

// The gateway's locked-down client must not reach loopback/private addresses,
// even when a DID document points there.
func TestGatewayRefusesPrivateAddresses(t *testing.T) {
	var hits int
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		fmt.Fprint(w, `{"records":[]}`)
	}))
	defer internal.Close()

	old := httpClient
	httpClient = newHTTPClient(true)
	defer func() { httpClient = old }()

	err := doJSON("GET", internal.URL+"/xrpc/com.atproto.repo.listRecords", nil, nil, "", nil)
	if err == nil || !strings.Contains(err.Error(), "non-public address") || hits != 0 {
		t.Fatalf("locked-down client reached a loopback server: err=%v hits=%d", err, hits)
	}
	// The normal client (used by the CLI) still can.
	httpClient = old
	if err := doJSON("GET", internal.URL+"/x", nil, nil, "", nil); err != nil || hits != 1 {
		t.Fatalf("normal client: err=%v hits=%d", err, hits)
	}
}

func TestIsPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false,
		"169.254.169.254": false, "100.100.100.100": false, "0.0.0.0": false, "::1": false,
		"fe80::1": false, "fc00::1": false, "::ffff:127.0.0.1": false, "64:ff9b::a00:1": false,
		"2002:c0a8:101::1": false, "198.18.0.1": false, "255.255.255.255": false,
	} {
		if got := isPublicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("isPublicAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestRedirectToPlainHTTPRefused(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{}`)
	}))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/stolen", http.StatusFound)
	}))
	defer secure.Close()

	old := httpClient
	httpClient = newHTTPClient(false)
	httpClient.Transport.(*http.Transport).TLSClientConfig = secure.Client().Transport.(*http.Transport).TLSClientConfig
	defer func() { httpClient = old }()

	err := doJSON("GET", secure.URL, nil, nil, "secret-token", nil)
	if err == nil || !strings.Contains(err.Error(), "refusing redirect") {
		t.Fatalf("HTTPS to HTTP redirect was followed: %v", err)
	}
}

func TestValidDID(t *testing.T) {
	good := []string{"did:plc:z72i7hdynmk6r22z27h6tvur", "did:web:example.com", "did:web:blog.example.co.uk"}
	bad := []string{
		"did:plc:short", "did:plc:z72i7hdynmk6r22z27h6tvu1", "did:plc:z72i7hdynmk6r22z27h6tvur/../x",
		"did:web:localhost", "did:web:127.0.0.1", "did:web:example.com%3A8080", "did:web:example.com:path",
		"did:web:ex ample.com", "did:key:z6Mk", "did:plc:z72i7hdynmk6r22z27h6tvur\x1b[2J", "",
	}
	for _, d := range good {
		if !validDID(d) {
			t.Errorf("validDID(%q) = false, want true", d)
		}
	}
	for _, d := range bad {
		if validDID(d) {
			t.Errorf("validDID(%q) = true, want false", d)
		}
	}
}

func TestPickTXT(t *testing.T) {
	if did, ok := pickTXT([]string{"v=spf1 -all", "did=did:plc:z72i7hdynmk6r22z27h6tvur"}); !ok || did != "did:plc:z72i7hdynmk6r22z27h6tvur" {
		t.Errorf("single record: %q %v", did, ok)
	}
	if _, ok := pickTXT([]string{"did=did:plc:z72i7hdynmk6r22z27h6tvur", "did=did:plc:aaaaaaaaaaaaaaaaaaaaaaaa"}); ok {
		t.Error("two did= records must be rejected")
	}
	if _, ok := pickTXT([]string{"did=did:web:localhost"}); ok {
		t.Error("invalid DID in TXT must be rejected")
	}
}

func TestCheckPDS(t *testing.T) {
	t.Setenv("THUMB_ALLOW_HTTP_PDS", "")
	if got, err := checkPDS("https://pds.example.com/"); err != nil || got != "https://pds.example.com" {
		t.Errorf("https: %q %v", got, err)
	}
	for _, bad := range []string{"http://pds.example.com", "ftp://x", "https://user:pw@pds.example.com",
		"https://pds.example.com/xrpc", "https://pds.example.com?x=1", "not a url", "https://"} {
		if _, err := checkPDS(bad); err == nil {
			t.Errorf("checkPDS(%q) should fail", bad)
		}
	}
	t.Setenv("THUMB_ALLOW_HTTP_PDS", "1")
	if _, err := checkPDS("http://localhost:2583"); err != nil {
		t.Errorf("http should be allowed with THUMB_ALLOW_HTTP_PDS=1: %v", err)
	}
}

// End to end through the real resolveDID: a DID document must describe the
// DID that was asked for, and must list an https PDS.
func TestResolveDIDChecksDocument(t *testing.T) {
	const did = "did:plc:z72i7hdynmk6r22z27h6tvur"
	var docID, endpoint string
	dir := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":%q,"alsoKnownAs":["at://Alice.Example.com"],"service":[{"id":"#atproto_pds","serviceEndpoint":%q}]}`, docID, endpoint)
	}))
	defer dir.Close()
	oldClient, oldPLC := httpClient, plcDirectory
	httpClient, plcDirectory = dir.Client(), dir.URL
	defer func() { httpClient, plcDirectory = oldClient, oldPLC }()
	t.Setenv("THUMB_ALLOW_HTTP_PDS", "")

	docID, endpoint = did, "https://pds.example.com"
	if pds, h, err := resolveDID(did); err != nil || pds != "https://pds.example.com" || h != "alice.example.com" {
		t.Fatalf("valid document: %q %q %v", pds, h, err)
	}
	docID, endpoint = "did:plc:aaaaaaaaaaaaaaaaaaaaaaaa", "https://pds.example.com"
	if _, _, err := resolveDID(did); err == nil {
		t.Error("document for a different DID was accepted")
	}
	docID, endpoint = did, "http://169.254.169.254"
	if _, _, err := resolveDID(did); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("plain-HTTP PDS was accepted: %v", err)
	}
	if _, _, err := resolveDID("did:web:localhost%3A8080"); err == nil {
		t.Error("malformed did:web was accepted")
	}
}

func TestSanitizeStripsBidi(t *testing.T) {
	if got := sanitize("pay ‮evil‬ to ⁦me⁩"); got != "pay evil to me" {
		t.Errorf("sanitize = %q", got)
	}
}

// When you look up a DID whose claimed handle does check out, show it.
func TestDIDWithVerifiedHandle(t *testing.T) {
	setup(t)
	resolveHandle = func(h string) (string, error) {
		if strings.HasPrefix(h, "did:") {
			return h, nil
		}
		return testDID, nil
	}
	if _, out, _ := run(t, testDID); !strings.HasPrefix(out, "Login: test.example ") || strings.Contains(out, "unverified") {
		t.Fatalf("verified DID lookup:\n%s", out)
	}
}
