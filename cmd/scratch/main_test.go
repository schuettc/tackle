package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/notes"
)

// isolate points the pad store at a temp directory and clears the ambient
// agent session, so these tests describe scratch's behavior rather than
// whichever Claude/Codex session happens to be running them. Without this,
// the suite inherits the developer's own $CLAUDE_CODE_SESSION_ID and the
// assertions move with it.
func isolate(t *testing.T) string {
	t.Helper()
	store := t.TempDir()
	t.Setenv(notes.EnvDir, store)
	t.Setenv(notes.EnvFile, "")
	t.Setenv(notes.EnvClaudeSession, "")
	t.Setenv("TMUX", "")
	return store
}

func TestRunPath(t *testing.T) {
	store := isolate(t)
	var out bytes.Buffer
	if code := run(t.TempDir(), []string{"path"}, &out); code != 0 {
		t.Fatalf("run path exit = %d, want 0", code)
	}
	got := strings.TrimSpace(out.String())
	if !strings.HasPrefix(got, filepath.Join(store, "pads")) {
		t.Fatalf("run path = %q, want it under %q", got, filepath.Join(store, "pads"))
	}
}

// The pad is addressed by session, so `path` is the way any caller finds
// the file the TUI is editing — print and append must agree with it.
func TestRunPrint(t *testing.T) {
	isolate(t)
	t.Setenv(notes.EnvClaudeSession, "print-sess")
	dir := t.TempDir()

	var pathOut bytes.Buffer
	run(dir, []string{"path"}, &pathOut)
	pad := strings.TrimSpace(pathOut.String())
	if err := os.MkdirAll(filepath.Dir(pad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pad, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if code := run(dir, []string{"print"}, &out); code != 0 {
		t.Fatalf("run print exit = %d, want 0", code)
	}
	if out.String() != "hello\n" {
		t.Fatalf("run print = %q, want %q", out.String(), "hello\n")
	}
}

func TestRunAppend(t *testing.T) {
	isolate(t)
	t.Setenv(notes.EnvClaudeSession, "append-sess")
	dir := t.TempDir()

	var out bytes.Buffer
	if code := run(dir, []string{"append", "a line"}, &out); code != 0 {
		t.Fatalf("run append exit = %d, want 0", code)
	}

	var pathOut bytes.Buffer
	run(dir, []string{"path"}, &pathOut)
	got, err := os.ReadFile(strings.TrimSpace(pathOut.String()))
	if err != nil {
		t.Fatalf("reading the resolved pad: %v", err)
	}
	if string(got) != "a line\n" {
		t.Fatalf("after append = %q, want %q", got, "a line\n")
	}
}

// Append must create the store on first use — nothing pre-makes it.
func TestRunAppendCreatesStore(t *testing.T) {
	store := isolate(t)
	t.Setenv(notes.EnvDir, filepath.Join(store, "not", "yet", "there"))
	t.Setenv(notes.EnvClaudeSession, "fresh-sess")

	var out bytes.Buffer
	if code := run(t.TempDir(), []string{"append", "first"}, &out); code != 0 {
		t.Fatalf("run append exit = %d, want 0 (stderr may explain)", code)
	}
}

// Nothing is written into the working directory any more.
func TestRunAppendLeavesCwdClean(t *testing.T) {
	isolate(t)
	t.Setenv(notes.EnvClaudeSession, "clean-sess")
	dir := t.TempDir()

	var out bytes.Buffer
	run(dir, []string{"append", "a line"}, &out)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("working dir got %d entries, want none: %v", len(entries), entries)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	isolate(t)
	var out bytes.Buffer
	if code := run(t.TempDir(), []string{"bogus"}, &out); code != 2 {
		t.Fatalf("run bogus exit = %d, want 2", code)
	}
}
