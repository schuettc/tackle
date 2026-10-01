// Package temppath recognizes temp folders, whose git activity casebook never
// journals: scratch clones, test and probe homes, copier and pytest checkouts
// come and go by the thousand and say nothing about the fate of a real repo.
//
// A path is temp when it is under one of the temp roots (DefaultRoots) and
// not under one of this machine's configured scan roots: a root a user put
// in a temp folder on purpose (tests and probes do) is tracked as usual.
package temppath

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/schuettc/tackle/internal/casebook/journal"
)

// DefaultRoots are the temp roots: os.TempDir(), $TMPDIR, /tmp, /private/tmp,
// /var/folders and /private/var/folders, each also by its real path.
func DefaultRoots() []string {
	return expand([]string{os.TempDir(), os.Getenv("TMPDIR"), "/tmp", "/private/tmp", "/var/folders", "/private/var/folders"})
}

// expand cleans roots and adds each one's symlink-resolved path, dropping
// empty, relative and "/" entries and duplicates.
func expand(roots []string) []string {
	var out []string
	add := func(p string) {
		if p == "" || !filepath.IsAbs(p) {
			return
		}
		p = filepath.Clean(p)
		if p == "/" {
			return
		}
		for _, o := range out {
			if o == p {
				return
			}
		}
		out = append(out, p)
	}
	for _, r := range roots {
		add(r)
		if r != "" {
			if real, err := filepath.EvalSymlinks(r); err == nil {
				add(real)
			}
		}
	}
	return out
}

// Matcher answers whether a path is in a temp folder. Its answers are cached;
// it is safe for concurrent use.
type Matcher struct {
	temp, keep []string
	mu         sync.Mutex
	cache      map[string]bool
}

// New returns a Matcher over DefaultRoots. keep are this machine's configured
// scan roots: a path under one of them is never temp.
func New(keep []string) *Matcher { return newWith(DefaultRoots(), keep) }

func newWith(temp, keep []string) *Matcher {
	return &Matcher{temp: expand(temp), keep: expand(keep), cache: map[string]bool{}}
}

// under reports whether p is root or inside it, by whole path segments.
func under(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+"/")
}

func anyUnder(p string, roots []string) bool {
	for _, r := range roots {
		if under(p, r) {
			return true
		}
	}
	return false
}

// Path reports whether the absolute path p is under a temp root and outside
// every keep root, as written or with its symlinks resolved (so /tmp/x and
// /private/tmp/x agree). A relative or empty path is never temp.
func (m *Matcher) Path(p string) bool {
	if p == "" || !filepath.IsAbs(p) {
		return false
	}
	p = filepath.Clean(p)
	m.mu.Lock()
	v, ok := m.cache[p]
	m.mu.Unlock()
	if ok {
		return v
	}
	v = m.path(p)
	m.mu.Lock()
	m.cache[p] = v
	m.mu.Unlock()
	return v
}

func (m *Matcher) path(p string) bool {
	if anyUnder(p, m.keep) {
		return false
	}
	if anyUnder(p, m.temp) {
		return true
	}
	if real, err := filepath.EvalSymlinks(p); err == nil && real != p {
		return anyUnder(real, m.temp) && !anyUnder(real, m.keep)
	}
	return false
}

// Event reports whether ev happened in a temp folder:
//   - its git dir is temp (a probe's git run from a real working directory);
//   - a git hook: its working directory is temp;
//   - an agent's command: every action ran in a temp folder (its `-C` dir,
//     resolved against the working directory, or the working directory). A
//     gh action naming its repo (`-R`) without a dir is about that repo
//     wherever it ran, so it is never temp.
func (m *Matcher) Event(ev journal.Event) bool {
	if m.Path(ev.GitDir) {
		return true
	}
	if len(ev.Actions) == 0 {
		return m.Path(ev.CWD)
	}
	for _, a := range ev.Actions {
		if a.Tool == "gh" && a.Repo != "" && a.Dir == "" {
			return false
		}
		dir := ev.CWD
		if a.Dir != "" {
			dir = a.Dir
			if !filepath.IsAbs(dir) && ev.CWD != "" {
				dir = filepath.Join(ev.CWD, dir)
			}
		}
		if !m.Path(dir) {
			return false
		}
	}
	return true
}

// Where returns the temp path that makes ev temp (its git dir, working
// directory or an action's dir), or "" when ev is not temp.
func (m *Matcher) Where(ev journal.Event) string {
	if !m.Event(ev) {
		return ""
	}
	if m.Path(ev.GitDir) {
		return filepath.Clean(ev.GitDir)
	}
	if m.Path(ev.CWD) {
		return filepath.Clean(ev.CWD)
	}
	for _, a := range ev.Actions {
		dir := a.Dir
		if dir != "" && !filepath.IsAbs(dir) {
			dir = filepath.Join(ev.CWD, dir)
		}
		if m.Path(dir) {
			return filepath.Clean(dir)
		}
	}
	return ""
}
