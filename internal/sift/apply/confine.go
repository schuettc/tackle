package apply

// Confinement: every path apply reads or writes (a row's source, a merge
// target, a move's destination) is checked by RepoPath, and every write goes
// through writeInside or removeInside, which resolve each parent and refuse
// anything that leaves the worktree or writes through a symlink.

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

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

// inside resolves dir (which exists) and reports an error unless it is root
// or under it; root is already resolved.
func inside(root, dir string) error {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%s resolves to %s, outside the worktree (a symlink)", dir, real)
	}
	return nil
}

// parent makes rel's parent directories under root one at a time (mk), or
// finds them (a missing one is os.ErrNotExist), checking each resolves
// inside root before anything is made in it, and returns the parent's path.
func parent(root, rel string, mk bool) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	dir := root
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		dir = filepath.Join(dir, part)
		if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
			if !mk {
				return "", err
			}
			if err := os.Mkdir(dir, 0o755); err != nil {
				return "", err
			}
		} else if err != nil {
			return "", err
		}
		if err := inside(realRoot, dir); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// leaf checks rel under root for a write: its parents resolve inside root
// and it is not a symlink. It returns the path to open.
func leaf(root, rel string, mk bool) (string, error) {
	clean, err := RepoPath(rel)
	if err != nil {
		return "", err
	}
	dir, err := parent(root, clean, mk)
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, path.Base(clean))
	if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is a symlink: apply does not write through one", clean)
	}
	return p, nil
}

// writeInside writes content to rel under root, refusing to leave root or
// to follow a symlink at the leaf (O_NOFOLLOW closes the race).
func writeInside(root, rel, content string) error {
	p, err := leaf(root, rel, true)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// removeInside removes rel under root (gone already is fine), with the same
// refusals as writeInside.
func removeInside(root, rel string) error {
	p, err := leaf(root, rel, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
