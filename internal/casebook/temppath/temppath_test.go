package temppath

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/casebook/journal"
)

func TestDefaultRootsCoverTheSystemTempFolders(t *testing.T) {
	t.Setenv("TMPDIR", "/var/folders/zz/abc/T/")
	roots := DefaultRoots()
	for _, want := range []string{"/tmp", "/private/tmp", "/var/folders", "/private/var/folders", "/var/folders/zz/abc/T"} {
		if !slices.Contains(roots, want) {
			t.Errorf("DefaultRoots() = %v, missing %s", roots, want)
		}
	}
	if !slices.Contains(roots, filepath.Clean(os.TempDir())) {
		t.Errorf("DefaultRoots() = %v, missing os.TempDir() %s", roots, os.TempDir())
	}
}

func TestPathMatchesWholeSegments(t *testing.T) {
	m := New(nil)
	for p, want := range map[string]bool{
		"/tmp":                 true,
		"/tmp/x/y":             true,
		"/private/tmp/scratch": true,
		"/var/folders/92/abc/T/pytest-of-court/x": true,
		"/private/var/folders/92/abc/T/tmp.AbC":   true,
		"/tmpfoo":                                 false,
		"/tmpfoo/bar":                             false,
		"/var/foldersx/a":                         false,
		"/Users/court/GitHub/tackle":              false,
		"/Users/court/tmp/x":                      false,
		"":                                        false,
		"relative/tmp":                            false,
	} {
		if got := m.Path(p); got != want {
			t.Errorf("Path(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestPathResolvesSymlinks(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	real := filepath.Join(base, "realtmp")
	link := filepath.Join(base, "linktmp")
	other := filepath.Join(base, "home")
	for _, d := range []string{filepath.Join(real, "clone"), other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	// A root given through a symlink matches the real path, and the reverse.
	m := newWith([]string{link}, nil)
	if !m.Path(filepath.Join(real, "clone")) || !m.Path(filepath.Join(link, "clone")) {
		t.Error("a root named through a symlink does not match both spellings")
	}
	m = newWith([]string{real}, nil)
	if !m.Path(filepath.Join(link, "clone")) {
		t.Error("a path through a symlink into the root is not matched")
	}
	if m.Path(other) || m.Path(filepath.Join(base, "realtmpfoo")) {
		t.Error("a sibling of the root is matched")
	}
}

func TestKeepRootsWin(t *testing.T) {
	m := newWith([]string{"/tmp"}, []string{"/tmp/home/GitHub"})
	if m.Path("/tmp/home/GitHub/hail") {
		t.Error("a clone inside a configured root is treated as temp")
	}
	if !m.Path("/tmp/home/scratch") {
		t.Error("temp outside the configured root is not matched")
	}
}

func TestEvent(t *testing.T) {
	m := newWith([]string{"/tmp"}, nil)
	for name, tc := range map[string]struct {
		ev   journal.Event
		want bool
	}{
		"hook in a real repo":   {journal.Event{Src: "git-hook", CWD: "/Users/c/GitHub/a"}, false},
		"hook in temp":          {journal.Event{Src: "git-hook", CWD: "/tmp/x"}, true},
		"real cwd, temp gitdir": {journal.Event{Src: "git-hook", CWD: "/Users/c/GitHub/a", GitDir: "/tmp/probe/repo/.git"}, true},
		"agent in temp":         {journal.Event{Src: "pi", CWD: "/tmp/x", Actions: []journal.Action{{Tool: "git", Verb: "push"}}}, true},
		"agent -C into temp":    {journal.Event{Src: "pi", CWD: "/Users/c", Actions: []journal.Action{{Tool: "git", Verb: "push", Dir: "/tmp/x"}}}, true},
		"agent relative -C":     {journal.Event{Src: "pi", CWD: "/tmp", Actions: []journal.Action{{Tool: "git", Verb: "push", Dir: "x"}}}, true},
		"agent in temp, -C real repo": {journal.Event{Src: "claude", CWD: "/tmp/x",
			Actions: []journal.Action{{Tool: "git", Verb: "push", Dir: "/Users/c/GitHub/a"}}}, false},
		"agent in temp, gh -R": {journal.Event{Src: "claude", CWD: "/tmp/x",
			Actions: []journal.Action{{Tool: "gh", Verb: "pr merge", Repo: "schuettc/hail", Number: 3}}}, false},
		"agent in temp, gh no -R": {journal.Event{Src: "claude", CWD: "/tmp/x",
			Actions: []journal.Action{{Tool: "gh", Verb: "pr view"}}}, true},
		"agent, one temp and one real action": {journal.Event{Src: "pi", CWD: "/tmp/x",
			Actions: []journal.Action{{Tool: "git", Verb: "clone"}, {Tool: "git", Verb: "push", Dir: "/Users/c/GitHub/a"}}}, false},
	} {
		if got := m.Event(tc.ev); got != tc.want {
			t.Errorf("%s: Event = %v, want %v", name, got, tc.want)
		}
	}
}

// TestEventKeepsRealWorkInTempFolders: a temp folder doing real work is not
// temp. A linked worktree of a clone in a configured root, a push to a
// non-local remote (a scratch clone's hook, or an agent's `git -C <tmp>
// push`), are kept; the copier, probe and local-test-remote noise stays temp,
// whatever repo its line was annotated with.
func TestEventKeepsRealWorkInTempFolders(t *testing.T) {
	m := newWith([]string{"/tmp", "/private/tmp", "/var/folders", "/private/var/folders"}, []string{"/Users/c/GitHub", "/Users/c/dotfiles"})
	const muda = "/Users/c/GitHub/schuettc/tools-workspace/muda"
	pushHook := func(cwd, url string) journal.Event {
		return journal.Event{Src: "git-hook", Hook: "pre-push", Args: []string{"origin", url}, CWD: cwd}
	}
	for name, tc := range map[string]struct {
		ev   journal.Event
		want bool
	}{
		// 1. A worktree in /tmp of a rooted repo: its git dir is in the root.
		"worktree in /tmp of a rooted repo": {journal.Event{Src: "git-hook", Hook: "reference-transaction", Args: []string{"committed"},
			CWD: "/tmp/bridge-rebase", GitDir: "/Users/c/GitHub/schuettc/tools-workspace/pi-claude-bridge/.git/worktrees/bridge-rebase", Repo: "elidickinson/pi-claude-bridge"}, false},
		"worktree in /private/tmp of a dotfiles clone": {journal.Event{Src: "git-hook", Hook: "post-commit",
			CWD: "/private/tmp/dots-wt", GitDir: "/Users/c/dotfiles/.git/worktrees/dots-wt"}, false},
		// 2. A scratch clone pushing to GitHub, in each remote spelling.
		"scratch clone pushes to github (https)": {pushHook("/tmp/p40b/pi-usage", "https://github.com/schuettc/pi-usage.git"), false},
		"scratch clone pushes to github (scp)":   {pushHook("/private/tmp/bhcli83", "git@github.com:recreational-spreadsheeting/bettor-help-cli.git"), false},
		"scratch clone pushes to github (ssh)":   {pushHook("/private/tmp/t4/pi-usage", "ssh://github.com/schuettc/pi-usage.git"), false},
		// 3. An agent's `git -C /private/tmp/x push` to GitHub: the worktree
		// is gone, its actions were annotated at sync from the tracked clone.
		"agent git -C /private/tmp push to github": {journal.Event{Src: "pi", CWD: "/Users/c/GitHub/schuettc/tools-workspace", Repo: "schuettc/tools-workspace",
			Actions: []journal.Action{
				{Tool: "git", Verb: "commit", Dir: "/private/tmp/muda-plan", Repo: "schuettc/tools-ops"},
				{Tool: "git", Verb: "push", Dir: "/private/tmp/muda-plan", Repo: "schuettc/tools-ops", Refs: []string{"origin", "docs/muda-plan"}},
			}}, false},
		"agent pushes a temp clone to a github url": {journal.Event{Src: "claude", CWD: "/tmp/x",
			Actions: []journal.Action{{Tool: "git", Verb: "push", Refs: []string{"git@github.com:a/b.git", "main"}}}}, false},

		// Negatives: still temp.
		"copier clone annotated schuettc/muda": {journal.Event{Src: "git-hook", Hook: "reference-transaction", Args: []string{"committed"},
			CWD: muda, GitDir: "/private/var/folders/92/ab/T/copier._vcs.clone.k3j2/.git", Repo: "schuettc/muda"}, true},
		"copier clone checkout annotated schuettc/muda": {journal.Event{Src: "git-hook", Hook: "post-checkout",
			CWD: "/var/folders/92/ab/T/copier._vcs.clone.k3j2", Repo: "schuettc/muda"}, true},
		"push to a local bare repo under /tmp (relative)": {pushHook("/tmp/cadtest/clone", "../remote.git"), true},
		"push to a local bare repo under /tmp (absolute)": {pushHook("/tmp/cadtest/other", "/tmp/cadtest/remote.git"), true},
		"push to a file:// remote":                        {pushHook("/var/folders/92/ab/T/tmp.AbC/fork", "file:///var/folders/92/ab/T/tmp.AbC/origin.git"), true},
		"push to a local path outside temp":               {pushHook("/tmp/x", "/Users/c/remote.git"), true},
		"agent pushes a temp clone, remote unknown": {journal.Event{Src: "pi", CWD: "/Users/c",
			Actions: []journal.Action{{Tool: "git", Verb: "push", Dir: "/tmp/gone", Refs: []string{"origin"}}}}, true},
		"agent commits in a temp clone annotated with a repo": {journal.Event{Src: "pi", CWD: "/Users/c",
			Actions: []journal.Action{{Tool: "git", Verb: "commit", Dir: "/tmp/gone", Repo: "schuettc/muda"}}}, true},
		"agent pushes a temp clone to a local path": {journal.Event{Src: "claude", CWD: "/tmp/x",
			Actions: []journal.Action{{Tool: "git", Verb: "push", Refs: []string{"../remote.git"}}}}, true},
		"a temp git dir outside every root": {journal.Event{Src: "git-hook", Hook: "post-commit", CWD: "/tmp/x", GitDir: "/tmp/x/.git"}, true},
	} {
		if got := m.Event(tc.ev); got != tc.want {
			t.Errorf("%s: Event = %v, want %v", name, got, tc.want)
		}
	}
}

func TestLocalRemote(t *testing.T) {
	for url, want := range map[string]bool{
		"https://github.com/a/b.git":     false,
		"git@github.com:a/b.git":         false,
		"github.com:a/b.git":             false,
		"ssh://git@github.com/a/b":       false,
		"git://example.com/a.git":        false,
		"file:///tmp/remote.git":         true,
		"/tmp/remote.git":                true,
		"../remote.git":                  true,
		"./remote.git":                   true,
		"remote.git":                     true,
		"sub/dir:with-colon.git":         true, // a slash before the colon: a path, as git reads it
		"/private/var/folders/x/T/o.git": true,
	} {
		if got := localRemote(url); got != want {
			t.Errorf("localRemote(%q) = %v, want %v", url, got, want)
		}
	}
}

// TestEventReadsTheWorkingTree: when the event names no git dir (a hook git
// ran without GIT_DIR, an agent's -C dir), a temp working tree that still
// exists is looked at: a linked worktree whose common dir is in a configured
// root is kept, and an agent's push goes where the clone's config says.
func TestEventReadsTheWorkingTree(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	tmp, keep := filepath.Join(base, "tmp"), filepath.Join(base, "GitHub")
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A clone in the root with a linked worktree in temp (git's layout:
	// <wt>/.git names <clone>/.git/worktrees/<wt>, whose commondir is ../..).
	clone := filepath.Join(keep, "hail")
	write(filepath.Join(clone, ".git", "config"), "[core]\n\tbare = false\n[remote \"origin\"]\n\turl = git@github.com:schuettc/hail.git\n")
	wtGit := filepath.Join(clone, ".git", "worktrees", "wt")
	write(filepath.Join(wtGit, "commondir"), "../..\n")
	wt := filepath.Join(tmp, "wt")
	write(filepath.Join(wt, ".git"), "gitdir: "+wtGit+"\n")
	if err := os.MkdirAll(filepath.Join(wt, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A git dir moved out of the root whose common dir is still in it.
	moved := filepath.Join(tmp, "moved-gitdir")
	write(filepath.Join(moved, "commondir"), filepath.Join(clone, ".git")+"\n")
	// A scratch clone whose origin is GitHub, one whose origin is a local
	// bare repo, and one whose push url overrides a GitHub url.
	gh, local, pushurl := filepath.Join(tmp, "gh"), filepath.Join(tmp, "local"), filepath.Join(tmp, "pushurl")
	write(filepath.Join(gh, ".git", "config"), "[remote \"origin\"]\n  url = https://github.com/schuettc/pi-usage.git\n  fetch = +refs/heads/*:refs/remotes/origin/*\n")
	write(filepath.Join(local, ".git", "config"), "[remote \"origin\"]\n\turl = ../remote.git\n[remote \"gh\"]\n\turl = git@github.com:a/b.git\n")
	write(filepath.Join(pushurl, ".git", "config"), "[remote \"origin\"]\n\turl = git@github.com:a/b.git\n\tpushurl = /tmp/nowhere.git\n")

	m := newWith([]string{tmp}, []string{keep})
	push := func(dir string, refs ...string) journal.Event {
		return journal.Event{Src: "pi", CWD: base, Actions: []journal.Action{{Tool: "git", Verb: "push", Dir: dir, Refs: refs}}}
	}
	for name, tc := range map[string]struct {
		ev   journal.Event
		want bool
	}{
		"hook in a rooted worktree, no GIT_DIR":        {journal.Event{Src: "git-hook", Hook: "post-commit", CWD: filepath.Join(wt, "sub")}, false},
		"hook with a moved git dir, common dir kept":   {journal.Event{Src: "git-hook", Hook: "post-commit", CWD: filepath.Join(tmp, "elsewhere"), GitDir: moved}, false},
		"agent commits in a rooted worktree":           {journal.Event{Src: "pi", CWD: base, Actions: []journal.Action{{Tool: "git", Verb: "commit", Dir: wt}}}, false},
		"agent pushes a scratch clone to github":       {push(gh, "origin", "main"), false},
		"agent pushes with no remote named":            {push(gh), false},
		"agent pushes a scratch clone to a local bare": {push(local, "origin", "main"), true},
		"agent pushes the same clone to github":        {push(local, "gh", "main"), false},
		"agent's push url is local":                    {push(pushurl, "origin"), true},
		"hook in a scratch clone":                      {journal.Event{Src: "git-hook", Hook: "post-commit", CWD: gh}, true},
	} {
		if got := m.Event(tc.ev); got != tc.want {
			t.Errorf("%s: Event = %v, want %v", name, got, tc.want)
		}
	}
}

// TestEventKeepsAWorktreeAddOfATrackedClone: `git worktree add /tmp/x` runs
// post-checkout in the new worktree (old head all zeros, branch flag 1)
// without GIT_DIR. Once the worktree is gone, the only evidence it belonged
// to a tracked clone is the repo sync resolved for it from this machine's
// snapshot: that keeps it. No other hook line is made real by its repo.
func TestEventKeepsAWorktreeAddOfATrackedClone(t *testing.T) {
	m := newWith([]string{"/tmp", "/private/tmp", "/var/folders", "/private/var/folders"}, []string{"/Users/c/GitHub"})
	zero := "0000000000000000000000000000000000000000"
	add := func(cwd, gitDir, repo string, args ...string) journal.Event {
		return journal.Event{Src: "git-hook", Hook: "post-checkout", Args: args, CWD: cwd, GitDir: gitDir, Repo: repo}
	}
	for name, tc := range map[string]struct {
		ev   journal.Event
		want bool
	}{
		"worktree add of a tracked clone":       {add("/private/tmp/muda-plan", "", "schuettc/tools-ops", zero, "5e02487e3f31e546b95d60022f9f57e1939ff331", "1"), false},
		"worktree add, sha256 repo":             {add("/tmp/wt", "", "schuettc/hail", strings.Repeat("0", 64), strings.Repeat("a", 64), "1"), false},
		"same shape, no repo (a scratch clone)": {add("/tmp/scratch", "", "", zero, "5e02487e3f31e546b95d60022f9f57e1939ff331", "1"), true},
		"copier clone's checkout, temp git dir": {add("/Users/c/GitHub/muda", "/private/var/folders/92/ab/T/copier._vcs.clone.k3j2/.git", "schuettc/muda", zero, "5e02487e3f31e546b95d60022f9f57e1939ff331", "1"), true},
		"a later checkout in the worktree":      {add("/tmp/wt", "", "schuettc/hail", "5e02487e3f31e546b95d60022f9f57e1939ff331", "9dafd0301faad79cf6a1974d92af424189476b5d", "1"), true},
		"a file checkout (flag 0)":              {add("/tmp/wt", "", "schuettc/hail", zero, "5e02487e3f31e546b95d60022f9f57e1939ff331", "0"), true},
		"a post-commit with a repo":             {journal.Event{Src: "git-hook", Hook: "post-commit", CWD: "/tmp/wt", Repo: "schuettc/hail"}, true},
		"a reference-transaction with a repo":   {journal.Event{Src: "git-hook", Hook: "reference-transaction", Args: []string{"committed"}, CWD: "/tmp/wt", Repo: "schuettc/hail"}, true},
	} {
		if got := m.Event(tc.ev); got != tc.want {
			t.Errorf("%s: Event = %v, want %v", name, got, tc.want)
		}
	}
}
