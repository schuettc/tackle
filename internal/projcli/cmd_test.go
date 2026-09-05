package projcli

import (
	"bytes"
	"strings"
	"testing"
)

// captureStdout runs Dispatch(args, ...) and returns everything written to
// its out writer, plus the resulting exit code.
func captureStdout(t *testing.T, args []string) (string, int) {
	t.Helper()
	var out, errw bytes.Buffer
	code := Dispatch(args, &out, &errw)
	return out.String(), code
}

func TestRunListRequiresJSON(t *testing.T) {
	if _, code := captureStdout(t, []string{"list"}); code != 2 {
		t.Fatalf("proj list: got exit %d, want 2", code)
	}
}

func TestRunListJSON(t *testing.T) {
	out, code := captureStdout(t, []string{"list", "--json"})
	if code != 0 {
		t.Fatalf("proj list --json: got exit %d, want 0", code)
	}
	trimmed := strings.TrimSpace(out)
	if !strings.HasPrefix(trimmed, "{") || !strings.Contains(trimmed, `"projects"`) {
		t.Fatalf("proj list --json: output does not look like JSON: %q", out)
	}
}

func TestRunNewRequiresTarget(t *testing.T) {
	if _, code := captureStdout(t, []string{"new"}); code != 2 {
		t.Fatalf("proj new: got exit %d, want 2", code)
	}
}

func TestRunNewMalformedTarget(t *testing.T) {
	// Fails at parseNewTarget (missing '/') before any LoadRoots/tmux call, so
	// this needs no live server. A malformed target is a usage error (exit 2),
	// matching pre-migration behavior.
	if _, code := captureStdout(t, []string{"new", "noslash"}); code != 2 {
		t.Fatalf("proj new noslash: got exit %d, want 2", code)
	}
}

func TestRunListHelp(t *testing.T) {
	out, code := captureStdout(t, []string{"list", "-h"})
	if code != 0 {
		t.Fatalf("proj list -h: got exit %d, want 0", code)
	}
	if !strings.Contains(out, "Usage: proj list") {
		t.Fatalf("proj list -h: output missing usage line: %q", out)
	}
}

func TestRunNewHelp(t *testing.T) {
	out, code := captureStdout(t, []string{"new", "-h"})
	if code != 0 {
		t.Fatalf("proj new -h: got exit %d, want 0", code)
	}
	if !strings.Contains(out, "Usage: proj new") {
		t.Fatalf("proj new -h: output missing usage line: %q", out)
	}
}
