package discover

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/profile"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
)

var ctx = context.Background()

func profiles(t *testing.T, names ...string) []profile.Profile {
	t.Helper()
	var out []profile.Profile
	for _, n := range names {
		p, ok := profile.Builtin(n)
		if !ok {
			t.Fatalf("no profile %s", n)
		}
		out = append(out, p)
	}
	return out
}

func run(t *testing.T, opt Options) Result {
	t.Helper()
	res, err := Run(ctx, opt)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func byRel(res Result) map[string]*File {
	m := map[string]*File{}
	for _, f := range res.Files {
		if f.Repo != nil {
			m[filepath.Base(f.Repo.Root)+":"+f.Rel] = f
		} else {
			m[f.Path] = f
		}
	}
	return m
}

func keys(m map[string]*File) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Repos are read at their fetched upstream base: neither a local commit nor
// an uncommitted edit counts.
func TestReadsTheFetchedBaseNotTheWorkingTree(t *testing.T) {
	st.Env(t)
	st.Home(t)
	ws := t.TempDir()
	repo := st.Repo(t, filepath.Join(ws, "app"), map[string]string{"CLAUDE.md": "published\n"})
	st.Publish(t, repo)
	st.Commit(t, repo, map[string]string{"CLAUDE.md": "local commit\n"})
	st.Write(t, repo, "CLAUDE.md", "uncommitted\n")

	res := run(t, Options{Profiles: profiles(t, "claude-code"), Roots: []config.Root{{Path: ws}}})
	f := byRel(res)["app:CLAUDE.md"]
	if f == nil {
		t.Fatalf("not found: %v", keys(byRel(res)))
	}
	if f.Content != "published\n" || f.Repo.Ref != "origin/main" || f.Class != ClassRepo {
		t.Fatalf("%+v ref %s", f, f.Repo.Ref)
	}
	if f.Path != filepath.Join(repo, "CLAUDE.md") {
		t.Errorf("path %s", f.Path)
	}
}

// A repo with no upstream is read at HEAD: committed work, not the working tree.
func TestNoUpstreamReadsHEAD(t *testing.T) {
	st.Env(t)
	st.Home(t)
	repo := st.Repo(t, t.TempDir(), map[string]string{"AGENTS.md": "committed\n"})
	st.Write(t, repo, "AGENTS.md", "uncommitted\n")
	res := run(t, Options{Profiles: profiles(t, "codex"), Roots: []config.Root{{Path: repo}}})
	if len(res.Files) != 1 || res.Files[0].Content != "committed\n" || res.Files[0].Repo.Ref != "HEAD" {
		t.Fatalf("%+v", res.Files)
	}
}

// A configured base wins over origin/HEAD when the repo has it.
func TestConfiguredBase(t *testing.T) {
	st.Env(t)
	st.Home(t)
	repo := st.Repo(t, t.TempDir(), map[string]string{"CLAUDE.md": "main\n"})
	st.Publish(t, repo)
	st.Git(t, repo, "checkout", "-q", "-b", "dev")
	st.Commit(t, repo, map[string]string{"CLAUDE.md": "dev\n"})
	st.Git(t, repo, "push", "-q", "origin", "dev")
	res := run(t, Options{Profiles: profiles(t, "claude-code"), Roots: []config.Root{{Path: repo, Base: "dev"}}})
	if len(res.Files) != 1 || res.Files[0].Content != "dev\n" || res.Files[0].Repo.Ref != "origin/dev" {
		t.Fatalf("%+v", res.Files)
	}
	// A base the repo lacks falls back to the repo's own default.
	res = run(t, Options{Profiles: profiles(t, "claude-code"), Roots: []config.Root{{Path: repo, Base: "nope"}}})
	if res.Files[0].Repo.Ref != "origin/main" {
		t.Fatalf("ref %s", res.Files[0].Repo.Ref)
	}
}

// A [[repo]] base wins over its root's for that repo alone: muster is read
// at origin/dev, its neighbour under the same root at its own default.
func TestRepoBaseOverride(t *testing.T) {
	st.Env(t)
	st.Home(t)
	ws := t.TempDir()
	muster := st.Repo(t, filepath.Join(ws, "muster"), map[string]string{"CLAUDE.md": "main\n"})
	st.Publish(t, muster)
	st.Git(t, muster, "checkout", "-q", "-b", "dev")
	st.Commit(t, muster, map[string]string{"CLAUDE.md": "dev\n"})
	st.Git(t, muster, "push", "-q", "origin", "dev")
	ops := st.Repo(t, filepath.Join(ws, "ops"), map[string]string{"CLAUDE.md": "main\n"})
	st.Publish(t, ops)
	st.Git(t, ops, "push", "-q", "origin", "main:dev")
	res := run(t, Options{Profiles: profiles(t, "claude-code"), Roots: []config.Root{{Path: ws}},
		Repos: []config.Repo{{Path: muster, Base: "dev"}}})
	m := byRel(res)
	if f := m["muster:CLAUDE.md"]; f == nil || f.Repo.Ref != "origin/dev" || f.Content != "dev\n" {
		t.Fatalf("muster %+v", f)
	}
	if f := m["ops:CLAUDE.md"]; f == nil || f.Repo.Ref != "origin/main" {
		t.Fatalf("ops (has an origin/dev, no override) %+v", f)
	}
}

// Files at any depth; skills anywhere in a repo; fixtures, vendored code,
// excluded globs, untracked files in a repo, nested repos and linked
// worktrees handled; files outside any repo read from disk.
func TestWorkspaceLayout(t *testing.T) {
	st.Env(t)
	st.Home(t)
	ws := t.TempDir()
	top := st.Repo(t, filepath.Join(ws, "top"), map[string]string{
		"CLAUDE.md":  "workspace\n",
		".gitignore": "member/\nscratch/\n",
	})
	st.Write(t, top, "scratch/CLAUDE.md", "untracked\n")
	member := st.Repo(t, filepath.Join(top, "member"), map[string]string{
		"CLAUDE.md":                     "member\n",
		"pkg/CLAUDE.md":                 "nested\n",
		".claude/skills/one/SKILL.md":   "skill one\n",
		"skills/two/SKILL.md":           "skill two\n",
		"internal/x/testdata/CLAUDE.md": "fixture\n",
		"node_modules/dep/CLAUDE.md":    "dep\n",
		"examples/CLAUDE.md":            "example\n",
		"README.md":                     "readme\n",
	})
	st.Git(t, member, "worktree", "add", "-q", filepath.Join(ws, "wt"), "-b", "side")
	st.Write(t, ws, "loose/CLAUDE.md", "not in a repo\n")
	st.Write(t, ws, "loose/deep/x/SKILL.md", "loose skill\n")
	st.Write(t, ws, "loose/node_modules/y/CLAUDE.md", "dep\n")

	res := run(t, Options{
		Profiles: profiles(t, "claude-code"),
		Roots:    []config.Root{{Path: ws, Exclude: []string{"examples/**"}}},
	})
	m := byRel(res)
	var got []string
	for _, k := range keys(m) {
		got = append(got, strings.TrimPrefix(k, ws+"/"))
	}
	want := []string{
		"loose/CLAUDE.md", "loose/deep/x/SKILL.md",
		"member:.claude/skills/one/SKILL.md", "member:CLAUDE.md", "member:pkg/CLAUDE.md", "member:skills/two/SKILL.md",
		"top:CLAUDE.md",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("files\n got %v\nwant %v", got, want)
	}
	if m["member:skills/two/SKILL.md"].Class != ClassSkill || m[filepath.Join(ws, "loose/CLAUDE.md")].Class != ClassRepo {
		t.Error("classes")
	}
	if len(res.Repos) != 2 {
		t.Errorf("repos %d (the linked worktree is not a repo of its own)", len(res.Repos))
	}
}

// Codex reads AGENTS.override.md before AGENTS.md, one per directory, and
// its chain runs from the repo root down; the chain counts against its limit.
func TestOverridePrecedenceAndChains(t *testing.T) {
	st.Env(t)
	home := st.Home(t)
	st.Write(t, home, ".codex/AGENTS.md", strings.Repeat("g", 100))
	repo := st.Repo(t, t.TempDir(), map[string]string{
		"AGENTS.md":          strings.Repeat("a", 10),
		"AGENTS.override.md": strings.Repeat("o", 20),
		"svc/AGENTS.md":      strings.Repeat("s", 30),
		"svc/api/AGENTS.md":  strings.Repeat("p", 40),
		"svc/api/CLAUDE.md":  "claude only\n",
		"tools/AGENTS.md":    strings.Repeat("t", 5),
	})
	res := run(t, Options{Profiles: profiles(t, "codex"), Roots: []config.Root{{Path: repo}}})
	m := byRel(res)
	name := filepath.Base(repo)
	if _, ok := m[name+":AGENTS.md"]; ok {
		t.Error("AGENTS.md beside an override is read by no enabled harness")
	}
	if _, ok := m[name+":svc/api/CLAUDE.md"]; ok {
		t.Error("CLAUDE.md is not Codex's")
	}
	if f := m[name+":AGENTS.override.md"]; f == nil || !reflect.DeepEqual(f.Profiles, []string{"codex"}) {
		t.Fatalf("override %+v", f)
	}
	var chains []string
	for _, c := range res.Chains {
		var rels []string
		for _, f := range c.Files {
			rels = append(rels, f.Rel)
		}
		chains = append(chains, fmt.Sprintf("%s %s %s %d/%d", c.Profile, c.Dir, strings.Join(rels, ","), c.Bytes, c.Limit))
	}
	sort.Strings(chains)
	want := []string{
		"codex svc/api ," + "AGENTS.override.md,svc/AGENTS.md,svc/api/AGENTS.md 190/32768",
		"codex tools ,AGENTS.override.md,tools/AGENTS.md 125/32768",
	}
	if !reflect.DeepEqual(chains, want) {
		t.Fatalf("chains\n got %q\nwant %q", chains, want)
	}
}

// The same global file reached through two harnesses' symlinks is one file.
func TestGlobalSymlinksAreOneFile(t *testing.T) {
	st.Env(t)
	home := st.Home(t)
	real := st.Write(t, home, "dotfiles/AGENTS.md", "global rules\n")
	for _, link := range []string{".claude/CLAUDE.md", ".pi/agent/AGENTS.md"} {
		p := filepath.Join(home, link)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, p); err != nil {
			t.Fatal(err)
		}
	}
	res := run(t, Options{Profiles: profiles(t, "claude-code", "codex", "pi")})
	if len(res.Files) != 1 {
		t.Fatalf("files %+v", res.Files)
	}
	f := res.Files[0]
	if f.Class != ClassGlobal || !reflect.DeepEqual(f.Profiles, []string{"claude-code", "pi"}) ||
		f.Path != filepath.Join(home, ".claude/CLAUDE.md") || !reflect.DeepEqual(f.Also, []string{filepath.Join(home, ".pi/agent/AGENTS.md")}) {
		t.Fatalf("%+v", f)
	}
}

