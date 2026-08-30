package proj

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSessionFromHomeSetsLabel(t *testing.T) {
	requireTmux(t)
	sock := "proj-phase1-sess"
	defer Run(sock, "kill-server")
	dir := t.TempDir()
	// tmux canonicalizes pane paths; resolve symlinks (macOS /var → /private/var)
	// so the pane_current_path contract holds cross-platform.
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	if err := EnsureSession(sock, "proj-phase1-sess/w", dir, "none"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	// pane dir is the requested dir...
	if got := Query(sock, "proj-phase1-sess/w", "#{pane_current_path}"); got != dir {
		t.Fatalf("pane dir = %q want %q", got, dir)
	}
	// ...but the SERVER's start dir is $HOME (cwd-poison defense): the session's
	// own start path is $HOME because new-session ran from there.
	if got := Query(sock, "proj-phase1-sess/w", "#{session_path}"); got != os.Getenv("HOME") {
		t.Fatalf("session_path = %q want $HOME", got)
	}
	if got := Query(sock, "proj-phase1-sess/w", "#{@claude_task}"); got != "w" {
		t.Fatalf("@claude_task = %q want w", got)
	}
	// idempotent
	if err := EnsureSession(sock, "proj-phase1-sess/w", dir, "none"); err != nil {
		t.Fatalf("second EnsureSession: %v", err)
	}
}
