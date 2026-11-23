package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// planPath picks the local plan file: the -f flag, then $THUMB_PLAN, then
// ~/.plan (where finger has always looked). A leading ~ is expanded.
func planPath(flagValue string) (string, error) {
	p := flagValue
	if p == "" {
		p = os.Getenv("THUMB_PLAN")
	}
	home, err := os.UserHomeDir()
	if p == "" {
		if err != nil {
			return "", fmt.Errorf("can't find your home directory; use -f or THUMB_PLAN: %v", err)
		}
		return filepath.Join(home, ".plan"), nil
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, p[1:])
	}
	return filepath.Abs(p)
}

func cleanText(s string) string {
	return strings.Trim(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// cmdEdit opens the plan file in $EDITOR and publishes it if you changed it.
// If the file doesn't exist yet, it starts from your latest published entry.
func (a *app) cmdEdit(args []string) error {
	fs := a.flags("edit", "thumb edit [-f planfile] [--amend]")
	file := fs.String("f", "", "plan file to edit (default $THUMB_PLAN or ~/.plan)")
	amend := fs.Bool("amend", false, "replace your latest entry instead of adding one")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	s, err := loadSession()
	if err != nil {
		return err
	}
	path, err := planPath(*file)
	if err != nil {
		return err
	}

	before, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		seed := ""
		if prev, err := latestOwn(s); err == nil && prev != nil {
			seed = prev.Value.Text + "\n"
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
			return err
		}
		before = []byte(seed)
		fmt.Fprintf(a.errw, "Created %s\n", path)
	} else if err != nil {
		return err
	}

	if err := openEditor(path); err != nil {
		return err
	}
	after, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if cleanText(string(after)) == cleanText(string(before)) {
		fmt.Fprintln(a.out, "No changes; nothing published.")
		return nil
	}
	return a.publish(s, string(after), *amend)
}

func openEditor(path string) error {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
		if runtime.GOOS == "windows" {
			editor = "notepad"
		}
	}
	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor %q: %v", editor, err)
	}
	return nil
}

// maxPlanBytes matches maxLength in lexicon/date.thumbplan.plan.json.
const maxPlanBytes = 100000

// publish adds text as a new plan entry, or replaces the latest one if amend
// is set. It skips posting a duplicate of your latest entry.
func (a *app) publish(s *session, text string, amend bool) error {
	text = cleanText(text)
	if strings.TrimSpace(text) == "" {
		fmt.Fprintln(a.out, "Empty plan; nothing published.")
		return nil
	}
	if len(text) > maxPlanBytes {
		return fmt.Errorf("that plan is %d bytes; the limit is %d. Plans are meant to be short", len(text), maxPlanBytes)
	}
	prev, err := latestOwn(s)
	if err != nil {
		return err
	}
	if prev != nil && cleanText(prev.Value.Text) == text {
		fmt.Fprintln(a.out, "Same as your latest entry; nothing published.")
		return nil
	}
	if amend && prev == nil {
		return errors.New("nothing to amend; you have no plan entries yet")
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	rec := planValue{Type: collection, Text: text, CreatedAt: now}
	var r struct {
		URI string `json:"uri"`
	}
	if amend {
		if prev.Value.CreatedAt != "" {
			rec.CreatedAt = prev.Value.CreatedAt
		}
		rec.UpdatedAt = now
		err = xrpcAuthed(s, "POST", "com.atproto.repo.putRecord", nil, map[string]any{
			"repo": s.DID, "collection": collection, "rkey": prev.rkey(),
			"record": rec, "validate": false, "swapRecord": prev.CID,
		}, &r)
		if err == nil {
			fmt.Fprintf(a.out, "Amended %s\n", r.URI)
		}
		return err
	}
	err = xrpcAuthed(s, "POST", "com.atproto.repo.createRecord", nil, map[string]any{
		"repo": s.DID, "collection": collection, "record": rec, "validate": false,
	}, &r)
	if err == nil {
		fmt.Fprintf(a.out, "Published %s\n", r.URI)
	}
	return err
}
