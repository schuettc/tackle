package apply

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schuettc/tackle/internal/sift/rec"
)

// swapAt replaces dir, once, with a symlink to to, the moment apply has
// checked it and before it is used: the race a path check can't close.
func swapAt(t *testing.T, base, to string) {
	t.Helper()
	done := false
	afterCheck = func(dir string) {
		if done || filepath.Base(dir) != base {
			return
		}
		done = true
		if err := os.Rename(dir, dir+".moved"); err != nil {
			t.Error(err)
			return
		}
		if err := os.Symlink(to, dir); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { afterCheck = nil })
}

// A checked parent swapped for a symlink to an outside directory between
// the check and the write: writing the file under it fails the repo, and
// nothing outside the worktree changes.
func TestApplyRefusesAParentSwappedMidWrite(t *testing.T) {
	g := newRig(t)
	out := t.TempDir()
	outFile := filepath.Join(out, "other.md")
	if err := os.WriteFile(outFile, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := g.in(g.repo, "docs/other.md")
	g.audit(o)
	g.propose(g.rec(o, "# Other\n\n- rewritten\n"))
	g.decide(o, rec.Decision{Action: "accept"})
	before := g.primary()
	swapAt(t, "docs", out)
	res := one(t, g.run(Options{}))
	if res.State != "failed" {
		t.Errorf("state %q (%s), want failed", res.State, res.Detail)
	}
	if b, err := os.ReadFile(outFile); err != nil || string(b) != "outside\n" {
		t.Errorf("the outside file changed: %q %v", b, err)
	}
	if files := tree(t, out); len(files) != 1 {
		t.Errorf("files outside: %v", files)
	}
	if g.primary() != before {
		t.Error("the primary clone's working tree or index changed")
	}
}

// A parent that resolves into .git is refused, whether it was a symlink
// when checked or became one after.
func TestAParentIntoGitIsRefused(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".git", filepath.Join(root, "meta")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeAt(root, "meta/config", "x\n"); err == nil {
		t.Error("meta/config: written into .git")
	}
	swapAt(t, "docs", ".git")
	if err := writeAt(root, "docs/config", "x\n"); err == nil {
		t.Error("docs/config: written into .git after a swap")
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".git", "config")); string(b) != "[core]\n" {
		t.Errorf(".git/config changed: %q", b)
	}
}