// An installed skill that is a copy of a skill sift audits at its source
// (installers add a stamp) is counted once, at the source. A skill with the
// same name and different content is not a copy.
func TestInstalledCopiesCollapseIntoTheirSource(t *testing.T) {
	st.Env(t)
	home := st.Home(t)
	body := strings.Repeat("A line of guidance about doing the work well.\n", 30)
	repo := st.Repo(t, t.TempDir(), map[string]string{
		"skills/alpha/SKILL.md": body,
		"skills/beta/SKILL.md":  body + "beta\n",
	})
	st.Write(t, home, ".pi/agent/skills/alpha/SKILL.md", body+"<!-- installed by a tool, v1.2.3 -->\n")
	st.Write(t, home, ".pi/agent/skills/beta/SKILL.md", "Something else entirely.\n")
	st.Write(t, home, ".pi/agent/skills/.system/bundled/SKILL.md", "the harness's own\n")
	// The skills directory itself may be a symlink.
	other := st.Write(t, home, "elsewhere/gamma/SKILL.md", "gamma\n")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(filepath.Dir(other)), filepath.Join(home, ".claude", "skills")); err != nil {
		t.Fatal(err)
	}

	res := run(t, Options{Profiles: profiles(t, "claude-code", "pi"), Roots: []config.Root{{Path: repo}}})
	m := byRel(res)
	name := filepath.Base(repo)
	for _, k := range []string{
		name + ":skills/alpha/SKILL.md", name + ":skills/beta/SKILL.md",
		filepath.Join(home, ".pi/agent/skills/beta/SKILL.md"),
		filepath.Join(home, ".claude/skills/gamma/SKILL.md"),
	} {
		if m[k] == nil {
			t.Errorf("missing %s; have %v", k, keys(m))
		}
	}
	if m[filepath.Join(home, ".pi/agent/skills/alpha/SKILL.md")] != nil {
		t.Error("the installed copy is audited too")
	}
	if m[filepath.Join(home, ".pi/agent/skills/.system/bundled/SKILL.md")] != nil {
		t.Error("a hidden directory in a skills directory is audited")
	}
	want := []Copy{{Path: filepath.Join(home, ".pi/agent/skills/alpha/SKILL.md"), Of: filepath.Join(repo, "skills/alpha/SKILL.md")}}
	if !reflect.DeepEqual(res.Copies, want) {
		t.Fatalf("copies %+v", res.Copies)
	}
	if f := m[filepath.Join(home, ".pi/agent/skills/beta/SKILL.md")]; f.Class != ClassSkill || !reflect.DeepEqual(f.Profiles, []string{"pi"}) {
		t.Errorf("%+v", f)
	}
}

