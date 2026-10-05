// Package discover finds the instruction files sift audits: each enabled
// profile's global file and skill directories, and every instruction file
// and skill under the configured roots. Repos are read at their fetched
// upstream base (git ls-tree / git show), not the working tree, because the
// audit judges what everyone gets; files outside any repo are read from disk.
// It also builds each load-limited harness's chain per directory and
// collapses installed skill copies into the source sift audits.
package discover

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/gitenv"
	"github.com/schuettc/tackle/internal/sift/profile"
)

// Class is a file's budget class.
type Class string

// The classes, by when the file is loaded.
const (
	ClassGlobal Class = "global" // every session
	ClassRepo   Class = "repo"   // every session in that repo
	ClassSkill  Class = "skill"  // when the skill is used
)

// File is one audited file.
type File struct {
	// Path is where the file was found: under the repo root for a repo file,
	// or the path a profile names (a symlink, perhaps) for a global or skill.
	Path  string
	Class Class
	// Profiles names the enabled harnesses that load the file (none for a
	// skill in a repo: which harness loads it depends on where it is
	// installed).
	Profiles []string
	Repo     *Repo  // nil outside a repo
	Rel      string // repo-relative, '/'-separated
	Content  string
	// Also lists other paths that reach the same file (symlinks).
	Also []string
}

// Repo is one git repository under a root.
type Repo struct {
	Root   string
	Ref    string // what it was read at: origin/<base>, an upstream, or HEAD
	Remote string // origin's URL, if any
	// Tree holds every file and directory at Ref (repo-relative).
	Tree map[string]bool
	// Gone holds every file Ref's history deleted (renamed files under their
	// old names) and Ref does not have.
	Gone map[string]bool
}

// Has reports whether rel (a file or directory) exists at the repo's Ref.
func (r *Repo) Has(rel string) bool { return r.Tree[strings.TrimSuffix(path.Clean(rel), "/")] }

// Copy is an installed skill counted once, at its source.
type Copy struct{ Path, Of string }

// Chain is what a load-limited harness loads in one directory of a repo: its
// global file, then the file it picks in each directory from the repo root
// down. Only the deepest chains are listed.
type Chain struct {
	Profile string
	Repo    *Repo
	Dir     string // repo-relative; "" is the root
	Files   []*File
	Bytes   int
	Limit   int
}

// Result is everything Run found.
type Result struct {
	Files    []*File
	Repos    []*Repo
	Copies   []Copy
	Chains   []Chain
	Warnings []string
}

// Options says what to look at.
type Options struct {
	Profiles []profile.Profile
	Roots    []config.Root
}

// skipDirs are never descended into, on disk or in a tree.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "testdata": true, ".worktrees": true,
}

type finder struct {
	opt   Options
	res   Result
	real  map[string]*File     // realpath → file, so a file reached twice is one
	repos map[string]*repoTree // root → repo
	// context holds, by realpath, instruction files above a root inside a
	// repo: in chains, never audited.
	context map[string]*File
	// picks holds the profiles that load each repo file as a repo file (a
	// global that links into a repo has other profiles too).
	picks map[*File][]string
}

// Run discovers the files.
func Run(ctx context.Context, opt Options) (Result, error) {
	f := &finder{opt: opt, real: map[string]*File{}, repos: map[string]*repoTree{}, context: map[string]*File{}, picks: map[*File][]string{}}
	f.globals()
	for _, root := range opt.Roots {
		f.root(ctx, root)
	}
	f.skillDirs()
	f.collapseCopies()
	f.chains()
	rank := map[Class]int{ClassGlobal: 0, ClassRepo: 1, ClassSkill: 2}
	sort.SliceStable(f.res.Files, func(i, j int) bool {
		a, b := f.res.Files[i], f.res.Files[j]
		if rank[a.Class] != rank[b.Class] {
			return rank[a.Class] < rank[b.Class]
		}
		return a.Path < b.Path
	})
	return f.res, nil
}

func (f *finder) warn(format string, a ...any) {
	f.res.Warnings = append(f.res.Warnings, fmt.Sprintf(format, a...))
}

