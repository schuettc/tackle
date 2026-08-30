package proj

import (
	"os/exec"
	"testing"
)

func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
}

func TestServersAndFind(t *testing.T) {
	requireTmux(t)
	sock := "proj-phase1-test"
	// create a detached session on the test socket, from a temp dir
	if _, err := Run(sock, "new-session", "-d", "-s", "proj-phase1-test/w", "-c", t.TempDir()); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	defer Run(sock, "kill-server")

	found := false
	for _, s := range Servers() {
		if s == sock {
			found = true
		}
	}
	if !found {
		t.Fatalf("Servers() missing %s: %v", sock, Servers())
	}

	if srv, ok := FindServer("proj-phase1-test/w"); !ok || srv != sock {
		t.Fatalf("FindServer = %q,%v", srv, ok)
	}
}
