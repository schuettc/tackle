package proj

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testSession creates a detached session on a throwaway socket (from a temp
// dir, with no agent) and kills the server when the test ends.
func testSession(t *testing.T, sock, name string) string {
	t.Helper()
	requireTmux(t)
	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	t.Cleanup(func() { _, _ = Run(sock, "kill-server") })
	if _, err := EnsureSession(sock, name, dir, "none", "", nil); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	return dir
}

func TestLiveEntriesDerivesAgent(t *testing.T) {
	sock, name := "proj-restoretest-live", "restoretest-live/w"
	dir := testSession(t, sock, name)
	tr := transcriptTree(t)

	if _, err := Run(sock, "set-option", "-t", name, "@harness_session", "78834978"); err != nil {
		t.Fatal(err)
	}
	got := LiveEntries(sock, tr)
	if len(got) != 1 {
		t.Fatalf("entries = %+v want 1", got)
	}
	want := LiveEntry{Name: name, Entry: Entry{Socket: sock, Project: "restoretest-live", Dir: dir, Agent: "claude", Conversation: "78834978"}}
	if got[0] != want {
		t.Fatalf("entry = %+v\nwant    %+v", got[0], want)
	}

	// An id in neither store falls back to what proj recorded launching.
	_, _ = Run(sock, "set-option", "-t", name, "@harness_session", "unknown-id")
	_, _ = Run(sock, "set-option", "-t", name, AgentOption(), "pi")
	if got := LiveEntries(sock, tr); len(got) != 1 || got[0].Agent != "pi" || got[0].Conversation != "unknown-id" {
		t.Fatalf("fallback entry = %+v", got)
	}
}

func TestSnapshotEmptyServerKeepsEntries(t *testing.T) {
	recordHome(t)
	if err := UpdateRecord(func(r *Record) bool { r.Sessions["x/w"] = Entry{Socket: "proj-x"}; return true }); err != nil {
		t.Fatal(err)
	}
	if err := Snapshot("proj-restoretest-none"); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	rec, _ := LoadRecord()
	if _, ok := rec.Sessions["x/w"]; !ok {
		t.Fatal("snapshot of a dead server deleted an entry")
	}
}

func TestHookCommand(t *testing.T) {
	want := `run-shell -b "'/a b/proj' __snapshot --socket 'proj-x'"`
	if got := HookCommand("/a b/proj", "proj-x"); got != want {
		t.Fatalf("HookCommand = %s\nwant          %s", got, want)
	}
}

func TestInstallHooksIdempotent(t *testing.T) {
	sock := "proj-restoretest-hooks"
	testSession(t, sock, "restoretest-hooks/w")
	if _, err := Run(sock, "set-hook", "-g", "session-created", "run-shell 'true'"); err != nil {
		t.Fatal(err)
	}
	installHooksExe(sock, "/bin/proj")
	installHooksExe(sock, "/bin/proj")
	out, _ := Run(sock, "show-hooks", "-g")
	if n := strings.Count(out, "session-created[80]"); n != 1 {
		t.Fatalf("session-created[80] appears %d times:\n%s", n, out)
	}
	if !strings.Contains(out, "session-created[0] run-shell true") {
		t.Fatalf("operator hook at [0] lost:\n%s", out)
	}
	for _, ev := range hookEvents {
		if !strings.Contains(out, ev+"[80] ") {
			t.Fatalf("missing %s[80]:\n%s", ev, out)
		}
	}
}

func TestInstallHooksSkipsNonProjBinary(t *testing.T) {
	// Under `go test` os.Executable is the test binary; a hook pointing at it
	// would re-run the whole suite every time an option is set.
	sock := "proj-restoretest-skip"
	testSession(t, sock, "restoretest-skip/w")
	InstallHooks(sock)
	if out, _ := Run(sock, "show-hooks", "-g"); strings.Contains(out, "[80]") {
		t.Fatalf("InstallHooks installed hooks from a non-proj binary:\n%s", out)
	}
}

func TestInstallHooksFireSnapshot(t *testing.T) {
	requireTmux(t)
	recordHome(t) // set BEFORE the server starts: hooks run in its environment
	tr := transcriptTree(t)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", tr.PiRoot)
	t.Setenv("CLAUDE_CONFIG_DIR", tr.ClaudeRoot)
	exe := filepath.Join(t.TempDir(), "proj")
	if out, err := exec.Command("go", "build", "-o", exe, "../../cmd/proj").CombinedOutput(); err != nil {
		t.Fatalf("build proj: %v\n%s", err, out)
	}

	sock, name := "proj-restoretest-fire", "restoretest-fire/w"
	testSession(t, sock, name)
	installHooksExe(sock, exe)
	if _, err := Run(sock, "set-option", "-t", name, "@harness_session", "01a0ed7a"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec, _ := LoadRecord()
		if e := rec.Sessions[name]; e.Conversation == "01a0ed7a" {
			if e.Agent != "pi" {
				t.Fatalf("agent = %q want pi", e.Agent)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	rec, _ := LoadRecord()
	t.Fatalf("hook never recorded the conversation; record = %+v", rec.Sessions)
}
