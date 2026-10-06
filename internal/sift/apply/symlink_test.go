package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/rec"
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

// A skill (or a global) whose path is a symlink into a git repo is written
// in the repo that owns it: on that repo's round branch, at the file's path
// there, cut from the repo's base, as any repo file is.
func TestASymlinkIntoARepoIsWrittenInThatRepo(t *testing.T) {
	g := newRig(t)
	dots := st.Repo(t, filepath.Join(t.TempDir(), "dotfiles"), map[string]string{"config/pi/skills/dispatch/SKILL.md": skill})
	st.Publish(t, dots)
	p := link(t, filepath.Join(dots, "config/pi/skills/dispatch/SKILL.md"))
	f := g.disk(p)
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

// The real path's repo takes its base from a [[repo]] like any other.
func TestASymlinkIntoARepoUsesItsBase(t *testing.T) {
	g := newRig(t)
	dots := st.Repo(t, filepath.Join(t.TempDir(), "dotfiles"), map[string]string{"SKILL.md": "main\n"})
	st.Publish(t, dots)
	st.Git(t, dots, "checkout", "-q", "-b", "dev")
	st.Commit(t, dots, map[string]string{"SKILL.md": skill})
	st.Git(t, dots, "push", "-q", "-u", "origin", "dev")
	f := g.disk(link(t, filepath.Join(dots, "SKILL.md")))
	g.audit(f)
	g.propose(g.rec(f, "# Dispatch\n"))
	g.decide(f, rec.Decision{Action: "accept"})
	r := one(t, g.run(Options{DryRun: true, Repos: []config.Repo{{Path: dots, Base: "dev"}}}))
	if r.State != "planned" || r.Base != "origin/dev" {
		t.Fatalf("%+v", r)
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