func TestMissingRootIsAWarning(t *testing.T) {
	st.Env(t)
	st.Home(t)
	res := run(t, Options{Profiles: profiles(t, "pi"), Roots: []config.Root{{Path: filepath.Join(t.TempDir(), "gone")}}})
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "gone") {
		t.Fatalf("%v", res.Warnings)
	}
}

func TestGlob(t *testing.T) {
	for _, c := range []struct {
		glob, path string
		want       bool
	}{
		{"examples/**", "examples/a/CLAUDE.md", true},
		{"examples/**", "src/examples/CLAUDE.md", false},
		{"**/fixtures/**", "a/b/fixtures/CLAUDE.md", true},
		{"**/fixtures/**", "fixtures/CLAUDE.md", true},
		{"docs/*.md", "docs/CLAUDE.md", true},
		{"docs/*.md", "docs/a/CLAUDE.md", false},
	} {
		if got := Match(c.glob, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v", c.glob, c.path, got)
		}
	}
}

// Run from a git hook, sift sees GIT_DIR and the like for another repo;
// every repo is still read as itself.
func TestIgnoresRepoSelectingVariables(t *testing.T) {
	st.Env(t)
	st.Home(t)
	other := st.Repo(t, filepath.Join(t.TempDir(), "other"), map[string]string{"CLAUDE.md": "other\n"})
	ws := t.TempDir()
	st.Repo(t, filepath.Join(ws, "app"), map[string]string{"CLAUDE.md": "app\n"})
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))
	t.Setenv("GIT_COMMON_DIR", filepath.Join(other, ".git"))

	res := run(t, Options{Profiles: profiles(t, "claude-code"), Roots: []config.Root{{Path: ws}}})
	f := byRel(res)["app:CLAUDE.md"]
	if f == nil || f.Content != "app\n" {
		t.Fatalf("read %+v (files %v, warnings %v)", f, keys(byRel(res)), res.Warnings)
	}
}

