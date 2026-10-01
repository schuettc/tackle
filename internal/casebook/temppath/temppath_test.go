package temppath

import (
	"os"
	"path/filepath"
	"slices"
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
