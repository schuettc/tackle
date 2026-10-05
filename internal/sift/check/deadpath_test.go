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
	hit.Repo.Gone = map[string]bool{"docs/CHANGES.md": true, "docs/retired-notes.md": true}
	rows := only(t, "dead-path", input(hit))
	if got := lines(rows); len(got) != 3 || got[0] != 3 || got[1] != 4 || got[2] != 5 {
		t.Fatalf("lines %v: %+v", got, rows)
	}
	if evidence(rows[0], "missing") != "internal/tool/" || evidence(rows[1], "missing") != "docs/CHANGES.md" {
		t.Errorf("evidence %+v %+v", rows[0].Evidence, rows[1].Evidence)
	}
	// A file the history had, gone at the audited commit and not on disk
	// either, is certain; a directory (line 3) is a judgment.
	for _, r := range rows {
		if r.Certain != (r.Source.Start != 3) {
			t.Errorf("line %d certain=%v", r.Source.Start, r.Certain)
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

// Near misses: untracked files on disk, refs and hosts, URLs, placeholders,
// globs, slash commands, ellipses, and a path that lives in another audited
// repo.
func TestDeadPathNearMiss(t *testing.T) {
	root := t.TempDir()
	near := inRepo(fixture(t, "dead-path", "near.md", discover.ClassRepo), root, "CLAUDE.md", "cmd/tool/main.go")
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "local.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sibling := &discover.Repo{Root: "/w/shared", Tree: map[string]bool{"docs": true, "docs/shared": true, "docs/shared/GUIDE.md": true}}
	in := input(near)
	in.Repos = []*discover.Repo{near.Repo, sibling}
	if rows := only(t, "dead-path", in); len(rows) != 0 {
		t.Fatalf("%+v", rows)
	}
}

// A skill is used in whatever repo the agent is in, so its relative paths
// are not resolved against the repo it lives in; home paths still are.
func TestDeadPathInASkill(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := inRepo(fixture(t, "dead-path", "skill.md", discover.ClassSkill), t.TempDir(), "skills/example/SKILL.md")
	rows := only(t, "dead-path", input(f))
	if len(rows) != 1 || evidence(rows[0], "missing") != "~/.example-missing/settings.json" || rows[0].Certain {
		t.Fatalf("%+v", rows)
	}
}

// Only a missing file is certain: a missing directory is often a convention
// ("specs go in docs/specs/") or a name.
func TestDeadPathDirectoryIsAJudgment(t *testing.T) {
	f := inRepo(&discover.File{Class: discover.ClassRepo, Content: "- Specs go in `docs/specs/`.\n"}, t.TempDir(), "CLAUDE.md")
	rows := only(t, "dead-path", input(f))
	if len(rows) != 1 || rows[0].Certain {
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

// A missing path is certain only when the repo's history had it as a file:
// an extensionless name may be a directory convention ("`./docs/specs`"), and
// a file that never existed may be one still to write.
func TestDeadPathNeverInHistoryIsAJudgment(t *testing.T) {
	f := inRepo(&discover.File{Class: discover.ClassRepo,
		Content: "- Specs go in `./docs/specs`.\n- Notes go in `docs/plan.md`.\n- Old notes: `docs/old.md`.\n"}, t.TempDir(), "CLAUDE.md")
	f.Repo.Gone = map[string]bool{"docs/old.md": true, "docs": true}
	rows := only(t, "dead-path", input(f))
	if len(rows) != 3 || rows[0].Certain || rows[1].Certain || !rows[2].Certain {
		t.Fatalf("%+v", rows)
	}
}