// GitEnv drops every variable that picks a repository, and keeps the rest.
func TestGitEnvDropsRepoSelectors(t *testing.T) {
	env := GitEnv([]string{"GIT_DIR=/x", "GIT_WORK_TREE=/y", "GIT_INDEX_FILE=/z", "GIT_OBJECT_DIRECTORY=/o",
		"GIT_COMMON_DIR=/c", "GIT_PREFIX=p/", "HOME=/h", "GIT_CONFIG_GLOBAL=/g"})
	if strings.Join(env, " ") != "HOME=/h GIT_CONFIG_GLOBAL=/g" {
		t.Fatalf("%v", env)
	}
}

// A root inside a repo is read at the repo's base like any repo, limited to
// the root's subtree: the committed text, not the working tree's, and
// nothing above the root.
func TestRootInsideARepoReadsItsBase(t *testing.T) {
	st.Env(t)
	st.Home(t)
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{
		"CLAUDE.md": "top\n", "docs/CLAUDE.md": "committed\n", "docs/guide/CLAUDE.md": "guide\n", "other/CLAUDE.md": "other\n",
	})
	st.Write(t, repo, "docs/CLAUDE.md", "changed\n")

	res := run(t, Options{Profiles: profiles(t, "claude-code"), Roots: []config.Root{{Path: filepath.Join(repo, "docs")}}})
	got := byRel(res)
	if k := strings.Join(keys(got), " "); k != "app:docs/CLAUDE.md app:docs/guide/CLAUDE.md" {
		t.Fatalf("files %s (warnings %v)", k, res.Warnings)
	}
	f := got["app:docs/CLAUDE.md"]
	if f.Content != "committed\n" || f.Repo.Ref != "HEAD" || f.Path != filepath.Join(repo, "docs", "CLAUDE.md") {
		t.Fatalf("%+v ref %s", f, f.Repo.Ref)
	}
	if !f.Repo.Has("other/CLAUDE.md") {
		t.Error("the repo's tree is the whole tree, for resolving paths")
	}
}

