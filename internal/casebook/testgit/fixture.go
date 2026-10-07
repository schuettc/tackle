package testgit

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// Fixture gives the test its own fresh copy of a directory tree that build
// makes once per test binary, and returns the copy's path. The tree is
// built and copied at the same path every time, so absolute paths it bakes
// in (config files, committed snapshots, remote URLs) stay true without
// rewriting git objects. The copy is the test's alone: nothing in it
// survives the test, and the next test gets a fresh copy of the pristine
// tree. A test (or subtest) holding a copy of name while asking for another
// gets a second slot, built once the same way.
//
// build runs inside the first test that asks, with dir empty; anything it
// needs from the environment (HOME and the like, derived from dir) the
// caller sets again after Fixture returns, since a copied test skips build.
func Fixture(t testing.TB, name string, build func(dir string)) string {
	t.Helper()
	fixMu.Lock()
	if fixBase == "" {
		removeStaleFixtures()
		b, err := os.MkdirTemp("", fixPrefix+strconv.Itoa(os.Getpid())+"-")
		if err != nil {
			fixMu.Unlock()
			t.Fatal(err)
		}
		fixBase = b
	}
	n := 0
	for fixBusy[name+"/"+strconv.Itoa(n)] {
		n++
	}
	slot := name + "/" + strconv.Itoa(n)
	fixBusy[slot] = true
	built := fixBuilt[slot]
	fixMu.Unlock()

	slotDir := filepath.Join(fixBase, name, "slot-"+strconv.Itoa(n))
	live, tpl := filepath.Join(slotDir, "live"), filepath.Join(slotDir, "tpl")
	t.Cleanup(func() {
		if err := os.RemoveAll(live); err != nil {
			t.Errorf("testgit.Fixture RemoveAll cleanup: %v", err)
		}
		fixMu.Lock()
		delete(fixBusy, slot)
		fixMu.Unlock()
	})
	if err := os.RemoveAll(live); err != nil {
		t.Fatal(err)
	}
	if built {
		if err := copyTree(tpl, live); err != nil {
			t.Fatal(err)
		}
		return live
	}
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	build(live)
	if t.Failed() {
		return live
	}
	if err := os.RemoveAll(tpl); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(live, tpl); err != nil {
		t.Fatal(err)
	}
	fixMu.Lock()
	fixBuilt[slot] = true
	fixMu.Unlock()
	return live
}

const fixPrefix = "casebook-testgit-fixture-"

var (
	fixMu    sync.Mutex
	fixBase  string // this process's fixtures; left behind at exit, removed by a later run
	fixBusy  = map[string]bool{}
	fixBuilt = map[string]bool{}
)

// removeStaleFixtures removes fixture dirs left by test binaries that have
// exited (a test binary has no hook at exit to remove its own).
func removeStaleFixtures() {
	dirs, _ := filepath.Glob(filepath.Join(os.TempDir(), fixPrefix+"*"))
	for _, d := range dirs {
		pid, err := strconv.Atoi(strings.SplitN(strings.TrimPrefix(filepath.Base(d), fixPrefix), "-", 2)[0])
		if err != nil || pid == os.Getpid() {
			continue
		}
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			_ = os.RemoveAll(d)
		}
	}
}

// copyTree copies src to dst (which must not exist), keeping modes and
// modification times.
func copyTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	from, err := os.OpenRoot(src)
	if err != nil {
		return err
	}
	defer func() { _ = from.Close() }()
	to, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer func() { _ = to.Close() }()
	type stamp struct {
		path string
		info fs.FileInfo
	}
	var dirs []stamp
	err = fs.WalkDir(from.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			dirs = append(dirs, stamp{p, info})
			return to.MkdirAll(p, info.Mode().Perm()|0o700)
		case d.Type()&fs.ModeSymlink != 0:
			l, err := from.Readlink(p)
			if err != nil {
				return err
			}
			return to.Symlink(l, p)
		default:
			b, err := from.ReadFile(p)
			if err != nil {
				return err
			}
			if err := to.WriteFile(p, b, info.Mode().Perm()); err != nil {
				return err
			}
			return to.Chtimes(p, info.ModTime(), info.ModTime())
		}
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := to.Chmod(dirs[i].path, dirs[i].info.Mode().Perm()); err != nil {
			return err
		}
		if err := to.Chtimes(dirs[i].path, dirs[i].info.ModTime(), dirs[i].info.ModTime()); err != nil {
			return err
		}
	}
	return nil
}
