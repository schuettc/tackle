package discover

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/schuettc/tackle/internal/sift/config"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
)

// A fork (a repo with an upstream remote) and a vendor-managed skill
// directory are not the user's to rewrite: skipped, and listed as skipped,
// unless the config includes them.
func TestForksAndVendorSkillsAreSkipped(t *testing.T) {
	st.Env(t)
	home := st.Home(t)
	ws := t.TempDir()
	own := st.Repo(t, filepath.Join(ws, "own"), map[string]string{"CLAUDE.md": "own\n"})
	fork := st.Repo(t, filepath.Join(ws, "fork"), map[string]string{"CLAUDE.md": "theirs\n", "skills/x/SKILL.md": "x\n"})
	st.Git(t, fork, "remote", "add", "upstream", "https://example.invalid/them/fork.git")
	st.Write(t, home, ".claude/skills/mine/SKILL.md", "mine\n")
	st.Write(t, home, ".claude/skills/synced/a/SKILL.md", "a\n")
	st.Write(t, home, ".claude/skills/synced/b/SKILL.md", "b\n")

	opt := Options{Profiles: profiles(t, "claude-code"), Roots: []config.Root{{Path: ws}}}
	res := run(t, opt)
	got := keys(byRel(res))
	want := []string{filepath.Join(home, ".claude/skills/mine/SKILL.md"), "own:CLAUDE.md"}
	sort.Strings(want)
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("audited %v, want %v", got, want)
	}
	kinds := map[string][]string{}
	for _, s := range res.Skipped {
		kinds[s.Why] = append(kinds[s.Why], s.Path)
	}
	if len(kinds["fork"]) != 1 || kinds["fork"][0] != fork {
		t.Errorf("forks skipped %v", kinds["fork"])
	}
	if len(kinds["vendor"]) != 2 {
		t.Errorf("vendor files skipped %v", kinds["vendor"])
	}
	_ = own

	opt.Include = config.Include{Forks: true, Vendor: true}
	res = run(t, opt)
	if n := len(res.Files); n != 6 || len(res.Skipped) != 0 {
		t.Fatalf("with include: %d files %v, skipped %v", n, keys(byRel(res)), res.Skipped)
	}
}
