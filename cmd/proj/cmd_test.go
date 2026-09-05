package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything written to it, plus fn's return value. run() writes to
// os.Stdout directly (via app.Dispatch), so tests that need to inspect
// output must capture at this level rather than pass in a buffer.
func captureStdout(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	code := fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out), code
}

func TestRunListRequiresJSON(t *testing.T) {
	if code := run([]string{"list"}); code != 2 {
		t.Fatalf("proj list: got exit %d, want 2", code)
	}
}

func TestRunListJSON(t *testing.T) {
	out, code := captureStdout(t, func() int { return run([]string{"list", "--json"}) })
	if code != 0 {
		t.Fatalf("proj list --json: got exit %d, want 0", code)
	}
	trimmed := strings.TrimSpace(out)
	if !strings.HasPrefix(trimmed, "{") || !strings.Contains(trimmed, `"projects"`) {
		t.Fatalf("proj list --json: output does not look like JSON: %q", out)
	}
}

func TestRunNewRequiresTarget(t *testing.T) {
	if code := run([]string{"new"}); code != 2 {
		t.Fatalf("proj new: got exit %d, want 2", code)
	}
}

func TestRunNewMalformedTarget(t *testing.T) {
	// Fails at parseNewTarget (missing '/') before any LoadRoots/tmux call, so
	// this needs no live server. A malformed target is a usage error (exit 2),
	// matching pre-migration behavior.
	if code := run([]string{"new", "noslash"}); code != 2 {
		t.Fatalf("proj new noslash: got exit %d, want 2", code)
	}
}

func TestRunListHelp(t *testing.T) {
	out, code := captureStdout(t, func() int { return run([]string{"list", "-h"}) })
	if code != 0 {
		t.Fatalf("proj list -h: got exit %d, want 0", code)
	}
	if !strings.Contains(out, "Usage: proj list") {
		t.Fatalf("proj list -h: output missing usage line: %q", out)
	}
}

func TestRunNewHelp(t *testing.T) {
	out, code := captureStdout(t, func() int { return run([]string{"new", "-h"}) })
	if code != 0 {
		t.Fatalf("proj new -h: got exit %d, want 0", code)
	}
	if !strings.Contains(out, "Usage: proj new") {
		t.Fatalf("proj new -h: output missing usage line: %q", out)
	}
}
