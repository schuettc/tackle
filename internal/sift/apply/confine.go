package apply

// Confinement: every path apply writes (a file's repo path) is checked by
// RepoPath, and every write goes
// through an os.Root on the worktree, one directory at a time: each
// component is Lstat'd and refused when it is a symlink, then opened as a
// root of its own and checked to be the directory that was Lstat'd. The
// descriptors carry the walk, so a parent swapped after its check can't
// redirect a write, and nothing resolves into .git or out of the tree.

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// afterCheck, when set (tests), runs after each directory is checked and
// before it is opened: where a swap would have to land.
var afterCheck func(dir string)

// RepoPath checks that p names a file inside a repo and returns it clean and
// slash-separated: relative, no "..", nothing under .git.
func RepoPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("no path")
	}
	s := filepath.ToSlash(p)
	if path.IsAbs(s) || filepath.IsAbs(p) {
		return "", fmt.Errorf("%s is outside the repo: a repo path is relative to the repo", p)
	}
	for _, part := range strings.Split(s, "/") {
		switch {
		case part == "..":
			return "", fmt.Errorf("%s is outside the repo (it climbs out with ..)", p)
		case strings.EqualFold(part, ".git"):
			return "", fmt.Errorf("%s is under .git: apply writes only the repo's own files", p)
		}
	}
	c := path.Clean(s)
	if c == "." {
		return "", fmt.Errorf("%q names the repo itself, not a file in it", p)
	}
	return c, nil
}

// parentDir opens the directory holding rel (clean, slash-separated,
// relative) under root, one component at a time, making missing ones when
// mk (else a missing one is fs.ErrNotExist). A component that is a symlink,
// or is not the directory its Lstat saw by the time it is opened, is
// refused. It returns the directory, the leaf's name, and a close for the
// directory (root itself is never closed).
//
// It assumes no other process modifies the worktree while apply runs: a
// directory renamed elsewhere in the tree after it is opened is still
// written through.
func parentDir(root *os.Root, rel string, mk bool) (*os.Root, string, func(), error) {
	parts := strings.Split(rel, "/")
	cur, done := root, func() {}
	fail := func(err error) (*os.Root, string, func(), error) {
		done()
		return nil, "", func() {}, err
	}
	for i, name := range parts[:len(parts)-1] {
		at := strings.Join(parts[:i+1], "/")
		fi, err := cur.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) && mk {
			if err = cur.Mkdir(name, 0o755); err == nil || errors.Is(err, fs.ErrExist) {
				fi, err = cur.Lstat(name)
			}
		}
		switch {
		case err != nil:
			return fail(err)
		case fi.Mode()&os.ModeSymlink != 0:
			return fail(fmt.Errorf("%s is a symlink: apply does not write through one", at))
		case !fi.IsDir():
			return fail(fmt.Errorf("%s is not a directory", at))
		}
		if afterCheck != nil {
			afterCheck(filepath.Join(cur.Name(), name))
		}
		next, err := cur.OpenRoot(name)
		if err != nil {
			return fail(fmt.Errorf("%s: %w", at, err))
		}
		if now, err := next.Stat("."); err != nil || !os.SameFile(fi, now) {
			_ = next.Close()
			return fail(fmt.Errorf("%s changed while it was being written (replaced by a symlink?)", at))
		}
		done()
		cur, done = next, func() { _ = next.Close() }
	}
	return cur, parts[len(parts)-1], done, nil
}

// leafInfo is rel's leaf in dir, Lstat'd: nil when there is none; a symlink
// is refused.
func leafInfo(dir *os.Root, leaf, rel string) (fs.FileInfo, error) {
	fi, err := dir.Lstat(leaf)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, err
	case fi.Mode()&os.ModeSymlink != 0:
		return nil, fmt.Errorf("%s is a symlink: apply does not write through one", rel)
	case !fi.Mode().IsRegular():
		return nil, fmt.Errorf("%s is not a regular file", rel)
	}
	return fi, nil
}

// openSeen opens the leaf with flag and checks it is the file Lstat saw
// (fi): a leaf swapped for a symlink since is refused before it is read.
func openSeen(dir *os.Root, leaf, rel string, flag int, fi fs.FileInfo) (*os.File, error) {
	f, err := dir.OpenFile(leaf, flag, 0)
	if err != nil {
		return nil, err
	}
	if now, err := f.Stat(); err != nil || !os.SameFile(fi, now) {
		_ = f.Close()
		return nil, fmt.Errorf("%s changed while it was being opened (replaced by a symlink?)", rel)
	}
	return f, nil
}

// WriteFile writes content to rel (checked by RepoPath) under root. It
// never writes into an existing inode: the content goes to a new temporary
// file beside the leaf, made with O_EXCL (which never follows a symlink),
// synced, given the old file's mode, then renamed over the leaf through the
// same directory. A leaf hard-linked to a file outside the worktree is
// replaced, and the outside file keeps its bytes.
func WriteFile(root *os.Root, rel, content string) error {
	clean, err := RepoPath(rel)
	if err != nil {
		return err
	}
	dir, leaf, done, err := parentDir(root, clean, true)
	if err != nil {
		return err
	}
	defer done()
	fi, err := leafInfo(dir, leaf, clean)
	if err != nil {
		return err
	}
	tmp, f, err := createTemp(dir, leaf)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		_ = f.Close()
		_ = dir.Remove(tmp)
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		return fail(err)
	}
	if fi != nil {
		if err := f.Chmod(fi.Mode().Perm()); err != nil {
			return fail(err)
		}
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = dir.Remove(tmp)
		return err
	}
	if err := dir.Rename(tmp, leaf); err != nil {
		_ = dir.Remove(tmp)
		return err
	}
	return nil
}

// createTemp makes a new file beside leaf in dir with O_EXCL, under a
// random name, and returns the name and the open file.
func createTemp(dir *os.Root, leaf string) (string, *os.File, error) {
	for range 10 {
		name := "." + leaf + "-" + rand.Text() + ".tmp"
		f, err := dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return name, f, err
	}
	return "", nil, fmt.Errorf("no free temporary name beside %s", leaf)
}

// ReadNoLink reads up to n bytes of rel under root by the same walk: no
// component may be a symlink, and the file opened must be the one Lstat
// saw. rel is clean, relative and slash-separated.
func ReadNoLink(root *os.Root, rel string, n int64) ([]byte, error) {
	if rel == "" || path.IsAbs(rel) || path.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, "../") {
		return nil, fmt.Errorf("%s is not a clean relative path", rel)
	}
	dir, leaf, done, err := parentDir(root, rel, false)
	if err != nil {
		return nil, err
	}
	defer done()
	fi, err := leafInfo(dir, leaf, rel)
	if err != nil {
		return nil, err
	}
	if fi == nil {
		return nil, fmt.Errorf("%s: %w", rel, fs.ErrNotExist)
	}
	f, err := openSeen(dir, leaf, rel, os.O_RDONLY, fi)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, n))
}