// add records a disk file, or merges it into the file already reached by
// another path. It returns the file.
func (f *finder) add(p string, class Class, prof string) *File {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		real = p
	}
	if have, ok := f.real[real]; ok {
		if prof != "" && !contains(have.Profiles, prof) {
			have.Profiles = append(have.Profiles, prof)
		}
		if p != have.Path && !contains(have.Also, p) {
			have.Also = append(have.Also, p)
		}
		return have
	}
	b, err := os.ReadFile(p)
	if err != nil {
		f.warn("%s: %v", p, err)
		return nil
	}
	file := &File{Path: p, Class: class, Content: string(b)}
	if prof != "" {
		file.Profiles = []string{prof}
	}
	f.real[real] = file
	f.res.Files = append(f.res.Files, file)
	return file
}

// globals adds each profile's global file: the first candidate present
// with FirstOnly, else every one.
func (f *finder) globals() {
	for _, p := range f.opt.Profiles {
		for _, name := range p.Global {
			gp := p.Path(name)
			if st, err := os.Stat(gp); err != nil || !st.Mode().IsRegular() {
				continue
			}
			f.add(gp, ClassGlobal, p.Name)
			if p.FirstOnly {
				break
			}
		}
	}
}

// skillDirs adds every SKILL.md under each profile's skill directories,
// following symlinks (the directory itself, or a skill installed as a link
// to its source). Hidden directories in them are the harness's own (Codex
// keeps its bundled skills in .system) and are skipped.
func (f *finder) skillDirs() {
	for _, p := range f.opt.Profiles {
		for _, s := range p.Skills {
			f.walkSkills(p.Path(s), p.Name, map[string]bool{})
		}
	}
}

// walkSkills adds the SKILL.md files under dir, named by the path they were
// reached by. seen holds the real directories walked, so a link back up the
// tree is not followed twice.
func (f *finder) walkSkills(dir, prof string, seen map[string]bool) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil || seen[real] {
		return
	}
	seen[real] = true
	entries, err := os.ReadDir(real)
	if err != nil {
		return
	}
	for _, e := range entries {
		fp := filepath.Join(dir, e.Name())
		isDir, regular := e.IsDir(), e.Type().IsRegular()
		if e.Type()&fs.ModeSymlink != 0 {
			st, err := os.Stat(fp)
			if err != nil {
				continue
			}
			isDir, regular = st.IsDir(), st.Mode().IsRegular()
		}
		switch {
		case isDir:
			if !skipDirs[e.Name()] && !strings.HasPrefix(e.Name(), ".") {
				f.walkSkills(fp, prof, seen)
			}
		case regular && e.Name() == profile.SkillFile:
			f.add(fp, ClassSkill, prof)
		}
	}
}

// root walks one root on disk: every directory holding a .git directory is
// a repo, read at its base; a .git file (a linked worktree or submodule) is
// skipped; files outside any repo are read from disk. A root inside a repo is
// read at that repo's base too, limited to the root's subtree.
func (f *finder) root(ctx context.Context, root config.Root) {
	start := root.Path
	if st, err := os.Lstat(start); err != nil {
		f.warn("root %s: %v", start, err)
		return
	} else if st.Mode()&os.ModeSymlink != 0 {
		if start, err = filepath.EvalSymlinks(start); err != nil {
			f.warn("root %s: %v", root.Path, err)
			return
		}
	}
	var repoRoots []string
	if top, prefix := enclosing(ctx, start); top != "" {
		repoRoots = append(repoRoots, start)
		if err := f.repo(ctx, top, root, prefix); err != nil {
			f.warn("repo %s: %v", top, err)
		}
	}
	inRepo := func(p string) bool {
		for _, r := range repoRoots {
			if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	loose := map[string][]string{} // dir → instruction file names present, outside repos
	err := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == start {
				return err
			}
			return nil //nolint:nilerr // an unreadable directory is skipped, not fatal
		}
		if d.IsDir() {
			if p != start && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			if st, gerr := os.Lstat(filepath.Join(p, ".git")); gerr == nil {
				// A .git file is a linked worktree or a submodule: skipped,
				// unless it is the root itself.
				if !st.IsDir() && p != start {
					return filepath.SkipDir
				}
				repoRoots = append(repoRoots, p)
				if err := f.repo(ctx, p, root, ""); err != nil {
					f.warn("repo %s: %v", p, err)
				}
			}
			return nil
		}
		if inRepo(p) || !d.Type().IsRegular() {
			return nil
		}
		if d.Name() == profile.SkillFile || f.isRepoFile(d.Name()) {
			dir := filepath.Dir(p)
			loose[dir] = append(loose[dir], d.Name())
		}
		return nil
	})
	if err != nil {
		f.warn("root %s: %v", root.Path, err)
	}
	dirs := make([]string, 0, len(loose))
	for d := range loose {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		for name, profs := range f.pick(loose[dir]) {
			class := ClassRepo
			if name == profile.SkillFile {
				class = ClassSkill
			}
			file := f.add(filepath.Join(dir, name), class, "")
			if file != nil {
				for _, pn := range profs {
					if !contains(file.Profiles, pn) {
						file.Profiles = append(file.Profiles, pn)
					}
				}
			}
		}
	}
}

