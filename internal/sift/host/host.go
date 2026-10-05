// Package host looks up pull request and issue state for the stale-status
// check. Only GitHub, through the gh CLI, is supported; without gh there is no
// host, and the check reports references as findings to judge.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Ref is one pull request or issue: owner/name and a number.
type Ref struct {
	Repo   string
	Number int
}

func (r Ref) String() string { return fmt.Sprintf("%s#%d", r.Repo, r.Number) }

// State is what the host says about a Ref.
type State struct {
	Kind  string // "pr" or "issue"
	State string // "open", "closed" or (a PR) "merged"
}

// Host answers Ref lookups.
type Host interface {
	Lookup(ctx context.Context, r Ref) (State, error)
}

// Runner runs a command and returns its stdout.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Exec is the Runner that runs real commands.
func Exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
		return out, fmt.Errorf("%s: %s", name, strings.TrimSpace(string(ee.Stderr)))
	}
	return out, err
}

// Detect returns the GitHub host when gh is on PATH, else nil.
func Detect(lookPath func(string) (string, error), run Runner) Host {
	if _, err := lookPath("gh"); err != nil {
		return nil
	}
	return &github{run: run, cache: map[Ref]answer{}}
}

type answer struct {
	s   State
	err error
}

type github struct {
	run   Runner
	mu    sync.Mutex
	cache map[Ref]answer
}

// Lookup asks GitHub's issues endpoint, which answers for pull requests too
// (with pull_request.merged_at). Answers, errors included, are cached.
func (g *github) Lookup(ctx context.Context, r Ref) (State, error) {
	g.mu.Lock()
	a, ok := g.cache[r]
	g.mu.Unlock()
	if ok {
		return a.s, a.err
	}
	a.s, a.err = g.lookup(ctx, r)
	g.mu.Lock()
	g.cache[r] = a
	g.mu.Unlock()
	return a.s, a.err
}

func (g *github) lookup(ctx context.Context, r Ref) (State, error) {
	out, err := g.run(ctx, "gh", "api", fmt.Sprintf("repos/%s/issues/%d", r.Repo, r.Number))
	if err != nil {
		return State{}, err
	}
	var v struct {
		State       string `json:"state"`
		PullRequest *struct {
			MergedAt *string `json:"merged_at"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return State{}, fmt.Errorf("%s: %w", r, err)
	}
	s := State{Kind: "issue", State: v.State}
	if v.PullRequest != nil {
		s.Kind = "pr"
		if v.PullRequest.MergedAt != nil {
			s.State = "merged"
		}
	}
	return s, nil
}

var slugRE = regexp.MustCompile(`^(?:git@github\.com:|ssh://git@github\.com/|https?://github\.com/)([\w.-]+)/([\w.-]+?)(?:\.git)?/?$`)

// Slug returns owner/name for a GitHub remote URL.
func Slug(remote string) (string, bool) {
	m := slugRE.FindStringSubmatch(strings.TrimSpace(remote))
	if m == nil {
		return "", false
	}
	return m[1] + "/" + m[2], true
}

var refRE = regexp.MustCompile(`https?://github\.com/([\w.-]+/[\w.-]+)/(?:pull|issues)/(\d+)|(?:^|[^\w&/])(?:([\w.-]+/[\w.-]+))?#(\d+)\b`)

// Refs returns the pull request and issue references in a line, in order: a
// GitHub URL, owner/name#N, or a bare #N (which names repo, when there is
// one).
func Refs(line, repo string) []Ref {
	var out []Ref
	for _, m := range refRE.FindAllStringSubmatch(line, -1) {
		r := Ref{}
		num := m[2]
		if m[1] != "" {
			r.Repo = m[1]
		} else {
			r.Repo, num = m[3], m[4]
			if r.Repo == "" {
				r.Repo = repo
			}
		}
		n, err := strconv.Atoi(num)
		if err != nil || r.Repo == "" {
			continue
		}
		r.Number = n
		out = append(out, r)
	}
	return out
}
