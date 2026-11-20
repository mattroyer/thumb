package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
)

type session struct {
	DID        string `json:"did"`
	Handle     string `json:"handle"`
	PDS        string `json:"pds"`
	AccessJwt  string `json:"accessJwt"`
	RefreshJwt string `json:"refreshJwt"`
}

// configDir is ~/.config/thumb on Linux, ~/Library/Application Support/thumb
// on macOS and %AppData%\thumb on Windows.
func configDir() string {
	if d := os.Getenv("THUMB_CONFIG_DIR"); d != "" {
		return d
	}
	d, err := os.UserConfigDir()
	if err != nil {
		d = "."
	}
	return filepath.Join(d, "thumb")
}

func sessionPath() string { return filepath.Join(configDir(), "session.json") }

func loadSession() (*session, error) {
	b, err := os.ReadFile(sessionPath())
	if err != nil {
		return nil, errors.New("not logged in; run: thumb login")
	}
	var s session
	if err := json.Unmarshal(b, &s); err != nil || s.AccessJwt == "" {
		return nil, errors.New("session file is damaged; run: thumb login")
	}
	return &s, nil
}

func saveSession(s *session) error {
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := sessionPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, sessionPath())
}

// xrpcAuthed calls an XRPC method on the signed-in user's PDS, refreshing the
// access token once if it has expired.
func xrpcAuthed(s *session, method, nsid string, params url.Values, body, out any) error {
	endpoint := s.PDS + "/xrpc/" + nsid
	err := doJSON(method, endpoint, params, body, s.AccessJwt, out)
	var xe *xrpcError
	if err == nil || !errors.As(err, &xe) ||
		!(xe.Code == "ExpiredToken" || xe.Code == "InvalidToken" || xe.Status == 401) {
		return err
	}

	var r struct {
		AccessJwt  string `json:"accessJwt"`
		RefreshJwt string `json:"refreshJwt"`
	}
	if err := doJSON("POST", s.PDS+"/xrpc/com.atproto.server.refreshSession",
		nil, nil, s.RefreshJwt, &r); err != nil {
		return errors.New("session expired; run: thumb login")
	}
	s.AccessJwt, s.RefreshJwt = r.AccessJwt, r.RefreshJwt
	if err := saveSession(s); err != nil {
		return err
	}
	return doJSON(method, endpoint, params, body, s.AccessJwt, out)
}
