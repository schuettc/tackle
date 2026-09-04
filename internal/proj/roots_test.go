package proj

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRoots(t *testing.T, body string) (home string) {
	t.Helper()
	home = t.TempDir()
	cfg := filepath.Join(home, ".config", "proj")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "roots"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

func TestParseRootsAndLookups(t *testing.T) {
	home := writeRoots(t, "# comment\n\n~/code\nproject:~/dotfiles\n")
	// create the dirs so they count
	mustMkdir(t, filepath.Join(home, "code", "alpha"))
	mustMkdir(t, filepath.Join(home, "code", "beta"))
	mustMkdir(t, filepath.Join(home, "dotfiles"))

	r, err := LoadRoots()
	if err != nil {
		t.Fatalf("LoadRoots: %v", err)
	}

	if got, ok := r.NameForDir(filepath.Join(home, "code", "alpha", "sub")); !ok || got != "alpha" {
		t.Fatalf("NameForDir(alpha/sub) = %q,%v", got, ok)
	}
	if got, ok := r.NameForDir(filepath.Join(home, "dotfiles")); !ok || got != "dotfiles" {
		t.Fatalf("NameForDir(dotfiles) = %q,%v", got, ok)
	}
	if got, ok := r.DirForName("beta"); !ok || got != filepath.Join(home, "code", "beta") {
		t.Fatalf("DirForName(beta) = %q,%v", got, ok)
	}
	dirs := r.AllProjectDirs()
	if !contains(dirs, filepath.Join(home, "code", "alpha")) ||
		!contains(dirs, filepath.Join(home, "dotfiles")) {
		t.Fatalf("AllProjectDirs missing entries: %v", dirs)
	}
}

func TestLoadRootsMissingFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "x"))
	if _, err := LoadRoots(); err != ErrNoRoots {
		t.Fatalf("want ErrNoRoots, got %v", err)
	}
}

func TestAddRoot(t *testing.T) {
	home := writeRoots(t, "~/code\n")
	mustMkdir(t, filepath.Join(home, "code", "alpha"))
	mustMkdir(t, filepath.Join(home, "more", "beta"))

	// non-directory input is rejected and the file is untouched.
	if err := AddRoot(filepath.Join(home, "nope")); err == nil {
		t.Fatal("AddRoot(nonexistent) should error")
	}

	if err := AddRoot("~/more"); err != nil {
		t.Fatalf("AddRoot(~/more): %v", err)
	}
	// the raw ~ form is preserved in the file.
	data, _ := os.ReadFile(RootsPath())
	if !contains(strings.Split(string(data), "\n"), "~/more") {
		t.Fatalf("roots file missing ~/more:\n%s", data)
	}

	// adding the same dir again (different spelling) is a no-op.
	if err := AddRoot(filepath.Join(home, "more")); err != nil {
		t.Fatalf("AddRoot(dup): %v", err)
	}
	r, err := LoadRoots()
	if err != nil {
		t.Fatalf("LoadRoots: %v", err)
	}
	dirs := r.AllProjectDirs()
	if !contains(dirs, filepath.Join(home, "code", "alpha")) ||
		!contains(dirs, filepath.Join(home, "more", "beta")) {
		t.Fatalf("AllProjectDirs after AddRoot: %v", dirs)
	}
	n := 0
	for _, root := range r.Roots {
		if root == filepath.Join(home, "more") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("~/more appears %d times, want 1", n)
	}
}

func TestEnsureRootsFileCreates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "cfg"))
	p, err := EnsureRootsFile()
	if err != nil {
		t.Fatalf("EnsureRootsFile: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("roots file not created: %v", err)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
