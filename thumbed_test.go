package main

import (
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestThumbedList(t *testing.T) {
	setup(t)
	if _, out, _ := run(t, "list"); out != "" {
		t.Fatalf("new list should be empty, got %q", out)
	}

	run(t, "test.example")
	run(t, "@Other.Example") // normalized before saving
	run(t, "test.example")   // moves back to the top, no duplicate
	run(t, "nobody.example") // failed lookups aren't remembered

	if _, out, _ := run(t, "list"); out != "test.example\nother.example\n" {
		t.Fatalf("list = %q", out)
	}
	if fi, err := os.Stat(thumbedPath()); err != nil || (fi.Mode().Perm() != 0o600 && os.PathSeparator == '/') {
		t.Fatalf("list file: %v %v", fi, err)
	}

	t.Setenv("THUMB_NO_LIST", "1")
	run(t, "third.example")
	if _, out, _ := run(t, "list"); strings.Contains(out, "third") {
		t.Fatalf("THUMB_NO_LIST was ignored: %q", out)
	}

	if _, out, _ := run(t, "list", "--clear"); !strings.Contains(out, "cleared") {
		t.Fatalf("clear: %q", out)
	}
	if _, out, _ := run(t, "list"); out != "" {
		t.Fatalf("list after clear = %q", out)
	}
}

// Lookups made by strangers through the finger gateway aren't yours to remember.
func TestGatewayDoesNotRemember(t *testing.T) {
	setup(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go serveFinger(ln, 1)
	defer ln.Close()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(c, "test.example\r\n")
	io.ReadAll(c)
	c.Close()
	if got := loadThumbed(); len(got) != 0 {
		t.Fatalf("gateway lookup was remembered: %v", got)
	}
}

func TestCompletionScripts(t *testing.T) {
	for _, sh := range []string{"bash", "zsh", "fish"} {
		code, out, _ := run(t, "completion", sh)
		if code != 0 || !strings.Contains(out, "thumb list") {
			t.Errorf("completion %s: code=%d", sh, code)
		}
	}
	if code, _, _ := run(t, "completion", "powershell"); code != 2 {
		t.Errorf("unknown shell should exit 2, got %d", code)
	}
}

// Run each completion script in its real shell, with a stand-in `thumb`
// whose list holds two handles.
func TestCompletionInRealShells(t *testing.T) {
	fake := `thumb() { printf 'alice.example\nbob.example\n'; }`
	cases := []struct {
		shell, script string
		want, notWant []string
	}{
		{"bash", fake + "\n" + completions["bash"] + `
COMP_WORDS=(thumb al); COMP_CWORD=1; _thumb; echo "1:${COMPREPLY[*]}"
COMP_WORDS=(thumb po); COMP_CWORD=1; _thumb; echo "2:${COMPREPLY[*]}"
COMP_WORDS=(thumb -n 5 b); COMP_CWORD=3; _thumb; echo "3:${COMPREPLY[*]}"
COMP_WORDS=(thumb post -f ''); COMP_CWORD=3; COMPREPLY=(); _thumb; echo "4:${COMPREPLY[*]}"`,
			[]string{"1:alice.example", "2:post", "3:bob.example", "4:\n"}, []string{"3:bob.example post"}},
		{"zsh", "autoload -U compinit && compinit -u\n" + fake + "\n" + completions["zsh"] + `
compadd() { print -l -- ${(P)2} }
_files() { print FILES; }
words=(thumb al); CURRENT=2; print "1:$(_thumb | tr '\n' ' ')"
words=(thumb -n 5 b); CURRENT=4; print "2:$(_thumb | tr '\n' ' ')"
words=(thumb post -f x); CURRENT=4; print "3:$(_thumb | tr '\n' ' ')"`,
			[]string{"1:alice.example bob.example finger", "2:alice.example bob.example", "3:FILES"},
			[]string{"2:finger"}},
		{"fish", "function thumb; printf 'alice.example\\nbob.example\\n'; end\n" + completions["fish"] + `
complete -C "thumb al"
complete -C "thumb po"`,
			[]string{"alice.example", "post"}, nil},
	}
	for _, c := range cases {
		if _, err := exec.LookPath(c.shell); err != nil {
			t.Logf("skipping %s: not installed", c.shell)
			continue
		}
		out, err := exec.Command(c.shell, "-c", c.script).CombinedOutput()
		if err != nil {
			t.Errorf("%s: %v\n%s", c.shell, err, out)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(string(out)+"\n", w) {
				t.Errorf("%s output missing %q:\n%s", c.shell, w, out)
			}
		}
		for _, w := range c.notWant {
			if strings.Contains(string(out), w) {
				t.Errorf("%s output should not contain %q:\n%s", c.shell, w, out)
			}
		}
	}
}
