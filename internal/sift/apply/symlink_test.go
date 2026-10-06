package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/row"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
)

const skill = "---\nname: dispatch\n---\n\n# Dispatch\n\n- waiting on the new runner\n"

// link makes a harness's skill path a symlink to target and returns the
// path, as a dotfiles install does.
func link(t *testing.T, target string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "skills", "dispatch", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
	return p
}

// linked is a file found at link whose real path is rel in repo, as the
// audit records it: read at origin/main there.
func (g *rig) linked(link, repo, rel string) rec.File {
	g.t.Helper()
	f := g.in(repo, rel)
	real, err := filepath.EvalSymlinks(repo)
	if err != nil {
		g.t.Fatal(err)
	}
	f = rec.NewFile(row.Source{File: link, Repo: real, Ref: "origin/main", Path: rel}, "skill", 10000, f.Content)
	f.Commit = st.Git(g.t, repo, "rev-parse", "origin/main")
	return f
}

// A skill (or a global) whose path is a symlink into a git repo, recorded
// by the audit in the repo that owns it, is written there: on that repo's
// round branch, at the file's path there, cut from the ref the audit read.
func TestASymlinkIntoARepoIsWrittenInThatRepo(t *testing.T) {
	g := newRig(t)
	dots := st.Repo(t, filepath.Join(t.TempDir(), "dotfiles"), map[string]string{"config/pi/skills/dispatch/SKILL.md": skill})
	st.Publish(t, dots)
	p := link(t, filepath.Join(dots, "config/pi/skills/dispatch/SKILL.md"))
	f := g.linked(p, dots, "config/pi/skills/dispatch/SKILL.md")
	g.audit(f)
	want := "---\nname: dispatch\n---\n\n# Dispatch\n"
	g.propose(g.rec(f, want))
	g.decide(f, rec.Decision{Action: "accept"})
	res := g.run(Options{})
	if len(res.Left) != 0 {
		t.Fatalf("left for the user: %+v", res.Left)
	}
	r := one(t, res)
	real, _ := filepath.EvalSymlinks(dots)
	if r.State != "branch" || r.Path != real || r.Base != "origin/main" || len(r.Applied) != 1 || r.Applied[0].Where != p {
		t.Fatalf("%+v", r)
	}
	if got := g.show(dots, r.Branch, "config/pi/skills/dispatch/SKILL.md"); got != want {
		t.Errorf("on the branch:\n%s", got)
	}
	if b, _ := os.ReadFile(p); string(b) != skill {
		t.Errorf("the checkout was written: %q", b)
	}
}

// A symlinked file a round recorded as read from disk (before the audit
// placed such files in their repo) is left for the user: apply never works
// out a repo or base the audit did not read.
func TestASymlinkRecordedFromDiskIsLeft(t *testing.T) {
	g := newRig(t)
	dots := st.Repo(t, filepath.Join(t.TempDir(), "dotfiles"), map[string]string{"SKILL.md": skill})
	st.Publish(t, dots)
	f := g.disk(link(t, filepath.Join(dots, "SKILL.md")))
	g.audit(f)
	g.propose(g.rec(f, "# Dispatch\n"))
	g.decide(f, rec.Decision{Action: "accept"})
	res := g.run(Options{})
	if len(res.Repos) != 0 || len(res.Left) != 1 || !strings.Contains(res.Left[0].Why, "not in a git repo") {
		t.Fatalf("%+v", res)
	}
}

// A symlink to a file outside every repo is still left for the user, with
// its approved copy saved.
func TestASymlinkOutsideEveryRepoIsLeft(t *testing.T) {
	g := newRig(t)
	target := st.Write(t, t.TempDir(), "SKILL.md", skill)
	p := link(t, target)
	f := g.disk(p)
	g.audit(f)
	g.propose(g.rec(f, "# Dispatch\n"))
	g.decide(f, rec.Decision{Action: "accept"})
	res := g.run(Options{})
	if len(res.Repos) != 0 || len(res.Left) != 1 || !strings.Contains(res.Left[0].Why, "not in a git repo") {
		t.Fatalf("%+v", res)
	}
	if b, err := os.ReadFile(res.Left[0].Approved); err != nil || string(b) != "# Dispatch\n" {
		t.Fatalf("approved copy %q %v", b, err)
	}
}
