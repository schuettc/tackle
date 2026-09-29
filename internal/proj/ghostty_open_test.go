package proj

import (
	"bytes"
	"strings"
	"testing"
)

type scriptCall struct {
	script string
	args   []string
}

// fakeGhostty records every osascript call and answers `new window` with a
// window id, and stubs the attach check.
func fakeGhostty(t *testing.T, attached bool) *[]scriptCall {
	t.Helper()
	var calls []scriptCall
	origScript, origClient, origWait, origTmux := osascript, hasClient, attachPoll, tmuxBin
	t.Cleanup(func() { osascript, hasClient, attachPoll, tmuxBin = origScript, origClient, origWait, origTmux })
	tmuxBin = func() string { return "/opt/homebrew/bin/tmux" }
	attachPoll = 0
	hasClient = func(socket, name string) bool { return attached }
	osascript = func(script string, args ...string) (string, error) {
		calls = append(calls, scriptCall{script, args})
		if strings.Contains(script, "new window") {
			return "win-1\n", nil
		}
		return "", nil
	}
	return &calls
}

func TestGhosttyOpenScript(t *testing.T) {
	calls := fakeGhostty(t, true)
	a := SavedSession{Name: "a/1", Entry: Entry{Socket: "proj-a"}}
	b := SavedSession{Name: "a/2", Entry: Entry{Socket: "proj-a"}}
	c := SavedSession{Name: "c/1", Entry: Entry{Socket: "proj-c"}}
	var out bytes.Buffer
	if err := GhosttyOpen([][]SavedSession{{a, b}, {c}}, &out); err != nil {
		t.Fatal(err)
	}
	got := *calls
	if len(got) != 3 {
		t.Fatalf("calls = %d want 3: %+v", len(got), got)
	}
	if !strings.Contains(got[0].script, "new window") || !strings.Contains(got[1].script, "new tab in") || !strings.Contains(got[2].script, "new window") {
		t.Fatalf("scripts in wrong order: %+v", got)
	}
	// Ghostty runs the command in a bare `bash --noprofile --norc`, so tmux
	// must be named by absolute path: Homebrew's bin is not on that PATH.
	if got[0].args[0] != "'/opt/homebrew/bin/tmux' -L 'proj-a' attach -t '=a/1'" {
		t.Fatalf("attach command = %q", got[0].args[0])
	}
	if got[1].args[1] != "win-1" {
		t.Fatalf("tab not placed in the new window: args %q", got[1].args)
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected output %q", out.String())
	}
}

func TestGhosttyOpenReportsNoAttach(t *testing.T) {
	calls := fakeGhostty(t, false)
	a := SavedSession{Name: "a/1", Entry: Entry{Socket: "proj-a"}}
	b := SavedSession{Name: "b/1", Entry: Entry{Socket: "proj-b"}}
	var out bytes.Buffer
	if err := GhosttyOpen([][]SavedSession{{a}, {b}}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "WARN a/1 did not attach") || !strings.Contains(out.String(), "WARN b/1 did not attach") {
		t.Fatalf("output = %q", out.String())
	}
	if len(*calls) != 2 {
		t.Fatalf("stopped after a failed attach: %d calls", len(*calls))
	}
}

func TestTmuxBinIsAbsolute(t *testing.T) {
	requireTmux(t)
	if p := tmuxBin(); !strings.HasPrefix(p, "/") {
		t.Fatalf("tmuxBin() = %q; want an absolute path", p)
	}
}