func (f *finder) isRepoFile(name string) bool {
	for _, p := range f.opt.Profiles {
		if p.IsRepoFile(name) {
			return true
		}
	}
	return false
}

// pick returns, of the names present in one directory, the files to audit
// and the profiles that load each: a SKILL.md always (no profile), an
// instruction file when some enabled profile picks it.
func (f *finder) pick(names []string) map[string][]string {
	out := map[string][]string{}
	for _, n := range names {
		if n == profile.SkillFile {
			out[n] = nil
		}
	}
	for _, p := range f.opt.Profiles {
		for _, n := range p.Pick(names) {
			out[n] = append(out[n], p.Name)
		}
	}
	return out
}

// enclosing returns the top of the repo start is inside (in start's own
// form where it can) and start's '/'-separated path under it; "" when start
// is no repo's subdirectory (it is a repo's top, or in none).
func enclosing(ctx context.Context, start string) (top, prefix string) {
	if _, err := os.Lstat(filepath.Join(start, ".git")); err == nil {
		return "", ""
	}
	out, err := git(ctx, start, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", ""
	}
	real := strings.TrimSpace(string(out))
	realStart, err := filepath.EvalSymlinks(start)
	if err != nil || real == "" {
		return "", ""
	}
	rel, err := filepath.Rel(real, realStart)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ""
	}
	top = start
	for range strings.Split(rel, string(filepath.Separator)) {
		top = filepath.Dir(top)
	}
	if t, err := filepath.EvalSymlinks(top); err != nil || t != real {
		top = real
	}
	return top, filepath.ToSlash(rel)
}

// repo reads one repo at its base: its whole tree, and the files sift audits
// under prefix (a '/'-separated directory; "" is the whole repo) that no
// other root has added. With a prefix, the instruction files in the
// directories above it are read too, as chain context only: a harness loads
// them, but they are not this root's to audit.
func (f *finder) repo(ctx context.Context, dir string, root config.Root, prefix string) error {
	r, err := f.readRepo(ctx, dir, root.Base)
	if err != nil {
		return err
	}
	above := map[string]bool{}
	if prefix != "" {
		for _, a := range ancestors(prefix) {
			if a != prefix {
				above[a] = true
			}
		}
	}
	byDir := map[string][]string{}
	for rel := range r.files {
		name := path.Base(rel)
		if prefix != "" && !strings.HasPrefix(rel, prefix+"/") && (!above[dirOf(rel)] || name == profile.SkillFile) {
			continue
		}
		if name != profile.SkillFile && !f.isRepoFile(name) {
			continue
		}
		if skipped(rel) || excluded(rel, root.Exclude) {
			continue
		}
		byDir[path.Dir(rel)] = append(byDir[path.Dir(rel)], name)
	}
	realRoot, err := filepath.EvalSymlinks(dir)
	if err != nil {
		realRoot = dir
	}
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		picked := f.pick(byDir[d])
		names := make([]string, 0, len(picked))
		for n := range picked {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			rel := n
			if d != "." {
				rel = d + "/" + n
			}
			f.repoFile(ctx, r, dir, realRoot, rel, picked[n], prefix != "" && !strings.HasPrefix(rel, prefix+"/"))
		}
	}
	return nil
}

