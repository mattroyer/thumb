package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Everyone you've thumbed, newest first, in a private file next to your
// login. Each line is a handle, then a tab and when you last read their plan
// (older files have just the handle). Plain text on purpose: easy to read,
// edit or delete, and trivial for shell completion to use. Never stored on
// the AT Protocol, where it would be public.

const maxThumbed = 500

type thumbed struct {
	Handle string
	Seen   time.Time // zero if unknown
}

func thumbedPath() string { return filepath.Join(configDir(), "thumbed") }

func loadThumbed() []thumbed {
	f, err := os.Open(thumbedPath())
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []thumbed
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		h, when, _ := strings.Cut(strings.TrimSpace(sc.Text()), "\t")
		if h = strings.TrimSpace(h); h == "" {
			continue
		}
		t, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(when))
		out = append(out, thumbed{Handle: h, Seen: t})
	}
	return out
}

// rememberThumbed moves handle to the top of the list and stamps it with the
// current time. Failures are ignored: a list that can't be saved shouldn't
// stop anyone from reading a plan.
func rememberThumbed(handle string) {
	if handle == "" || os.Getenv("THUMB_NO_LIST") == "1" {
		return
	}
	list := []thumbed{{Handle: handle, Seen: time.Now().UTC()}}
	for _, t := range loadThumbed() {
		if t.Handle != handle && len(list) < maxThumbed {
			list = append(list, t)
		}
	}
	var b strings.Builder
	for _, t := range list {
		b.WriteString(t.Handle)
		if !t.Seen.IsZero() {
			b.WriteString("\t" + t.Seen.UTC().Format(time.RFC3339Nano))
		}
		b.WriteString("\n")
	}
	if os.MkdirAll(configDir(), 0o700) != nil {
		return
	}
	tmp := thumbedPath() + ".tmp"
	if os.WriteFile(tmp, []byte(b.String()), 0o600) == nil {
		os.Rename(tmp, thumbedPath())
	}
}

func (a *app) cmdList(args []string) error {
	fs := a.flags("list", "thumb list [--clear]")
	clear := fs.Bool("clear", false, "forget everyone on the list")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	if *clear {
		if err := os.Remove(thumbedPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Fprintln(a.out, "List cleared.")
		return nil
	}
	for _, t := range loadThumbed() {
		fmt.Fprintln(a.out, t.Handle)
	}
	return nil
}

func (a *app) cmdCompletion(args []string) error {
	if len(args) != 1 || completions[args[0]] == "" {
		return flagError{errors.New("usage: thumb completion bash|zsh|fish")}
	}
	fmt.Fprint(a.out, completions[args[0]])
	return nil
}

// Shell completion: commands for the first word, and handles from
// `thumb list` everywhere a handle fits. After -f it completes files.
const completionCommands = "finger login logout whoami post edit delete new name migrate serve list completion version help"

var completions = map[string]string{
	"bash": `# thumb completion for bash. Add to ~/.bashrc:  eval "$(thumb completion bash)"
_thumb() {
  local cur=${COMP_WORDS[COMP_CWORD]} prev=${COMP_WORDS[COMP_CWORD-1]} words
  [ "$prev" = -f ] && return  # fall back to file names
  words=$(thumb list 2>/dev/null)
  [ "$COMP_CWORD" -eq 1 ] && words="$words ` + completionCommands + `"
  COMPREPLY=($(compgen -W "$words" -- "$cur"))
}
complete -o default -F _thumb thumb
`,
	"zsh": `#compdef thumb
# thumb completion for zsh. Add to ~/.zshrc:  source <(thumb completion zsh)
(( $+functions[compdef] )) || { autoload -U compinit && compinit; }
_thumb() {
  if [[ ${words[CURRENT-1]} == -f ]]; then _files; return; fi
  local -a candidates
  candidates=(${(f)"$(thumb list 2>/dev/null)"})
  (( CURRENT == 2 )) && candidates+=(` + completionCommands + `)
  compadd -a candidates
}
compdef _thumb thumb
`,
	"fish": `# thumb completion for fish. Save as ~/.config/fish/completions/thumb.fish:
#   thumb completion fish > ~/.config/fish/completions/thumb.fish
complete -c thumb -f
complete -c thumb -n __fish_use_subcommand -a "` + completionCommands + `"
complete -c thumb -a "(thumb list 2>/dev/null)"
complete -c thumb -s f -r -F
`,
}
