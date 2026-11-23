// thumb: finger(1) and .plan files over the AT Protocol.
//
// Your .plan lives in your own AT Protocol repo (Bluesky's PDS or your own) as
// records in a custom collection. Anyone can finger you with just your handle;
// reading needs no account and no API key.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// Set at build time: -ldflags "-X main.version=1.2.3"
var version = "dev"

const usage = `thumb: finger and .plan files over the AT Protocol

Usage:
  thumb <handle>...                 show a user's latest plan
  thumb [-n N] [-l] <handle>...     show N entries; -l adds DID and PDS
  thumb login [handle]              sign in with an app password
  thumb logout                      end the session
  thumb whoami                      show the signed-in account
  thumb edit [-f file] [--amend]    edit your plan file in $EDITOR, then publish it
  thumb post [-f file] [--amend]    publish your plan file as it is now
  thumb post -m text                publish text directly (or pipe it in on stdin)
  thumb delete <rkey|latest>        delete a plan entry
  thumb                             your own plan (when logged in)
  thumb new                         who on your list has posted since you last looked
  thumb name ["Your Name"]          show or set the name shown next to Name:
  thumb list [--clear]              everyone you've thumbed, newest first
  thumb completion bash|zsh|fish    print a tab-completion script for your shell
  thumb migrate [--dry-run]         move entries from the old dev.thumb.plan name
  thumb serve [--port 79]           run an RFC 1288 finger gateway
  thumb version

Your plan file is ~/.plan unless you pass -f or set THUMB_PLAN.

Environment:
  THUMB_PLAN          path to your plan file (default ~/.plan)
  THUMB_NO_LIST=1     don't remember who you thumb
  THUMB_COLLECTION    record collection (default date.thumbplan.plan)
  THUMB_APP_PASSWORD  app password for non-interactive login
  THUMB_CONFIG_DIR    where the session is stored
`

var commands = map[string]bool{
	"finger": true, "login": true, "logout": true, "whoami": true,
	"post": true, "edit": true, "delete": true, "migrate": true, "serve": true,
	"list": true, "completion": true, "new": true, "name": true,
}

// errSilent means the error was already reported; just exit non-zero.
var errSilent = errors.New("")

type flagError struct{ err error }

func (f flagError) Error() string { return f.err.Error() }

type app struct {
	in       io.Reader
	out      io.Writer
	errw     io.Writer
	stdinTTY bool
}

func main() {
	a := &app{
		in:       os.Stdin,
		out:      os.Stdout,
		errw:     os.Stderr,
		stdinTTY: term.IsTerminal(int(os.Stdin.Fd())),
	}
	os.Exit(a.run(os.Args[1:]))
}

func (a *app) run(args []string) int {
	if len(args) == 0 {
		// Like finger with no arguments: show your own plan if you're logged in.
		s, err := loadSession()
		if err != nil {
			fmt.Fprint(a.errw, usage)
			return 2
		}
		out, err := render(s.DID, 1, false)
		if err != nil {
			fmt.Fprintf(a.errw, "thumb: %s\n", sanitize(err.Error()))
			return 1
		}
		fmt.Fprint(a.out, out)
		return 0
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(a.out, usage)
		return 0
	case "-v", "--version", "version":
		fmt.Fprintf(a.out, "thumb %s\n", version)
		return 0
	}
	// `thumb alice.bsky.social` is shorthand for `thumb finger alice.bsky.social`
	if !commands[args[0]] {
		args = append([]string{"finger"}, args...)
	}

	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "finger":
		err = a.cmdFinger(rest)
	case "login":
		err = a.cmdLogin(rest)
	case "logout":
		err = a.cmdLogout(rest)
	case "whoami":
		err = a.cmdWhoami(rest)
	case "post":
		err = a.cmdPost(rest)
	case "edit":
		err = a.cmdEdit(rest)
	case "new":
		err = a.cmdNew(rest)
	case "name":
		err = a.cmdName(rest)
	case "list":
		err = a.cmdList(rest)
	case "completion":
		err = a.cmdCompletion(rest)
	case "migrate":
		err = a.cmdMigrate(rest)
	case "delete":
		err = a.cmdDelete(rest)
	case "serve":
		err = a.cmdServe(rest)
	}

	var fe flagError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &fe):
		if errors.Is(fe.err, flag.ErrHelp) {
			return 0
		}
		return 2
	case err == errSilent:
		return 1
	default:
		fmt.Fprintf(a.errw, "thumb: %s\n", sanitize(err.Error()))
		return 1
	}
}

func (a *app) flags(name, synopsis string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.errw)
	fs.Usage = func() {
		fmt.Fprintf(a.errw, "Usage: %s\n", synopsis)
		fs.PrintDefaults()
	}
	return fs
}

// parseFlags lets flags appear before or after positional arguments
// (`thumb alice -n 5` as well as `thumb -n 5 alice`).
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, flagError{err}
		}
		consumed := len(args) - fs.NArg()
		if consumed > 0 && args[consumed-1] == "--" {
			return append(pos, fs.Args()...), nil
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func (a *app) cmdFinger(args []string) error {
	fs := a.flags("finger", "thumb [finger] [-n N] [-l] <handle>...")
	n := fs.Int("n", 1, "number of entries to show (1-100)")
	long := fs.Bool("l", false, "long format: also show DID and PDS")
	users, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		fs.Usage()
		return flagError{errors.New("no handle given")}
	}
	*n = max(1, min(*n, 100))

	failed := false
	for i, u := range users {
		if i > 0 {
			fmt.Fprintln(a.out)
		}
		s, err := render(u, *n, *long)
		if err != nil {
			fmt.Fprintf(a.errw, "finger: %s\n", sanitize(err.Error()))
			failed = true
			continue
		}
		fmt.Fprint(a.out, s)
		rememberThumbed(normalize(u))
	}
	if failed {
		return errSilent
	}
	return nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