// repoFile reads one file of r at its base. A file already audited from the
// repo is left alone; one a profile reached on disk (a global that links into
// the repo) becomes the repo's file too: read at the base, with the repo's
// profiles and path. A context file is kept for chains only, never audited.
func (f *finder) repoFile(ctx context.Context, r *repoTree, dir, realRoot, rel string, profs []string, onlyContext bool) {
	key := filepath.Join(realRoot, filepath.FromSlash(rel))
	have := f.real[key]
	if have != nil && have.Repo != nil {
		return
	}
	if have == nil && onlyContext && f.context[key] != nil {
		return
	}
	body, err := git(ctx, dir, "show", r.Ref+":"+rel)
	if err != nil {
		f.warn("%s: %v", filepath.Join(dir, rel), err)
		return
	}
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if have != nil {
		have.Repo, have.Rel, have.Content = r.Repo, rel, string(body)
		for _, pn := range profs {
			if !contains(have.Profiles, pn) {
				have.Profiles = append(have.Profiles, pn)
			}
		}
		if p != have.Path && !contains(have.Also, p) {
			have.Also = append(have.Also, p)
		}
		f.picks[have] = profs
		return
	}
	class := ClassRepo
	if path.Base(rel) == profile.SkillFile {
		class = ClassSkill
	}
	file := &File{Path: p, Class: class, Profiles: profs, Repo: r.Repo, Rel: rel, Content: string(body)}
	f.picks[file] = profs
	if onlyContext {
		f.context[key] = file
		return
	}
	f.real[key] = file
	f.res.Files = append(f.res.Files, file)
}

// readRepo reads a repo's tree at its base, once.
func (f *finder) readRepo(ctx context.Context, dir, base string) (*repoTree, error) {
	if r, ok := f.repos[dir]; ok {
		return r, nil
	}
	ref := resolveRef(ctx, dir, base)
	if ref == "" {
		return nil, errors.New("no commits")
	}
	out, err := git(ctx, dir, "ls-tree", "-r", "-z", "--name-only", ref)
	if err != nil {
		return nil, err
	}
	r := &repoTree{Repo: &Repo{Root: dir, Ref: ref, Tree: map[string]bool{}}, files: map[string]bool{}}
	if u, err := git(ctx, dir, "config", "--get", "remote.origin.url"); err == nil {
		r.Remote = strings.TrimSpace(string(u))
	}
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" {
			continue
		}
		r.files[rel] = true
		r.Tree[rel] = true
		for d := path.Dir(rel); d != "."; d = path.Dir(d) {
			r.Tree[d] = true
		}
	}
	r.Gone = map[string]bool{}
	if out, err := git(ctx, dir, "log", "--format=", "--name-only", "--no-renames", "--diff-filter=D", "-z", ref); err == nil {
		for _, rel := range strings.Split(string(out), "\x00") {
			if rel = strings.TrimSpace(rel); rel != "" && !r.files[rel] {
				r.Gone[rel] = true
			}
		}
	}
	f.repos[dir] = r
	f.res.Repos = append(f.res.Repos, r.Repo)
	return r, nil
}

// repoTree is a repo read once, with its files apart from its directories.
type repoTree struct {
	*Repo
	files map[string]bool
}