// A skill installed as a symlink to its directory is followed: one file per
// real path, merged into its source when sift audits that, read from disk
// when not; a link back up the tree does not loop.
func TestSymlinkedSkillDirectories(t *testing.T) {
	st.Env(t)
	home := st.Home(t)
	repo := st.Repo(t, t.TempDir(), map[string]string{"skills/delta/SKILL.md": "delta\n"})
	link := func(target, at string) {
		t.Helper()
		p := filepath.Join(home, at)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
	}
	link(filepath.Join(repo, "skills", "delta"), ".pi/agent/skills/delta")
	link(filepath.Join(repo, "skills", "delta"), ".claude/skills/delta")
	link(filepath.Join(home, ".pi/agent/skills"), ".pi/agent/skills/loop")
	installed := filepath.Join(home, ".pi/agent/skills/delta/SKILL.md")

	// Its source audited too: one file, at the source.
	res := run(t, Options{Profiles: profiles(t, "claude-code", "pi"), Roots: []config.Root{{Path: repo}}})
	m := byRel(res)
	src := m[filepath.Base(repo)+":skills/delta/SKILL.md"]
	if len(res.Files) != 1 || src == nil || !contains(src.Also, installed) || !reflect.DeepEqual(src.Profiles, []string{"claude-code", "pi"}) {
		t.Fatalf("files %v; %+v", keys(m), src)
	}

	// Only the installed link: read once, at the base of the repo it
	// links into, as that repo's file.
	res = run(t, Options{Profiles: profiles(t, "claude-code", "pi")})
	if len(res.Files) != 1 {
		t.Fatalf("files %v", keys(byRel(res)))
	}
	f := res.Files[0]
	if f.Content != "delta\n" || f.Class != ClassSkill || f.Repo == nil || f.Rel != "skills/delta/SKILL.md" || len(f.Profiles) != 2 {
		t.Fatalf("%+v", f)
	}
}

// A repo knows which files its history had and its base has not: the only
// misses a dead-path row can be certain of.
func TestRepoKnowsItsGoneFiles(t *testing.T) {
	st.Env(t)
	st.Home(t)
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{"CLAUDE.md": "x\n", "docs/old.md": "x\n", "docs/moved.md": "x\n"})
	st.Git(t, repo, "rm", "-q", "docs/old.md")
	st.Git(t, repo, "mv", "docs/moved.md", "docs/new.md")
	st.Commit(t, repo, nil)
	res := run(t, Options{Profiles: profiles(t, "claude-code"), Roots: []config.Root{{Path: repo}}})
	if len(res.Repos) != 1 {
		t.Fatalf("repos %+v", res.Repos)
	}
	r := res.Repos[0]
	if !r.Gone["docs/old.md"] || !r.Gone["docs/moved.md"] || r.Gone["docs/new.md"] || r.Gone["CLAUDE.md"] || r.Gone["docs"] {
		t.Fatalf("gone %v", r.Gone)
	}
}

// A global file that links to an instruction file in an audited repo is one
// file, read at the repo's base, loaded by both the global's harness and the
// repo's, and in the repo's chain.
func TestGlobalLinkedIntoARepo(t *testing.T) {
	st.Env(t)
	home := st.Home(t)
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{
		"AGENTS.md": "committed\n", "svc/AGENTS.md": "svc\n",
	})
	st.Write(t, repo, "AGENTS.md", "changed\n")
	global := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(global), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repo, "AGENTS.md"), global); err != nil {
		t.Fatal(err)
	}

	res := run(t, Options{Profiles: profiles(t, "claude-code", "codex"), Roots: []config.Root{{Path: repo}}})
	m := byRel(res)
	if k := strings.Join(keys(m), " "); k != "app:AGENTS.md app:svc/AGENTS.md" {
		t.Fatalf("files %s", k)
	}
	f := m["app:AGENTS.md"]
	if f.Content != "committed\n" || f.Path != global || f.Class != ClassGlobal ||
		!reflect.DeepEqual(f.Profiles, []string{"claude-code", "codex"}) || !reflect.DeepEqual(f.Also, []string{filepath.Join(repo, "AGENTS.md")}) {
		t.Fatalf("%+v", f)
	}
	if len(res.Chains) != 1 || len(res.Chains[0].Files) != 2 || res.Chains[0].Files[0] != f || res.Chains[0].Dir != "svc" {
		t.Fatalf("chains %+v", res.Chains)
	}
}

