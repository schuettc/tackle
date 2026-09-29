package projcli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/proj"
)

// startSandboxServer starts a throwaway tmux server whose panes run /bin/sh
// with PATH limited to stubDir plus the system dirs. The operator's login
// shell could put the real pi ahead of the stub on PATH; this keeps a
// restored pane from ever launching a real agent. -f /dev/null skips
// ~/.tmux.conf (plugins, continuum) on a test server.
func startSandboxServer(t *testing.T, sock, stubDir string) {
	t.Helper()
	run := func(args ...string) {
		if out, err := exec.Command("tmux", append([]string{"-L", sock}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v\n%s", args, err, out)
		}
	}
	run("-f", "/dev/null", "new-session", "-d", "-s", "_boot")
	run("set-option", "-g", "default-command", "/bin/sh")
	run("set-environment", "-g", "PATH", stubDir+":/usr/bin:/bin")
}

func TestRestoreEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	sock, name := "proj-restoretest-e2e", "restoretest-e2e/w"
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", sock, "kill-server").Run() })

	// Isolated record and transcript store holding the conversation.
	t.Setenv("PROJ_HOME", t.TempDir())
	root := t.TempDir()
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", filepath.Join(root, "pi"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	tp := filepath.Join(root, "pi", "--x--", "2026-09-29T00-00-00-000Z_idE2E.jsonl")
	if err := os.MkdirAll(filepath.Dir(tp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tp, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A stub pi that shows the argv it was resumed with.
	stubDir := t.TempDir()
	stub := "#!/bin/sh\necho \"STUB PI $*\"\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(stubDir, "pi"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}

	// 1. A live session whose agent announced its conversation is recorded.
	startSandboxServer(t, sock, stubDir)
	if _, err := proj.EnsureSession(sock, name, dir, "none", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := proj.Run(sock, "set-option", "-t", name, "@harness_session", "idE2E"); err != nil {
		t.Fatal(err)
	}
	if err := proj.Snapshot(sock); err != nil {
		t.Fatal(err)
	}

	// 2. The server dies (a reboot); the record keeps the session.
	_ = exec.Command("tmux", "-L", sock, "kill-server").Run()
	rec, err := proj.LoadRecord()
	if err != nil {
		t.Fatal(err)
	}
	if e := rec.Sessions[name]; e.Conversation != "idE2E" || e.Agent != "pi" || e.Dir != dir {
		t.Fatalf("record after kill-server = %+v", rec.Sessions)
	}

	// 3. After the "reboot", restore brings it back resuming that conversation.
	startSandboxServer(t, sock, stubDir)
	origS, origG := spawnSidebar, ghosttyOpen
	t.Cleanup(func() { spawnSidebar, ghosttyOpen = origS, origG })
	spawnSidebar = func(socket, session, dir string) {} // would spawn the test binary
	ghosttyOpen = func([][]proj.SavedSession, io.Writer) error { return nil }

	var out bytes.Buffer
	if code := runRestore([]string{name}, "", &out); code != 0 {
		t.Fatalf("runRestore exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "restored "+name+" (pi, resumed)") {
		t.Fatalf("output = %q", out.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	var pane string
	for time.Now().Before(deadline) {
		pane, _ = proj.Run(sock, "capture-pane", "-p", "-t", "="+name+":")
		if strings.Contains(pane, "STUB PI --session idE2E") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("restored pane never ran pi --session idE2E; pane:\n%s", pane)
}
