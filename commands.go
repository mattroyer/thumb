package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

func (a *app) cmdLogin(args []string) error {
	fs := a.flags("login", "thumb login [handle]")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	handle := ""
	if len(pos) > 0 {
		handle = pos[0]
	} else {
		fmt.Fprint(a.errw, "Handle: ")
		line, _ := bufio.NewReader(a.in).ReadString('\n')
		handle = line
	}
	handle = normalize(handle)
	if handle == "" {
		return errors.New("no handle given")
	}
	// Check the handle before asking for a password, so typos fail fast.
	id, err := lookup(handle)
	if err != nil {
		return err
	}
	handle = id.Handle

	password := os.Getenv("THUMB_APP_PASSWORD")
	if password == "" {
		if !a.stdinTTY {
			return errors.New("set THUMB_APP_PASSWORD to log in non-interactively")
		}
		fmt.Fprint(a.errw, "App password (Settings > Privacy and security > App passwords): ")
		pw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(a.errw)
		if err != nil {
			return err
		}
		password = string(pw)
	}

	var r struct {
		DID        string `json:"did"`
		Handle     string `json:"handle"`
		AccessJwt  string `json:"accessJwt"`
		RefreshJwt string `json:"refreshJwt"`
	}
	err = doJSON("POST", id.PDS+"/xrpc/com.atproto.server.createSession", nil,
		map[string]string{"identifier": handle, "password": password}, "", &r)
	if err != nil {
		return err
	}
	if r.Handle == "" {
		r.Handle = handle
	}
	if err := saveSession(&session{DID: r.DID, Handle: r.Handle, PDS: id.PDS,
		AccessJwt: r.AccessJwt, RefreshJwt: r.RefreshJwt}); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "Logged in as %s (%s)\n", r.Handle, r.DID)
	return nil
}

func (a *app) cmdLogout(args []string) error {
	if s, err := loadSession(); err == nil {
		_ = doJSON("POST", s.PDS+"/xrpc/com.atproto.server.deleteSession", nil, nil, s.RefreshJwt, nil)
		if err := os.Remove(sessionPath()); err != nil {
			return err
		}
	}
	fmt.Fprintln(a.out, "Logged out.")
	return nil
}

func (a *app) cmdWhoami(args []string) error {
	s, err := loadSession()
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "%s  %s  %s\n", s.Handle, s.DID, s.PDS)
	return nil
}

func latestOwn(s *session) (*planRecord, error) {
	recs, err := listPlans(s.PDS, s.DID, 1)
	if err != nil || len(recs) == 0 {
		return nil, err
	}
	return &recs[0], nil
}

func (a *app) cmdPost(args []string) error {
	fs := a.flags("post", "thumb post [-m text] [-f planfile] [--amend]")
	msg := fs.String("m", "", "plan text (otherwise stdin, otherwise your plan file)")
	file := fs.String("f", "", "plan file to publish (default $THUMB_PLAN or ~/.plan)")
	amend := fs.Bool("amend", false, "replace your latest entry instead of adding one")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	msgSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "m" {
			msgSet = true
		}
	})

	s, err := loadSession()
	if err != nil {
		return err
	}

	var text string
	switch {
	case msgSet:
		text = *msg
	case !a.stdinTTY && *file == "":
		b, err := io.ReadAll(io.LimitReader(a.in, 1<<20))
		if err != nil {
			return err
		}
		text = string(b)
	default:
		path, err := planPath(*file)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no plan file at %s; create it with: thumb edit", path)
		}
		if err != nil {
			return err
		}
		text = string(b)
	}
	return a.publish(s, text, *amend)
}

func (a *app) cmdDelete(args []string) error {
	fs := a.flags("delete", "thumb delete <rkey|latest>")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fs.Usage()
		return flagError{errors.New("need exactly one record key")}
	}
	s, err := loadSession()
	if err != nil {
		return err
	}
	rkey := pos[0]
	if rkey == "latest" {
		prev, err := latestOwn(s)
		if err != nil {
			return err
		}
		if prev == nil {
			return errors.New("you have no plan entries")
		}
		rkey = prev.rkey()
	}
	err = xrpcAuthed(s, "POST", "com.atproto.repo.deleteRecord", nil, map[string]any{
		"repo": s.DID, "collection": collection, "rkey": rkey,
	}, nil)
	if err == nil {
		fmt.Fprintf(a.out, "Deleted %s\n", rkey)
	}
	return err
}