// A global that links into one repo is that harness's global and, for the
// harness that picks it there, a file of that repo only: it does not become
// the other harness's global, so other repos' chains start with their own.
func TestGlobalLinkedIntoARepoIsNotAnotherHarnesssGlobal(t *testing.T) {
	st.Env(t)
	home := st.Home(t)
	ws := t.TempDir()
	a := st.Repo(t, filepath.Join(ws, "a"), map[string]string{"AGENTS.md": "a agents\n", "CLAUDE.md": "a claude\n"})
	st.Repo(t, filepath.Join(ws, "b"), map[string]string{"AGENTS.md": "b agents\n", "CLAUDE.md": "b claude\n"})
	codexGlobal := st.Write(t, home, ".codex/AGENTS.md", "codex global\n")
	claudeGlobal := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(claudeGlobal), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(a, "AGENTS.md"), claudeGlobal); err != nil {
		t.Fatal(err)
	}
	profs := profiles(t, "claude-code", "codex")
	profs[0].LoadLimit = 1 << 20 // so Claude has chains to look at too

	res := run(t, Options{Profiles: profs, Roots: []config.Root{{Path: ws}}})
	chains := map[string][]string{}
	for _, c := range res.Chains {
		var paths []string
		for _, f := range c.Files {
			p := f.Path
			if f.Repo != nil {
				p = filepath.Base(f.Repo.Root) + ":" + f.Rel
			}
			paths = append(paths, p)
		}
		chains[c.Profile+" "+filepath.Base(c.Repo.Root)+" "+c.Dir] = paths
	}
	want := map[string][]string{
		"codex a ":       {codexGlobal, "a:AGENTS.md"},
		"codex b ":       {codexGlobal, "b:AGENTS.md"},
		"claude-code a ": {"a:AGENTS.md", "a:CLAUDE.md"},
		"claude-code b ": {"a:AGENTS.md", "b:CLAUDE.md"},
	}
	if !reflect.DeepEqual(chains, want) {
		t.Fatalf("chains\n got %q\nwant %q", chains, want)
	}
	var loads []string
	for _, l := range res.Loads {
		if l.File.Rel == "AGENTS.md" && filepath.Base(l.File.Repo.Root) == "a" {
			loads = append(loads, fmt.Sprintf("%s %s %q", l.Profile, l.Role, l.Dir))
		}
	}
	if w := []string{`claude-code global ""`, `codex repo ""`}; !reflect.DeepEqual(loads, w) {
		t.Fatalf("loads of a:AGENTS.md %q, want %q", loads, w)
	}
}

// A file read as context for a subtree root and audited by a whole-repo
// root is one file, audited, whichever root comes first.
func TestContextFileAuditedByAnotherRoot(t *testing.T) {
	st.Env(t)
	st.Home(t)
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{"AGENTS.md": "top\n", "svc/AGENTS.md": "svc\n"})
	sub, whole := config.Root{Path: filepath.Join(repo, "svc")}, config.Root{Path: repo}
	for _, roots := range [][]config.Root{{sub, whole}, {whole, sub}} {
		res := run(t, Options{Profiles: profiles(t, "codex"), Roots: roots})
		m := byRel(res)
		if k := strings.Join(keys(m), " "); k != "app:AGENTS.md app:svc/AGENTS.md" || m["app:AGENTS.md"].Context {
			t.Fatalf("files %s", k)
		}
		if len(res.Chains) != 1 || len(res.Chains[0].Files) != 2 || res.Chains[0].Files[0] != m["app:AGENTS.md"] {
			t.Fatalf("chains %+v", res.Chains)
		}
	}
}
