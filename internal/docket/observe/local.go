package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/schuettc/tackle/internal/docket/gitx"
	"github.com/schuettc/tackle/internal/docket/journal"
)

var skipDirs = map[string]bool{".git": true, ".worktrees": true, "node_modules": true, "vendor": true, ".venv": true, "venv": true,
	"target": true, "dist": true, "build": true, ".next": true, ".cache": true, ".terraform": true, "__pycache__": true}

const (
	maxDepth    = 6
	maxUnpushed = 1000
)

// Scan finds every clone under roots and records its local state. The walk
// is sequential; inspecting the clones runs on up to 8 goroutines.
func Scan(ctx context.Context, roots []string, machine string) (Snapshot, []error) {
	snap := Snapshot{Version: SnapshotVersion, Machine: machine, Roots: roots, Clones: []Clone{}}
	seen := map[string]bool{}
	var found []string
	var errs []error
	for _, root := range roots {
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			errs = append(errs, fmt.Errorf("root %s: %w", root, err))
			continue
		}
		_ = filepath.WalkDir(real, func(p string, de fs.DirEntry, err error) error {
			if err != nil || !de.IsDir() {
				return nil
			}
			if p != real && skipDirs[de.Name()] {
				return filepath.SkipDir
			}
			if rel, _ := filepath.Rel(real, p); rel != "." && strings.Count(rel, string(filepath.Separator)) >= maxDepth {
				return filepath.SkipDir
			}
			kind := dirKind(p)
			if kind == "" {
				return nil
			}
			if kind == "linked" {
				return filepath.SkipDir
			}
			if !seen[p] {
				seen[p] = true
				found = append(found, p)
			}
			if kind == "bare" {
				return filepath.SkipDir
			}
			return nil
		})
	}
	clones := make([]Clone, len(found))
	cerrs := make([]error, len(found))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, p := range found {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			clones[i], cerrs[i] = inspect(ctx, p)
		}()
	}
	wg.Wait()
	for i := range found {
		if cerrs[i] != nil {
			errs = append(errs, fmt.Errorf("%s: %w", found[i], cerrs[i]))
			continue
		}
		snap.Clones = append(snap.Clones, clones[i])
	}
	sort.Slice(snap.Clones, func(i, j int) bool { return snap.Clones[i].Path < snap.Clones[j].Path })
	return snap, errs
}

// dirKind returns "clone" (.git directory), "linked" (.git file), "bare" (bare
// repo layout) or "" (not a git repo).
func dirKind(p string) string {
	if fi, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
		if fi.IsDir() {
			return "clone"
		}
		return "linked"
	}
	head, err1 := os.Stat(filepath.Join(p, "HEAD"))
	obj, err2 := os.Stat(filepath.Join(p, "objects"))
	refs, err3 := os.Stat(filepath.Join(p, "refs"))
	if err1 == nil && err2 == nil && err3 == nil && !head.IsDir() && obj.IsDir() && refs.IsDir() {
		return "bare"
	}
	return ""
}

func inspect(ctx context.Context, dir string) (Clone, error) {
	bare, err := gitx.Run(ctx, dir, "rev-parse", "--is-bare-repository")
	if err != nil {
		return Clone{}, err
	}
	c := Clone{Path: dir, Bare: bare == "true"}
	if out, err := gitx.Run(ctx, dir, "remote", "-v"); err == nil && out != "" {
		c.Remotes = map[string]string{}
		for _, line := range strings.Split(out, "\n") {
			f := strings.Fields(line)
			if len(f) < 3 || f[2] != "(fetch)" {
				continue
			}
			if r := GitHubRepo(f[1]); r != "" {
				c.Remotes[f[0]] = r
			} else {
				c.Remotes[f[0]] = "url:" + journal.RedactURL(f[1])
			}
		}
		c.Repo = identity(c.Remotes)
	}
	if !c.Bare {
		st, err := gitx.Run(ctx, dir, "status", "--porcelain=v1", "--untracked-files=normal")
		if err != nil {
			return c, err
		}
		c.Dirty = st != ""
		if out, err := gitx.Run(ctx, dir, "stash", "list"); err == nil && out != "" {
			c.Stashes = len(strings.Split(out, "\n"))
		}
	}
	c.LocalHooksPath, _ = gitx.Run(ctx, dir, "config", "--local", "--get", "core.hooksPath")
	branches, err := gitx.Run(ctx, dir, "for-each-ref", "--format=%(refname:short)%09%(upstream:short)%09%(upstream:track,nobracket)%09%(objectname)", "refs/heads")
	if err != nil {
		return c, err
	}
	for _, line := range strings.Split(branches, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 {
			continue
		}
		b := Branch{Name: f[0], Upstream: f[1], Tip: f[3]}
		for _, part := range strings.Split(f[2], ",") {
			part = strings.TrimSpace(part)
			if n, ok := strings.CutPrefix(part, "ahead "); ok {
				b.Ahead, _ = strconv.Atoi(n)
			}
			if part == "gone" {
				b.Gone = true
			}
		}
		if out, err := gitx.Run(ctx, dir, "log", "--format=%ct", "-n", strconv.Itoa(maxUnpushed), "refs/heads/"+f[0], "--not", "--remotes"); err == nil && out != "" {
			lines := strings.Split(out, "\n")
			b.Unpushed = len(lines)
			if ts, err := strconv.ParseInt(lines[len(lines)-1], 10, 64); err == nil {
				b.OldestUnpushed = time.Unix(ts, 0).UTC()
			}
		}
		c.Branches = append(c.Branches, b)
	}
	sort.Slice(c.Branches, func(i, j int) bool { return c.Branches[i].Name < c.Branches[j].Name })
	c.Worktrees = worktrees(ctx, dir)
	return c, nil
}

// identity picks the remote that names the clone's repo: origin if it is
// GitHub, else the first GitHub remote by name.
func identity(remotes map[string]string) string {
	if r := remotes["origin"]; r != "" && !strings.HasPrefix(r, "url:") {
		return r
	}
	names := make([]string, 0, len(remotes))
	for n := range remotes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !strings.HasPrefix(remotes[n], "url:") {
			return remotes[n]
		}
	}
	return ""
}

func worktrees(ctx context.Context, dir string) []Worktree {
	out, err := gitx.Run(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	var wts []Worktree
	for i, block := range strings.Split(out, "\n\n") {
		if i == 0 { // the main worktree, or the bare repo itself
			continue
		}
		var w Worktree
		prunable := false
		for _, line := range strings.Split(block, "\n") {
			k, v, _ := strings.Cut(line, " ")
			switch k {
			case "worktree":
				w.Path = v
			case "HEAD":
				w.Head = v
			case "branch":
				w.Branch = strings.TrimPrefix(v, "refs/heads/")
			case "detached":
				w.Detached = true
			case "prunable":
				prunable = true
			}
		}
		if w.Path == "" || prunable {
			continue
		}
		if st, err := gitx.Run(ctx, w.Path, "status", "--porcelain=v1", "--untracked-files=normal"); err == nil {
			w.Dirty = st != ""
		}
		wts = append(wts, w)
	}
	sort.Slice(wts, func(i, j int) bool { return wts[i].Path < wts[j].Path })
	return wts
}

// EncodeSnapshot renders s deterministically (map keys sorted by
// encoding/json, slices already sorted by Scan).
func EncodeSnapshot(s Snapshot) ([]byte, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
