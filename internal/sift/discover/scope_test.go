package discover

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
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

// A configured root that holds the harness home leaves out its vendor
// directories as the skills walk does, on disk and in a repo's tree:
// nothing under them is audited, an instruction file included.
func TestARootHoldingTheHomeSkipsItsVendorDirs(t *testing.T) {
	vendored := map[string]string{
		"plugins/cache/p/CLAUDE.md":         "theirs\n",
		"plugins/cache/p/skills/s/SKILL.md": "s\n",
		"plugins/marketplaces/m/CLAUDE.md":  "theirs\n",
		"skills/synced/a/SKILL.md":          "a\n",
		"skills/synced/CLAUDE.md":           "theirs\n",
	}
	own := map[string]string{"skills/mine/SKILL.md": "mine\n", "notes/CLAUDE.md": "mine\n"}
	all := map[string]string{}
	for k, v := range vendored {
		all[k] = v
	}
	for k, v := range own {
		all[k] = v
	}
	audited := func(res Result) []string {
		var out []string
		for _, f := range res.Files {
			p := f.Path
			if f.Repo != nil {
				p = f.Rel
			}
			out = append(out, filepath.ToSlash(p))
		}
		sort.Strings(out)
		return out
	}
	check := func(t *testing.T, home string) {
		t.Helper()
		opt := Options{Profiles: profiles(t, "claude-code"), Roots: []config.Root{{Path: home}}}
		got := audited(run(t, opt))
		for _, p := range got {
			for v := range vendored {
				if strings.HasSuffix(p, v) {
					t.Errorf("audited vendor file %s", p)
				}
			}
		}
		for o := range own {
			if !slices.ContainsFunc(got, func(p string) bool { return strings.HasSuffix(p, o) }) {
				t.Errorf("left out %s: %v", o, got)
			}
		}
		opt.Include = config.Include{Vendor: true}
		got = audited(run(t, opt))
		for v := range vendored {
			if !slices.ContainsFunc(got, func(p string) bool { return strings.HasSuffix(p, v) }) {
				t.Errorf("with include, left out %s: %v", v, got)
			}
		}
	}
	t.Run("on disk", func(t *testing.T) {
		st.Env(t)
		home := st.Home(t)
		for k, v := range all {
			st.Write(t, home, ".claude/"+k, v)
		}
		check(t, home)
	})
	t.Run("in a repo", func(t *testing.T) {
		st.Env(t)
		home := st.Home(t)
		files := map[string]string{}
		for k, v := range all {
			files[".claude/"+k] = v
		}
		st.Repo(t, home, files)
		check(t, home)
	})
}

// A user skill that is a link to a directory outside the vendor dirs is
// the user's, and audited, even when a link inside a vendor dir reaches
// the same directory: the vendor check is by the path a skill is reached
// by (given and real), not by what else reaches its target. Named to sort
// after the vendor dir, so the vendor alias is met first.
func TestALinkedUserSkillIsAuditedWhenAVendorAliasSharesItsTarget(t *testing.T) {
	st.Env(t)
	home := st.Home(t)
	shared := filepath.Join(t.TempDir(), "shared")
	st.Write(t, shared, "SKILL.md", "shared\n")
	st.Write(t, home, ".claude/skills/synced/a/SKILL.md", "a\n")
	for _, link := range []string{".claude/skills/synced/alias", ".claude/skills/zlinked"} {
		if err := os.Symlink(shared, filepath.Join(home, link)); err != nil {
			t.Fatal(err)
		}
	}
	res := run(t, Options{Profiles: profiles(t, "claude-code")})
	var got []string
	for _, f := range res.Files {
		got = append(got, f.Path)
	}
	want := filepath.Join(home, ".claude/skills/zlinked/SKILL.md")
	if !slices.Contains(got, want) {
		t.Fatalf("left out the linked user skill %s: audited %v", want, got)
	}
	for _, p := range got {
		if strings.Contains(p, "/synced/") {
			t.Errorf("audited a vendor skill %s", p)
		}
	}
	var skipped []string
	for _, s := range res.Skipped {
		skipped = append(skipped, s.Path)
	}
	if !slices.Contains(skipped, filepath.Join(home, ".claude/skills/synced/alias/SKILL.md")) {
		t.Errorf("the vendor alias is not listed as skipped: %v", skipped)
	}
}
