// Package item is the docket's pure data model: item keys, decisions, until
// conditions, statuses and policies. It does no I/O.
package item

import (
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// Kind is an item's kind.
type Kind string

// Item kinds.
const (
	KindRepo     Kind = "repo"
	KindPR       Kind = "pr"
	KindIssue    Kind = "issue"
	KindBranch   Kind = "branch"
	KindWorktree Kind = "worktree"
)

// Key identifies one item. Owner and Name are lower-cased (GitHub is
// case-insensitive and the docket keeps one spelling); Branch and Path keep
// their case.
type Key struct {
	Kind    Kind
	Owner   string // repo, pr, issue, branch
	Name    string // repo, pr, issue, branch
	Number  int    // pr, issue
	Branch  string // branch
	Machine string // worktree
	Path    string // worktree (absolute)
}

// Repo returns "owner/name", or "" for worktrees.
func (k Key) Repo() string {
	if k.Owner == "" {
		return ""
	}
	return k.Owner + "/" + k.Name
}

// String renders the canonical key text.
func (k Key) String() string {
	switch k.Kind {
	case KindRepo:
		return "repo:" + k.Repo()
	case KindPR, KindIssue:
		return string(k.Kind) + ":" + k.Repo() + "#" + strconv.Itoa(k.Number)
	case KindBranch:
		return "branch:" + k.Repo() + "@" + k.Branch
	case KindWorktree:
		return "worktree:" + k.Machine + ":" + k.Path
	}
	return ""
}

// File is the item's decision file, relative to the docket repo root and
// slash-separated.
func (k Key) File() string {
	switch k.Kind {
	case KindRepo:
		return path.Join("items", "repo", k.Owner, k.Name+".toml")
	case KindPR, KindIssue:
		return path.Join("items", string(k.Kind), k.Owner, k.Name, strconv.Itoa(k.Number)+".toml")
	case KindBranch:
		return path.Join("items", "branch", k.Owner, k.Name, url.PathEscape(k.Branch)+".toml")
	case KindWorktree:
		return path.Join("items", "worktree", k.Machine, url.PathEscape(k.Path)+".toml")
	}
	return ""
}

// ParseKey parses canonical key text: "repo:o/n", "pr:o/n#1", "issue:o/n#1",
// "branch:o/n@branch" or "worktree:machine:/abs/path".
func ParseKey(s string) (Key, error) {
	kind, rest, ok := strings.Cut(s, ":")
	if !ok || rest == "" {
		return Key{}, fmt.Errorf("invalid key %q: want <kind>:<id>", s)
	}
	switch Kind(kind) {
	case KindRepo:
		o, n, err := splitRepo(rest)
		if err != nil {
			return Key{}, fmt.Errorf("invalid key %q: %w", s, err)
		}
		return Key{Kind: KindRepo, Owner: o, Name: n}, nil
	case KindPR, KindIssue:
		repo, num, ok := strings.Cut(rest, "#")
		n, err := strconv.Atoi(num)
		if !ok || err != nil || n <= 0 {
			return Key{}, fmt.Errorf("invalid key %q: want %s:owner/name#number", s, kind)
		}
		o, nm, err := splitRepo(repo)
		if err != nil {
			return Key{}, fmt.Errorf("invalid key %q: %w", s, err)
		}
		return Key{Kind: Kind(kind), Owner: o, Name: nm, Number: n}, nil
	case KindBranch:
		repo, br, ok := strings.Cut(rest, "@")
		if !ok || br == "" {
			return Key{}, fmt.Errorf("invalid key %q: want branch:owner/name@branch", s)
		}
		o, nm, err := splitRepo(repo)
		if err != nil {
			return Key{}, fmt.Errorf("invalid key %q: %w", s, err)
		}
		return Key{Kind: KindBranch, Owner: o, Name: nm, Branch: br}, nil
	case KindWorktree:
		m, p, ok := strings.Cut(rest, ":")
		if !ok || m == "" || !strings.HasPrefix(p, "/") {
			return Key{}, fmt.Errorf("invalid key %q: want worktree:machine:/abs/path", s)
		}
		return Key{Kind: KindWorktree, Machine: m, Path: p}, nil
	}
	return Key{}, fmt.Errorf("invalid key %q: unknown kind %q", s, kind)
}

// KeyFromFile inverts File: a slash-separated "items/<kind>/..." path back to
// its key.
func KeyFromFile(rel string) (Key, error) {
	bad := fmt.Errorf("not a decision file path: %q", rel)
	if !strings.HasSuffix(rel, ".toml") {
		return Key{}, bad
	}
	p := strings.Split(strings.TrimSuffix(rel, ".toml"), "/")
	if len(p) < 4 || p[0] != "items" {
		return Key{}, bad
	}
	switch Kind(p[1]) {
	case KindRepo:
		if len(p) != 4 {
			return Key{}, bad
		}
		return RepoKey(p[2] + "/" + p[3]), nil
	case KindPR, KindIssue:
		n, err := strconv.Atoi(p[len(p)-1])
		if len(p) != 5 || err != nil || n <= 0 {
			return Key{}, bad
		}
		return Key{Kind: Kind(p[1]), Owner: strings.ToLower(p[2]), Name: strings.ToLower(p[3]), Number: n}, nil
	case KindBranch:
		if len(p) != 5 {
			return Key{}, bad
		}
		b, err := url.PathUnescape(p[4])
		if err != nil || b == "" {
			return Key{}, bad
		}
		return BranchKey(p[2]+"/"+p[3], b), nil
	case KindWorktree:
		if len(p) != 4 {
			return Key{}, bad
		}
		wp, err := url.PathUnescape(p[3])
		if err != nil || !strings.HasPrefix(wp, "/") {
			return Key{}, bad
		}
		return WorktreeKey(p[2], wp), nil
	}
	return Key{}, bad
}

// RepoKey builds a repo key from "owner/name".
func RepoKey(repo string) Key {
	o, n := lowerRepo(repo)
	return Key{Kind: KindRepo, Owner: o, Name: n}
}

// PRKey builds a pull request key from "owner/name" and a number.
func PRKey(repo string, n int) Key {
	o, nm := lowerRepo(repo)
	return Key{Kind: KindPR, Owner: o, Name: nm, Number: n}
}

// IssueKey builds an issue key from "owner/name" and a number.
func IssueKey(repo string, n int) Key {
	o, nm := lowerRepo(repo)
	return Key{Kind: KindIssue, Owner: o, Name: nm, Number: n}
}

// BranchKey builds a branch key from "owner/name" and a branch name.
func BranchKey(repo, branch string) Key {
	o, nm := lowerRepo(repo)
	return Key{Kind: KindBranch, Owner: o, Name: nm, Branch: branch}
}

// WorktreeKey builds a worktree key from a machine name and an absolute path.
func WorktreeKey(machine, path string) Key {
	return Key{Kind: KindWorktree, Machine: machine, Path: path}
}

func lowerRepo(repo string) (string, string) {
	o, n, _ := strings.Cut(repo, "/")
	return strings.ToLower(o), strings.ToLower(n)
}

func splitRepo(s string) (string, string, error) {
	o, n, ok := strings.Cut(s, "/")
	if !ok || o == "" || n == "" || strings.Contains(n, "/") {
		return "", "", fmt.Errorf("want owner/name, got %q", s)
	}
	return strings.ToLower(o), strings.ToLower(n), nil
}
