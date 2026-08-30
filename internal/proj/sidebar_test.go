package proj

import (
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
	// empty/garbage → $HOME (no socket to query)
	if got := resolveDir("", "", "/definitely/not/here/xyz"); got == "" {
		t.Fatal("must never return empty")
	}
}

func TestAppCommand(t *testing.T) {
	c := appCommand("shell", "/tmp/x")
	if !strings.Contains(c, "cd ") || !strings.Contains(c, "exec ") {
		t.Fatalf("shell cmd: %q", c)
	}
	if !strings.Contains(appCommand("yazi", "/tmp/x"), "yazi") {
		t.Fatal("yazi passthrough")
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
