package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schuettc/tackle/internal/sift/discover"
)

func TestDeadPath(t *testing.T) {
	root := t.TempDir()
	hit := inRepo(fixture(t, "dead-path", "hit.md", discover.ClassRepo), root, "CLAUDE.md", "cmd/tool/main.go")
	rows := only(t, "dead-path", input(hit))
	if got := lines(rows); len(got) != 3 || got[0] != 3 || got[1] != 4 || got[2] != 5 {
		t.Fatalf("lines %v: %+v", got, rows)
	}
	if evidence(rows[0], "missing") != "internal/tool/" || evidence(rows[1], "missing") != "docs/CHANGES.md" {
		t.Errorf("evidence %+v %+v", rows[0].Evidence, rows[1].Evidence)
	}
	// Gone at the audited commit and not on disk either: certain.
	for _, r := range rows {
		if !r.Certain {
			t.Errorf("line %d not certain", r.Source.Start)
		}
	}

	// On disk but untracked (a nested repo, a local file): alive.
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "retired-notes.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lines(only(t, "dead-path", input(hit))); len(got) != 2 {
		t.Fatalf("an untracked file on disk is dead: %v", got)
	}
}

func TestDeadPathNearMiss(t *testing.T) {
	root := t.TempDir()
	near := inRepo(fixture(t, "dead-path", "near.md", discover.ClassRepo), root, "CLAUDE.md", "cmd/tool/main.go")
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "local.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if rows := only(t, "dead-path", input(near)); len(rows) != 0 {
		t.Fatalf("%+v", rows)
	}
}

// Outside a repo only home and absolute paths are checked, on disk, and a
// miss there is a judgment: the path may exist on another machine.
func TestDeadPathOutsideARepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "present.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &discover.File{Path: "/g/AGENTS.md", Class: discover.ClassGlobal,
		Content: "- See `~/present.md` and `~/absent.md`; in a repo, `internal/x/y.go`.\n"}
	rows := only(t, "dead-path", input(f))
	if len(rows) != 1 || evidence(rows[0], "missing") != "~/absent.md" || rows[0].Certain {
		t.Fatalf("%+v", rows)
	}
}
