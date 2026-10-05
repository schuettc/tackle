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

// File is one physical file, one per real path, however many ways it is
// loaded (see Load).
type File struct {
	// Path is where the file was found first: under the repo root for a repo
	// file, or the path a profile names (a symlink, perhaps) for a global or
	// skill.
	Path string
	// Class and Profiles are derived from the file's loads: global when some
	// harness loads it as its global file, else skill or repo by name; and
	// the harnesses that load it in any way (none for a skill in a repo:
	// which harness loads it depends on where it is installed).
	Class    Class
	Profiles []string
	Repo     *Repo  // nil outside a repo
	Rel      string // repo-relative, '/'-separated
	Content  string
	// Also lists other paths that reach the same file (symlinks).
	Also []string
	// Context marks a file read only for chains: an instruction file above a
	// root inside a repo, which a harness loads but sift does not audit. It
	// gets no rows and is not in Result.Files.
	Context bool
}

// Role is how a harness loads a file.
type Role string

// The roles.
const (
	RoleGlobal Role = "global" // the harness's global file
	RoleRepo   Role = "repo"   // the file it picks in a directory
	RoleSkill  Role = "skill"  // a skill in its skill directories
)

// Load is one way a harness loads a file, as discovery found it. A global
// that links into a repo has two: the global's harness loads it as its
// global, and a harness that picks it there loads it as a repo file.
type Load struct {
	Profile string
	Role    Role
	Dir     string // repo-relative directory of a repo load; "" is the root
	File    *File
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
// global file, then the files it picks in each directory from the repo root
// down. A file loaded both ways is in it twice. Only the deepest chains are
// listed, and each ends at an audited file.
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
	Files    []*File // audited files
	Loads    []Load  // every load, context files' included, in discovery order
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
}

// Run discovers the files.
func Run(ctx context.Context, opt Options) (Result, error) {
	f := &finder{opt: opt, real: map[string]*File{}, repos: map[string]*repoTree{}}
	f.globals()
	for _, root := range opt.Roots {
		f.root(ctx, root)
	}
	f.skillDirs()
	f.collapseCopies()
	f.derive()
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
// another path, and records prof's load of it in role (none without a
// profile). It returns the file.
func (f *finder) add(p string, class Class, role Role, prof string) *File {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		real = p
	}
	file, ok := f.real[real]
	if ok {
		if p != file.Path && !contains(file.Also, p) {
			file.Also = append(file.Also, p)
		}
	} else {
		b, err := os.ReadFile(p)
		if err != nil {
			f.warn("%s: %v", p, err)
			return nil
		}
		file = &File{Path: p, Class: class, Content: string(b)}
		f.real[real] = file
		f.res.Files = append(f.res.Files, file)
	}
	if prof != "" {
		f.load(prof, role, "", file)
	}
	return file
}

// load records that prof loads file in role, once.
func (f *finder) load(prof string, role Role, dir string, file *File) {
	for _, l := range f.res.Loads {
		if l.Profile == prof && l.Role == role && l.Dir == dir && l.File == file {
			return
		}
	}
	f.res.Loads = append(f.res.Loads, Load{Profile: prof, Role: role, Dir: dir, File: file})
}

// derive sets each file's class and profiles from its loads.
func (f *finder) derive() {
	for _, file := range f.real {
		file.Profiles = nil
		switch {
		case file.Class == ClassSkill || path.Base(filepath.ToSlash(file.Path)) == profile.SkillFile:
			file.Class = ClassSkill
		default:
			file.Class = ClassRepo
		}
	}
	for _, l := range f.res.Loads {
		if l.Role == RoleGlobal {
			l.File.Class = ClassGlobal
		}
		if !contains(l.File.Profiles, l.Profile) {
			l.File.Profiles = append(l.File.Profiles, l.Profile)
		}
	}
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
			f.add(gp, ClassGlobal, RoleGlobal, p.Name)
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
			f.add(fp, ClassSkill, RoleSkill, prof)
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
			file := f.add(filepath.Join(dir, name), class, RoleRepo, "")
			if file != nil {
				for _, pn := range profs {
					f.load(pn, RoleRepo, "", file)
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

// repoFile reads one file of r at its base, once per real path, and records
// the loads of the profiles that pick it there. A file a profile reached on
// disk (a global that links into the repo) becomes the repo's file too: read
// at the base, with the repo's path in Also. A context file is read for
// chains only, until a root that audits it comes along.
func (f *finder) repoFile(ctx context.Context, r *repoTree, dir, realRoot, rel string, profs []string, onlyContext bool) {
	key := filepath.Join(realRoot, filepath.FromSlash(rel))
	p := filepath.Join(dir, filepath.FromSlash(rel))
	file := f.real[key]
	if file == nil || file.Repo == nil {
		body, err := git(ctx, dir, "show", r.Ref+":"+rel)
		if err != nil {
			f.warn("%s: %v", p, err)
			return
		}
		if file == nil {
			class := ClassRepo
			if path.Base(rel) == profile.SkillFile {
				class = ClassSkill
			}
			file = &File{Path: p, Class: class, Context: onlyContext}
			f.real[key] = file
			if !onlyContext {
				f.res.Files = append(f.res.Files, file)
			}
		} else if p != file.Path && !contains(file.Also, p) {
			file.Also = append(file.Also, p)
		}
		file.Repo, file.Rel, file.Content = r.Repo, rel, string(body)
	} else if file.Context && !onlyContext {
		file.Context = false
		f.res.Files = append(f.res.Files, file)
	}
	for _, pn := range profs {
		f.load(pn, RoleRepo, dirOf(rel), file)
	}
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
	dropped := map[*File]bool{}
	for _, c := range f.res.Copies {
		for real, file := range f.real {
			if file.Path == c.Path {
				dropped[file] = true
				delete(f.real, real)
			}
		}
	}
	loads := f.res.Loads[:0]
	for _, l := range f.res.Loads {
		if !dropped[l.File] {
			loads = append(loads, l)
		}
	}
	f.res.Loads = loads
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

// chains builds, from the loads alone, each load-limited profile's deepest
// chains in each repo: for every deepest directory holding an audited file
// it loads there, its global load, then its repo loads from the repo root
// down to that directory, in load order. A file loaded both as the global
// and as a repo file is in the chain twice, and counts twice.
func (f *finder) chains() {
	for _, p := range f.opt.Profiles {
		if p.LoadLimit <= 0 {
			continue
		}
		var global *File
		for _, l := range f.res.Loads {
			if l.Profile == p.Name && l.Role == RoleGlobal {
				global = l.File
				break
			}
		}
		for _, r := range f.res.Repos {
			byDir, audited := map[string][]*File{}, map[string]bool{}
			for _, l := range f.res.Loads {
				if l.Profile == p.Name && l.Role == RoleRepo && l.File.Repo == r {
					byDir[l.Dir] = append(byDir[l.Dir], l.File)
					if !l.File.Context {
						audited[l.Dir] = true
					}
				}
			}
			var dirs []string
			for d := range audited {
				deepest := true
				for o := range audited {
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
					c.Files = append(c.Files, byDir[a]...)
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
