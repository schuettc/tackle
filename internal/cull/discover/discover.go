// Package discover finds test files for cull: either a whole-suite walk of
// a project tree, or the tests touched by a git diff against a base
// revision (working tree vs base, including staged, unstaged,
// committed-since-base, and untracked changes).
package discover

import (
	"bytes"
	"fmt"
	"io/fs"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/schuettc/tackle/internal/cull/extract"
)

// skipDirs are directory names Suite never descends into.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"testdata":     true,
	".worktrees":   true,
}

// Root returns the git toplevel of path, or the absolute path itself if
// path is not inside a git repository.
func Root(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = abs
	out, err := cmd.Output()
	if err != nil {
		return abs, nil //nolint:nilerr // not a git repository: the path itself is the root
	}
	return strings.TrimSpace(string(out)), nil
}

// Suite walks root/sub and returns the project-root-relative, '/'-separated
// paths of every file matched by a registered extractor. It skips .git,
// node_modules, vendor, testdata, .worktrees directories anywhere in the
// tree, and any file whose relpath matches one of the exclude globs
// (doublestar-style ** supported).
func Suite(root, sub string, exclude []string) ([]string, error) {
	start := filepath.Join(root, sub)
	var out []string
	err := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != start && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if Keep(sub, exclude, rel) {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// Keep is the one path filter Suite and Diff share, so --diff never
// selects a file suite mode wouldn't: rel (project-root-relative,
// '/'-separated) must be under sub (root-relative; "" is the whole
// project), have no skipDirs directory below sub, match no exclude glob,
// and be a test file some registered extractor handles.
func Keep(sub string, exclude []string, rel string) bool {
	sub = strings.Trim(filepath.ToSlash(sub), "/")
	below := rel
	if sub != "" {
		if !strings.HasPrefix(rel, sub+"/") {
			return false
		}
		below = strings.TrimPrefix(rel, sub+"/")
	}
	parts := strings.Split(below, "/")
	for _, part := range parts[:len(parts)-1] {
		if skipDirs[part] {
			return false
		}
	}
	for _, pat := range exclude {
		if globMatch(pat, rel) {
			return false
		}
	}
	return extract.ForFile(rel) != nil
}

// globMatch reports whether name matches a doublestar-style glob pattern:
// "**" matches zero or more whole path segments, other segments use
// path.Match semantics (*, ?, [...]) and never cross a '/'.
func globMatch(pattern, name string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		if matchSegments(pat[1:], name) {
			return true
		}
		if len(name) == 0 {
			return false
		}
		return matchSegments(pat, name[1:])
	}
	if len(name) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], name[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pat[1:], name[1:])
}

// Changes maps a project-root-relative relpath to its changed line ranges
// (1-based, inclusive [from,to] pairs) in the working tree. A nil slice
// value means the whole file is new (untracked).
type Changes map[string][][2]int

// Touches reports whether [startLine, endLine] overlaps any changed range
// recorded for relpath, or relpath is a whole new file.
func (c Changes) Touches(relpath string, startLine, endLine int) bool {
	ranges, ok := c[relpath]
	if !ok {
		return false
	}
	if ranges == nil {
		return true
	}
	for _, r := range ranges {
		if r[0] <= endLine && r[1] >= startLine {
			return true
		}
	}
	return false
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// Diff runs `git diff -U0 base --` in root (working tree vs base) and
// returns the changed line ranges per file, plus untracked test files as
// whole-file additions. A base that doesn't exist, or root not being a git
// repository, is reported as an error naming base and including git's
// message. Only files passing Keep(sub, exclude, ·) are returned.
// Prefixes, external diff and relative mode are pinned on the
// command line so user git config (diff.mnemonicPrefix, diff.noprefix,
// diff.external, diff.relative) can't change the paths parsed here.
func Diff(root, base, sub string, exclude []string) (Changes, error) {
	cmd := exec.Command("git", "-c", "core.quotePath=false", "diff", "--no-color", "--no-ext-diff", "--no-relative", "--src-prefix=a/", "--dst-prefix=b/", "-U0", base, "--")
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git diff base %q: %w: %s", base, err, strings.TrimSpace(stderr.String()))
	}

	changes := Changes{}
	var current string
	for _, line := range strings.Split(stdout.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			p := strings.TrimPrefix(line, "+++ ")
			if p == "/dev/null" {
				current = ""
				continue
			}
			p = unquotePath(p)
			current = strings.TrimPrefix(p, "b/")
			if !Keep(sub, exclude, current) {
				current = ""
			}
		case strings.HasPrefix(line, "@@ "):
			if current == "" {
				continue
			}
			m := hunkHeader.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			c, _ := strconv.Atoi(m[1])
			d := 1
			if m[2] != "" {
				d, _ = strconv.Atoi(m[2])
			}
			var r [2]int
			if d == 0 {
				// Pure deletion: nothing was added at c, but a test
				// enclosing line c had content removed, so record c.
				r = [2]int{c, c}
			} else {
				r = [2]int{c, c + d - 1}
			}
			changes[current] = append(changes[current], r)
		}
	}

	untracked, err := untrackedFiles(root)
	if err != nil {
		return nil, err
	}
	for _, rel := range untracked {
		if Keep(sub, exclude, rel) {
			changes[rel] = nil
		}
	}

	return changes, nil
}

// untrackedFiles lists untracked, non-ignored files in root, as
// '/'-separated relpaths.
func untrackedFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "-c", "core.quotePath=false", "ls-files", "-z", "--others", "--exclude-standard")
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git ls-files: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var out []string
	for _, line := range strings.Split(stdout.String(), "\x00") {
		if line == "" {
			continue
		}
		out = append(out, filepath.ToSlash(line))
	}
	return out, nil
}

// unquotePath undoes git's C-style quoting of a path (used as a fallback
// for filenames containing characters, like a literal quote or newline,
// that git quotes even with core.quotePath=false).
func unquotePath(p string) string {
	if len(p) < 2 || p[0] != '"' || p[len(p)-1] != '"' {
		return p
	}
	unquoted, err := strconv.Unquote(p)
	if err != nil {
		return p
	}
	return unquoted
}
