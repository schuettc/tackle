package observe

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/docket/testgit"
)

func commitAt(t *testing.T, dir, file string, at time.Time) string {
	t.Helper()
	t.Setenv("GIT_COMMITTER_DATE", at.Format(time.RFC3339))
	t.Setenv("GIT_AUTHOR_DATE", at.Format(time.RFC3339))
	return testgit.Commit(t, dir, file, at.String())
}

func TestScan(t *testing.T) {
	testgit.Env(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	a := filepath.Join(root, "a")
	os.MkdirAll(a, 0o755)
	testgit.Git(t, a, "init", "-q", "-b", "main")
	d1 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	c1 := commitAt(t, a, "one", d1)
	testgit.Git(t, a, "remote", "add", "origin", "https://tok@github.com/Acme/Widget.git")
	testgit.Git(t, a, "update-ref", "refs/remotes/origin/main", c1)
	testgit.Git(t, a, "branch", "--set-upstream-to=origin/main", "main")
	commitAt(t, a, "two", d2)
	testgit.Git(t, a, "branch", "feat")
	// A squash-merged PR branch: upstream deleted on the remote after merge.
	testgit.Git(t, a, "branch", "merged", c1)
	testgit.Git(t, a, "update-ref", "refs/remotes/origin/merged", c1)
	testgit.Git(t, a, "branch", "--set-upstream-to=origin/merged", "merged")
	testgit.Git(t, a, "update-ref", "-d", "refs/remotes/origin/merged")
	testgit.Git(t, a, "worktree", "add", "-q", filepath.Join(root, "a-wt"), "feat")
	commitAt(t, filepath.Join(root, "a-wt"), "three", d2.Add(time.Hour))
	os.WriteFile(filepath.Join(root, "a-wt", "scratch"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(a, "one"), []byte("changed"), 0o644)
	testgit.Git(t, a, "stash", "-q")
	os.WriteFile(filepath.Join(a, "untracked"), []byte("x"), 0o644)
	testgit.Git(t, a, "config", "core.hooksPath", ".husky")

	nested := filepath.Join(a, "nested")
	os.MkdirAll(nested, 0o755)
	testgit.Git(t, nested, "init", "-q", "-b", "main")
	testgit.Git(t, nested, "remote", "add", "origin", "https://user:SECRET@gitlab.com/x/y.git")

	nm := filepath.Join(a, "node_modules", "dep")
	os.MkdirAll(nm, 0o755)
	testgit.Git(t, nm, "init", "-q")

	bare := filepath.Join(root, "b.git")
	testgit.Git(t, root, "init", "-q", "--bare", "-b", "main", bare)

	snap, errs := Scan(ctx, []string{root}, "mbp")
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if snap.Machine != "mbp" || snap.Version != SnapshotVersion {
		t.Errorf("header %+v", snap)
	}
	var paths []string
	for _, c := range snap.Clones {
		paths = append(paths, c.Path)
	}
	want := []string{a, nested, bare}
	if len(paths) != 3 || paths[0] != want[0] || paths[1] != want[1] || paths[2] != want[2] {
		t.Fatalf("clones %v, want %v", paths, want)
	}
	ca := snap.Clones[0]
	if ca.Repo != "acme/widget" || ca.Remotes["origin"] != "acme/widget" || !ca.Dirty || ca.Stashes != 1 || ca.LocalHooksPath != ".husky" || ca.Bare {
		t.Errorf("clone a %+v", ca)
	}
	br := map[string]Branch{}
	for _, b := range ca.Branches {
		br[b.Name] = b
	}
	if m := br["main"]; m.Upstream != "origin/main" || m.Ahead != 1 || m.Unpushed != 1 || !m.OldestUnpushed.Equal(d2) {
		t.Errorf("main %+v", m)
	}
	if f := br["feat"]; f.Unpushed != 2 || !f.OldestUnpushed.Equal(d2) || f.Upstream != "" || f.Gone {
		t.Errorf("feat %+v", f)
	}
	if m := br["merged"]; !m.Gone || m.Upstream != "origin/merged" {
		t.Errorf("merged %+v", m)
	}
	if len(ca.Worktrees) != 1 || ca.Worktrees[0].Path != filepath.Join(root, "a-wt") || ca.Worktrees[0].Branch != "feat" || !ca.Worktrees[0].Dirty {
		t.Errorf("worktrees %+v", ca.Worktrees)
	}
	if cn := snap.Clones[1]; cn.Repo != "" || cn.Remotes["origin"] != "url:https://gitlab.com/x/y.git" {
		t.Errorf("nested %+v", cn)
	}
	if !snap.Clones[2].Bare {
		t.Errorf("bare %+v", snap.Clones[2])
	}
	j1, _ := EncodeSnapshot(snap)
	snap2, _ := Scan(ctx, []string{root}, "mbp")
	j2, _ := EncodeSnapshot(snap2)
	if !bytes.Equal(j1, j2) {
		t.Error("snapshot is not deterministic")
	}
	if bytes.Contains(j1, []byte("SECRET")) || bytes.Contains(j1, []byte("tok@")) {
		t.Errorf("credentials in snapshot:\n%s", j1)
	}
}

func TestScanBareWithWorktrees(t *testing.T) {
	testgit.Env(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	src := testgit.NewRepo(t)
	g := filepath.Join(root, "galley")
	os.MkdirAll(g, 0o755)
	testgit.Git(t, root, "clone", "-q", "--bare", src, filepath.Join(g, ".git"))
	testgit.Git(t, g, "worktree", "add", "-q", filepath.Join(g, ".worktrees", "main"), "main")
	snap, errs := Scan(ctx, []string{root}, "mbp")
	if len(errs) != 0 || len(snap.Clones) != 1 {
		t.Fatalf("clones %+v errs %v", snap.Clones, errs)
	}
	c := snap.Clones[0]
	if !c.Bare || c.Path != g || len(c.Worktrees) != 1 || c.Worktrees[0].Branch != "main" {
		t.Errorf("galley-style clone %+v", c)
	}
}

func TestScanMissingRootIsReported(t *testing.T) {
	testgit.Env(t)
	_, errs := Scan(ctx, []string{filepath.Join(t.TempDir(), "nope")}, "mbp")
	if len(errs) != 1 {
		t.Fatalf("errs %v", errs)
	}
}
