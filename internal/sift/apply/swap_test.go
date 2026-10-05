package apply

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schuettc/tackle/internal/sift/row"
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
// the check and the write: creating, writing and deleting a file under it
// each fail the repo, and nothing outside the worktree changes.
func TestApplyRefusesAParentSwappedMidWrite(t *testing.T) {
	for name, mk := range map[string]func(g *rig) row.Row{
		"create": func(g *rig) row.Row {
			r := g.at("mv", "misplaced", 5, "- Never push to main.")
			r.Verdict, r.Destination = "move", "docs/new.md#Notes"
			return r
		},
		"write": func(g *rig) row.Row {
			r := g.at("mv", "misplaced", 5, "- Never push to main.")
			r.Verdict, r.Destination = "move", "docs/other.md#Notes"
			return r
		},
		"delete": func(g *rig) row.Row {
			r := g.at("rm", "size", 0, "")
			r.Source.File, r.Source.Path, r.Source.Start, r.Source.End = filepath.Join(g.repo, "docs", "other.md"), "docs/other.md", 0, 0
			r.Verdict = "delete"
			return r
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := newRig(t)
			out := t.TempDir()
			outFile := filepath.Join(out, "other.md")
			if err := os.WriteFile(outFile, []byte("outside\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			r := mk(g)
			verdict, dest := r.Verdict, r.Destination
			r.Verdict, r.Destination = "", ""
			g.record(r)
			if _, err := g.s.AddRows(ctx, g.round, []row.Row{{ID: r.ID, Verdict: verdict, Destination: dest}}); err != nil {
				t.Fatal(err)
			}
			g.decide(r.ID, row.Decision{Action: "accept"})
			before := g.primary()
			swapAt(t, "docs", out)
			res := g.run(Options{}).Repos[0]
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
		})
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
	if err := removeAt(root, "meta/config"); err == nil {
		t.Error("meta/config: removed from .git")
	}
	swapAt(t, "docs", ".git")
	if err := writeAt(root, "docs/config", "x\n"); err == nil {
		t.Error("docs/config: written into .git after a swap")
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".git", "config")); string(b) != "[core]\n" {
		t.Errorf(".git/config changed: %q", b)
	}
}
