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
	strs       map[string]string // gitDirOf, rooted and remoteURL answers
}

// New returns a Matcher over DefaultRoots. keep are this machine's configured
// scan roots: a path under one of them is never temp.
func New(keep []string) *Matcher { return newWith(DefaultRoots(), keep) }

func newWith(temp, keep []string) *Matcher {
	return &Matcher{temp: expand(temp), keep: expand(keep), cache: map[string]bool{}, strs: map[string]string{}}
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

// Event reports whether ev is git activity in a temp folder, which casebook
// does not journal. Something happened in a temp folder when its git dir is
// temp, a hook's working directory is temp, or every one of an agent's
// actions ran in a temp folder (its `-C` dir, resolved against the working
// directory, or the working directory).
//
// A temp folder doing real work is not temp, whichever repo its line was
// annotated with (a copier clone of schuettc/muda's template is still noise):
//
//  1. Its git dir, or for a linked worktree the common dir, resolves inside
//     a configured root: a worktree of a tracked clone placed in /tmp. With
//     no git dir recorded, a working tree that still exists is looked at
//     (its .git file names the git dir).
//  2. It pushes to a remote that isn't local (localRemote): a pre-push hook's
//     remote URL, or an agent's `git push` whose remote is a URL, or a remote
//     the clone's config names. When that clone is gone, a push action that
//     sync annotated with a repo counts: sync takes an action's repo only
//     from this machine's snapshot, so its dir was a worktree of a tracked
//     clone. A gh action naming its repo (`-R`) without a dir is likewise
//     about that repo wherever it ran.
//
// The one hook line whose repo counts is a `git worktree add`'s
// post-checkout recorded without a git dir in a temp folder (worktreeAdd):
// sync resolved that repo from a worktree of a tracked clone (rule 1, after
// the worktree is gone). A copier or scratch clone's checkout carries no
// such repo, and no other hook's repo makes it real.
func (m *Matcher) Event(ev journal.Event) bool {
	if len(ev.Actions) == 0 {
		return m.hookTemp(ev)
	}
	for _, a := range ev.Actions {
		if !m.actionTemp(ev.CWD, a) {
			return false
		}
	}
	return true
}

func (m *Matcher) hookTemp(ev journal.Event) bool {
	if !m.Path(ev.GitDir) && !m.Path(ev.CWD) {
		return false
	}
	gitDir := ev.GitDir
	if gitDir == "" {
		gitDir = m.gitDirOf(ev.CWD)
	}
	if m.rooted(gitDir) {
		return false
	}
	if ev.Hook == "pre-push" && len(ev.Args) > 1 && !localRemote(ev.Args[1]) {
		return false
	}
	if ev.GitDir == "" && ev.Repo != "" && worktreeAdd(ev) {
		// Rule 1 for a worktree that is gone: with no git dir and a temp
		// working directory, sync could only take the line's repo from a
		// worktree of a clone in this machine's snapshot. Only this hook
		// counts; any other line's repo says nothing (see Event).
		return false
	}
	return true
}

// worktreeAdd reports whether ev is the post-checkout `git worktree add`
// runs in the new worktree: the old head all zeros, a branch checkout.
func worktreeAdd(ev journal.Event) bool {
	return ev.Hook == "post-checkout" && len(ev.Args) == 3 && ev.Args[0] != "" &&
		strings.Trim(ev.Args[0], "0") == "" && ev.Args[2] == "1"
}

func (m *Matcher) actionTemp(cwd string, a journal.Action) bool {
	if a.Tool == "gh" && a.Repo != "" && a.Dir == "" {
		return false
	}
	dir := actionDir(cwd, a)
	if !m.Path(dir) {
		return false
	}
	if m.rooted(m.gitDirOf(dir)) {
		return false
	}
	if a.Tool == "git" && a.Verb == "push" && m.pushesAway(dir, a) {
		return false
	}
	return true
}

// actionDir is where an agent's action ran: its -C dir, resolved against
// cwd, or cwd.
func actionDir(cwd string, a journal.Action) string {
	if a.Dir == "" {
		return cwd
	}
	if !filepath.IsAbs(a.Dir) && cwd != "" {
		return filepath.Join(cwd, a.Dir)
	}
	return a.Dir
}

// pushesAway reports whether an agent's `git push` in dir went to a remote
// that isn't local (rule 2 of Event).
func (m *Matcher) pushesAway(dir string, a journal.Action) bool {
	remote := ""
	if len(a.Refs) > 0 {
		remote = a.Refs[0]
	}
	if strings.ContainsAny(remote, "/:") {
		return !localRemote(remote) // a URL or a path, not a remote's name
	}
	if url, ok := m.remoteURL(dir, remote); ok {
		return !localRemote(url)
	}
	return a.Repo != ""
}

// localRemote reports whether a remote URL is on this machine: a file://
// URL or a path. A URL with any other scheme, or git's scp-like
// [user@]host:path (a colon before any slash), is not.
func localRemote(url string) bool {
	if i := strings.Index(url, "://"); i >= 0 {
		return strings.EqualFold(url[:i], "file")
	}
	if c := strings.IndexByte(url, ':'); c > 0 && !strings.Contains(url[:c], "/") {
		return false
	}
	return true
}

// memo caches a string answer under key.
func (m *Matcher) memo(key string, f func() string) string {
	m.mu.Lock()
	v, ok := m.strs[key]
	m.mu.Unlock()
	if ok {
		return v
	}
	v = f()
	m.mu.Lock()
	m.strs[key] = v
	m.mu.Unlock()
	return v
}

// gitDirOf returns the git dir of the working tree holding the temp folder
// dir, read from the first .git found going up while still in a temp
// folder (a directory, or a file naming the git dir as a linked worktree's
// does); "" when there is none, as when the folder is gone.
func (m *Matcher) gitDirOf(dir string) string {
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	return m.memo("gitdir\x00"+dir, func() string {
		for d := filepath.Clean(dir); m.Path(d); d = filepath.Dir(d) {
			dotGit := filepath.Join(d, ".git")
			fi, err := os.Stat(dotGit)
			if err == nil && fi.IsDir() {
				return dotGit
			}
			if err == nil {
				b, err := os.ReadFile(dotGit)
				if err != nil {
					return ""
				}
				gd, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
				if !ok {
					return ""
				}
				if !filepath.IsAbs(gd) {
					gd = filepath.Join(d, gd)
				}
				return filepath.Clean(gd)
			}
			if d == filepath.Dir(d) {
				break
			}
		}
		return ""
	})
}

// commonDir is a git dir's common dir: what its commondir file names (a
// linked worktree's), or itself.
func commonDir(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	cd := strings.TrimSpace(string(b))
	if !filepath.IsAbs(cd) {
		cd = filepath.Join(gitDir, cd)
	}
	return filepath.Clean(cd)
}

// rooted reports whether gitDir, or its common dir, resolves inside a keep
// root (rule 1 of Event).
func (m *Matcher) rooted(gitDir string) bool {
	if gitDir == "" || !filepath.IsAbs(gitDir) {
		return false
	}
	return m.memo("rooted\x00"+gitDir, func() string {
		for _, p := range []string{filepath.Clean(gitDir), commonDir(filepath.Clean(gitDir))} {
			if anyUnder(p, m.keep) {
				return "y"
			}
			if real, err := filepath.EvalSymlinks(p); err == nil && anyUnder(real, m.keep) {
				return "y"
			}
		}
		return ""
	}) != ""
}

// remoteURL reads the URL git pushes to for remote (its pushurl, else its
// url; an empty remote is remote.pushDefault, else origin) from the config
// of the clone holding dir. ok is false when the clone or the remote can't
// be found.
func (m *Matcher) remoteURL(dir, remote string) (url string, ok bool) {
	gd := m.gitDirOf(dir)
	if gd == "" {
		return "", false
	}
	v := m.memo("remote\x00"+gd+"\x00"+remote, func() string {
		b, err := os.ReadFile(filepath.Join(commonDir(gd), "config"))
		if err != nil {
			return ""
		}
		cfg := parseGitConfig(string(b))
		name := remote
		if name == "" {
			name = cfg["remote.pushdefault"]
		}
		if name == "" {
			name = "origin"
		}
		for _, k := range []string{"pushurl", "url"} {
			if u := cfg["remote."+name+"."+k]; u != "" {
				return "=" + u
			}
		}
		return ""
	})
	return strings.TrimPrefix(v, "="), v != ""
}

// parseGitConfig reads the plain keys of a git config file as
// section[.subsection].key → first value (section and key lower-cased).
// It is enough for remote URLs: includes and url rewrites are not followed.
func parseGitConfig(s string) map[string]string {
	out := map[string]string{}
	section := ""
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			end := strings.IndexByte(line, ']')
			if end < 0 {
				continue
			}
			head := strings.TrimSpace(line[1:end])
			if i := strings.IndexByte(head, '"'); i >= 0 {
				section = strings.ToLower(strings.TrimSpace(head[:i])) + "." + strings.Trim(head[i:], `"`)
			} else {
				section = strings.ToLower(head)
			}
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		k = section + "." + strings.ToLower(strings.TrimSpace(k))
		v = strings.Trim(strings.TrimSpace(v), `"`)
		if _, seen := out[k]; !seen {
			out[k] = v
		}
	}
	return out
}

// Root returns the longest temp root p is under, and p as it matched
// (written, or with symlinks resolved); "" when p is not temp.
func (m *Matcher) Root(p string) (root, matched string) {
	if !m.Path(p) {
		return "", ""
	}
	p = filepath.Clean(p)
	if r := longest(p, m.temp); r != "" {
		return r, p
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return longest(real, m.temp), real
	}
	return "", ""
}

func longest(p string, roots []string) string {
	best := ""
	for _, r := range roots {
		if under(p, r) && len(r) > len(best) {
			best = r
		}
	}
	return best
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
		if dir := actionDir(ev.CWD, a); m.Path(dir) {
			return filepath.Clean(dir)
		}
	}
	return ""
}
