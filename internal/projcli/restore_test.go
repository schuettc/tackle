package projcli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/proj"
)

type restoreCall struct{ socket, name, dir, agent, conv string }

type fakeRestore struct {
	restored []restoreCall
	opened   [][]proj.SavedSession
	jumped   []string
}

// withFakeRestore records a sessions.json under a temp PROJ_HOME, points the
// transcript roots at a temp tree holding a pi transcript for "id1", and
// replaces every side-effecting seam of runRestore.
func withFakeRestore(t *testing.T, sessions map[string]proj.Entry, fail map[string]error, exists map[string]bool) *fakeRestore {
	t.Helper()
	t.Setenv("PROJ_HOME", t.TempDir())
	root := t.TempDir()
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", filepath.Join(root, "pi"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	p := filepath.Join(root, "pi", "--x--", "2026_id1.jsonl")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte("{}"), 0o644)
	if err := proj.UpdateRecord(func(r *proj.Record) bool {
		for k, v := range sessions {
			r.Sessions[k] = v
		}
		return true
	}); err != nil {
		t.Fatal(err)
	}

	f := &fakeRestore{}
	origR, origS, origG, origJ, origL := restoreSession, spawnSidebar, ghosttyOpen, gotoSession, liveState
	t.Cleanup(func() {
		restoreSession, spawnSidebar, ghosttyOpen, gotoSession, liveState = origR, origS, origG, origJ, origL
	})
	restoreSession = func(socket, name, dir, agent, conv string) (bool, error) {
		f.restored = append(f.restored, restoreCall{socket, name, dir, agent, conv})
		if err := fail[name]; err != nil {
			return false, err
		}
		return !exists[name], nil
	}
	spawnSidebar = func(socket, session, dir string) {}
	ghosttyOpen = func(groups [][]proj.SavedSession, out io.Writer) error {
		f.opened = groups
		return nil
	}
	gotoSession = func(socket, name string) error {
		f.jumped = append(f.jumped, socket+" "+name)
		return nil
	}
	liveState = func() proj.LiveState { return proj.LiveState{} }
	return f
}

func TestRunRestoreLines(t *testing.T) {
	dir := t.TempDir()
	f := withFakeRestore(t, map[string]proj.Entry{
		"a/1": {Socket: "proj-a", Project: "a", Dir: dir, Agent: "pi", Conversation: "id1"},
		"b/1": {Socket: "proj-b", Project: "b", Dir: dir, Agent: "claude"},
		"c/1": {Socket: "proj-c", Project: "c", Dir: dir, Agent: "pi", Conversation: "id1"},
		"d/1": {Socket: "proj-d", Project: "d", Dir: dir, Agent: "pi", Conversation: "gone"},
	}, map[string]error{"c/1": errors.New("boom")}, nil)

	var out bytes.Buffer
	code := runRestore([]string{"a/1", "b/1", "c/1", "d/1"}, "", &out)
	for _, want := range []string{
		"restored a/1 (pi, resumed)",
		"restored b/1 (claude, fresh)",
		"failed c/1: boom",
		"restored d/1 (pi, fresh)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
	if code != 1 {
		t.Fatalf("exit %d want 1 (one failure)", code)
	}
	// A missing transcript restores fresh: no id reaches RestoreSession.
	for _, c := range f.restored {
		if c.name == "d/1" && c.conv != "" {
			t.Fatalf("d/1 resumed a conversation with no transcript: %+v", c)
		}
	}
	if len(f.opened) == 0 {
		t.Fatal("Ghostty windows were not opened")
	}
}

func TestRunRestoreExists(t *testing.T) {
	withFakeRestore(t, map[string]proj.Entry{
		"a/1": {Socket: "proj-a", Project: "a", Dir: t.TempDir(), Agent: "pi", Conversation: "id1"},
	}, nil, map[string]bool{"a/1": true})
	var out bytes.Buffer
	if code := runRestore([]string{"a/1"}, "", &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "exists a/1") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunRestoreJumpSkipsGhostty(t *testing.T) {
	f := withFakeRestore(t, map[string]proj.Entry{
		"a/1": {Socket: "proj-a", Project: "a", Dir: t.TempDir(), Agent: "pi", Conversation: "id1"},
	}, nil, nil)
	var out bytes.Buffer
	if code := runRestore([]string{"a/1"}, "a/1", &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if f.opened != nil {
		t.Fatal("single restore opened Ghostty windows")
	}
	if len(f.jumped) != 1 || f.jumped[0] != "proj-a a/1" {
		t.Fatalf("jumped = %v", f.jumped)
	}
}

func TestRunRestoreMissingDir(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "deleted-worktree")
	f := withFakeRestore(t, map[string]proj.Entry{
		"a/1": {Socket: "proj-a", Project: "a", Dir: gone, Agent: "pi", Conversation: "id1"},
	}, nil, nil)
	var out bytes.Buffer
	runRestore([]string{"a/1"}, "", &out)
	if len(f.restored) != 1 || f.restored[0].dir == gone || f.restored[0].dir == "" {
		t.Fatalf("restore got dir %+v; want a surviving fallback, not %s", f.restored, gone)
	}
}
