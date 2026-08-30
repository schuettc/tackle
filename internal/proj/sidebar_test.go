package proj

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDirFallback(t *testing.T) {
	real := t.TempDir()
	if got := resolveDir("", "", real); got != real {
		t.Fatalf("existing dir: %q", got)
	}
	// a non-existent child walks up to the surviving ancestor
	dead := filepath.Join(real, "gone", "deeper")
	if got := resolveDir("", "", dead); got != real {
		t.Fatalf("walk-up: %q want %q", got, real)
	}
	// empty/garbage → $HOME (no socket to query); "/" must NEVER be returned
	got := resolveDir("", "", "/definitely/not/here/xyz")
	if got == "" {
		t.Fatal("must never return empty")
	}
	if got == "/" {
		t.Fatalf("resolveDir must not return /; got %q", got)
	}
}

func TestAppCommand(t *testing.T) {
	c := appCommand("shell", "/tmp/x")
	if !strings.Contains(c, "cd ") || !strings.Contains(c, "exec ") {
		t.Fatalf("shell cmd: %q", c)
	}
	if _, err := exec.LookPath("yazi"); err == nil {
		if !strings.Contains(appCommand("yazi", "/tmp/x"), "yazi") {
			t.Fatal("yazi passthrough: yazi on PATH but not in command")
		}
	} else {
		// yazi not on PATH: appCommand degrades to a shell — still must exec something
		if !strings.Contains(appCommand("yazi", "/tmp/x"), "exec ") {
			t.Fatal("yazi degraded: expected exec shell fallback")
		}
	}
}

func TestBuildSidebarTagsPanes(t *testing.T) {
	requireTmux(t) // from Phase 1
	sock := "proj-p3-test"
	defer Run(sock, "kill-server")
	dir := t.TempDir()
	if err := EnsureSession(sock, "proj-p3-test/w", dir, "none"); err != nil {
		t.Fatal(err)
	}
	BuildSidebar(sock, "proj-p3-test/w", dir, Layout{Panes: []string{"scratch", "shell", "shell"}})
	// 3 sidebar panes built (use shells to avoid needing scratch/yazi installed in CI)
	out, _ := Run(sock, "list-panes", "-t", "proj-p3-test/w", "-F", "#{@sidebar}")
	n := 0
	for _, l := range splitLines(out) {
		if l == "1" {
			n++
		}
	}
	if n < 3 {
		t.Fatalf("want ≥3 @sidebar panes, got %d\n%s", n, out)
	}
}