// resolveRef picks what a repo is read at: origin/<base> or <base> when
// configured and present, else origin/HEAD, else the upstream, else HEAD.
// "" means the repo has no commits.
func resolveRef(ctx context.Context, dir, base string) string {
	exists := func(ref string) bool {
		_, err := git(ctx, dir, "rev-parse", "--verify", "-q", ref+"^{commit}")
		return err == nil
	}
	if base != "" {
		for _, r := range []string{"origin/" + base, base} {
			if exists(r) {
				return r
			}
		}
	}
	if out, err := git(ctx, dir, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if r := strings.TrimSpace(string(out)); r != "" && exists(r) {
			return r
		}
	}
	if out, err := git(ctx, dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil {
		if r := strings.TrimSpace(string(out)); r != "" && exists(r) {
			return r
		}
	}
	if exists("HEAD") {
		return "HEAD"
	}
	return ""
}

// GitEnv returns env without the variables that select a repository, for
// every git command sift runs.
func GitEnv(env []string) []string { return gitenv.Clean(env) }

// Git runs git in dir with the repository-selecting variables cleared.
func Git(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = GitEnv(os.Environ())
	return cmd
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := Git(ctx, dir, append([]string{"-c", "core.quotepath=off"}, args...)...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out, nil
}

func skipped(rel string) bool {
	for _, seg := range strings.Split(path.Dir(rel), "/") {
		if skipDirs[seg] {
			return true
		}
	}
	return false
}

func excluded(rel string, globs []string) bool {
	for _, g := range globs {
		if Match(g, rel) {
			return true
		}
	}
	return false
}

// Match reports whether a '/'-separated path matches a glob in which * and ?
// stay within a segment and ** spans segments.
func Match(glob, p string) bool {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; {
		case strings.HasPrefix(glob[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(glob[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	return err == nil && re.MatchString(p)
}

// collapseCopies drops a skill outside any repo that is a copy of a skill
// sift audits in a repo: the same skill name and near-identical content.
func (f *finder) collapseCopies() {
	sources := map[string][]*File{}
	for _, file := range f.res.Files {
		if file.Class == ClassSkill && file.Repo != nil {
			name := path.Base(path.Dir(file.Rel))
			sources[name] = append(sources[name], file)
		}
	}
	kept := f.res.Files[:0]
	for _, file := range f.res.Files {
		if file.Class == ClassSkill && file.Repo == nil {
			name := filepath.Base(filepath.Dir(file.Path))
			if src := firstSimilar(file, sources[name]); src != nil {
				f.res.Copies = append(f.res.Copies, Copy{Path: file.Path, Of: src.Path})
				continue
			}
		}
		kept = append(kept, file)
	}
	f.res.Files = kept
}

func firstSimilar(file *File, cands []*File) *File {
	for _, c := range cands {
		if NearIdentical(file.Content, c.Content) {
			return c
		}
	}
	return nil
}

// NearIdentical reports whether two texts share at least 90% of their
// non-blank lines (installers often add a stamp line or two).
func NearIdentical(a, b string) bool {
	la, lb := lines(a), lines(b)
	n := max(len(la), len(lb))
	if n == 0 {
		return true
	}
	count := map[string]int{}
	for _, l := range la {
		count[l]++
	}
	common := 0
	for _, l := range lb {
		if count[l] > 0 {
			count[l]--
			common++
		}
	}
	return common*10 >= n*9
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// chains builds, for each profile with a load limit, the deepest chain in
// each repo: its global file and the file it picks in every directory from
// the repo root down. Chains end at an audited file; the directories above
// may hold context files (above a root inside the repo).
func (f *finder) chains() {
	for _, p := range f.opt.Profiles {
		if p.LoadLimit <= 0 {
			continue
		}
		var global *File
		for _, file := range f.res.Files {
			if file.Class == ClassGlobal && contains(file.Profiles, p.Name) {
				global = file
				break
			}
		}
		loads := func(file *File, r *Repo) bool {
			return file.Repo == r && path.Base(file.Rel) != profile.SkillFile && contains(f.picks[file], p.Name)
		}
		for _, r := range f.res.Repos {
			byDir, above := map[string]*File{}, map[string]*File{}
			for _, file := range f.res.Files {
				if loads(file, r) {
					byDir[dirOf(file.Rel)] = file
				}
			}
			for _, file := range f.context {
				if loads(file, r) {
					above[dirOf(file.Rel)] = file
				}
			}
			var dirs []string
			for d := range byDir {
				deepest := true
				for o := range byDir {
					if o != d && (d == "" || strings.HasPrefix(o, d+"/")) {
						deepest = false
						break
					}
				}
				if deepest {
					dirs = append(dirs, d)
				}
			}
			sort.Strings(dirs)
			for _, d := range dirs {
				c := Chain{Profile: p.Name, Repo: r, Dir: d, Limit: p.LoadLimit}
				if global != nil {
					c.Files = append(c.Files, global)
				}
				for _, a := range ancestors(d) {
					file := byDir[a]
					if file == nil {
						file = above[a]
					}
					if file != nil && file != global {
						c.Files = append(c.Files, file)
					}
				}
				for _, file := range c.Files {
					c.Bytes += len(file.Content)
				}
				f.res.Chains = append(f.res.Chains, c)
			}
		}
	}
}

func dirOf(rel string) string {
	if d := path.Dir(rel); d != "." {
		return d
	}
	return ""
}

// ancestors returns "", then each directory down to dir.
func ancestors(dir string) []string {
	out := []string{""}
	if dir == "" {
		return out
	}
	parts := strings.Split(dir, "/")
	for i := range parts {
		out = append(out, strings.Join(parts[:i+1], "/"))
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
