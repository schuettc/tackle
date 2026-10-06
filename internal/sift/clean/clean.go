// Package clean is `sift clean`: it removes what sift apply recorded
// creating, and nothing else. A round branch goes, local and remote, once
// its pull request is merged or closed (checked through gh); without gh, or
// with no pull request, only once its commit is in the base. A worktree
// apply left behind goes whatever its branch. Nothing is ever selected by
// name or author: other tools and sessions may share the account and use
// the same names. Plan works out what to do and touches nothing; Do does
// it.
package clean

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/schuettc/tackle/internal/sift/apply"
	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/store"
)

// Options says how to clean.
type Options struct {
	Store *store.Store
	// Gh checks pull requests; nil: no gh, a branch goes only once its
	// commit is in the base.
	Gh apply.Runner
	// DryRun plans and touches nothing.
	DryRun bool
}

// Step is one recorded thing and what clean does with it.
type Step struct {
	ID   int64  `json:"-"`
	Kind string `json:"kind"` // branch or worktree
	Repo string `json:"repo"`
	Name string `json:"name"` // the branch, or the worktree's path
	PR   string `json:"pr,omitempty"`
	// Action is remove or keep; Why says why.
	Action string `json:"action"`
	Why    string `json:"why"`
	// Local and Remote say what of a branch is there to remove (a branch
	// gone from both is only crossed off the record).
	Local  bool `json:"local,omitempty"`
	Remote bool `json:"remote,omitempty"`
	// Done: removed and crossed off; Error: what went wrong instead.
	Done  bool   `json:"done,omitempty"`
	Error string `json:"error,omitempty"`

	commit string
	pushed bool
}

// Run plans, then (unless a dry run) does the plan.
func Run(ctx context.Context, o Options) ([]Step, error) {
	steps, err := Plan(ctx, o)
	if err != nil || o.DryRun {
		return steps, err
	}
	return Do(ctx, o, steps), nil
}

// Plan works out what to remove, worktrees first (a branch checked out in
// one can't be deleted). It changes nothing: no fetch, no write.
func Plan(ctx context.Context, o Options) ([]Step, error) {
	made, err := o.Store.Created(ctx)
	if err != nil {
		return nil, err
	}
	var wts, branches []Step
	for _, c := range made {
		s := Step{ID: c.ID, Kind: c.Kind, Repo: c.Repo, Name: c.Name, PR: c.PR, commit: c.Commit, pushed: c.Pushed}
		switch c.Kind {
		case "worktree":
			s.Action, s.Why = "remove", "a worktree apply left behind"
			if _, err := os.Stat(c.Name); err != nil {
				s.Why = "gone already: only crossed off the record"
			}
			wts = append(wts, s)
		case "branch":
			planBranch(ctx, o, c, &s)
			branches = append(branches, s)
		}
	}
	return append(wts, branches...), nil
}

func planBranch(ctx context.Context, o Options, c store.Created, s *Step) {
	if _, err := os.Stat(c.Repo); err != nil {
		s.Action, s.Why = "keep", "the repo is not there: "+c.Repo
		return
	}
	local, _ := git(ctx, c.Repo, "rev-parse", "--verify", "-q", "refs/heads/"+c.Name)
	remote := ""
	if c.Pushed {
		out, err := git(ctx, c.Repo, "ls-remote", "--heads", "origin", "refs/heads/"+c.Name)
		if err != nil {
			s.Action, s.Why = "keep", "could not read origin: "+oneLine(err.Error())
			return
		}
		remote, _, _ = strings.Cut(out, "\t")
	}
	if local == "" && remote == "" {
		s.Action, s.Why = "remove", "gone already: only crossed off the record"
		return
	}
	// Recorded before commits were (the v6 migration): nothing proves the
	// branch by that name is the one sift made, so it is kept.
	if s.commit == "" {
		s.Action, s.Why = "keep", "recorded without its commit: sift can't tell it is the branch it made"
		return
	}
	// A branch now at another commit than the one apply made is someone
	// else's under the same name.
	for _, at := range []string{local, remote} {
		if at != "" && at != s.commit {
			s.Action, s.Why = "keep", "now at "+short(at)+", not the commit sift made ("+short(s.commit)+")"
			return
		}
	}
	s.Local, s.Remote = local != "", remote != ""
	if o.Gh != nil && c.PR != "" {
		out, err := o.Gh(ctx, c.Repo, "pr", "view", c.PR, "--json", "state", "--jq", ".state")
		if err != nil {
			s.Action, s.Why = "keep", "could not check the pull request: "+oneLine(err.Error())
			return
		}
		switch st := strings.TrimSpace(string(out)); st {
		case "MERGED", "CLOSED":
			s.Action, s.Why = "remove", "pull request "+strings.ToLower(st)
		default:
			s.Action, s.Why = "keep", "pull request "+strings.ToLower(st)
		}
		return
	}
	why := "no gh"
	if o.Gh != nil {
		why = "no pull request"
	}
	base := c.Base
	if base == "" {
		base = "HEAD"
	}
	if _, err := git(ctx, c.Repo, "merge-base", "--is-ancestor", s.commit, base); err != nil {
		s.Action, s.Why = "keep", why+", and its commit is not in "+base
		return
	}
	s.Action, s.Why = "remove", why+": its commit is in "+base
}

// Do carries out a plan's removals and crosses each off the record once
// it is gone. Kept steps are left as they are.
func Do(ctx context.Context, o Options, steps []Step) []Step {
	for i := range steps {
		s := &steps[i]
		if s.Action != "remove" {
			continue
		}
		var err error
		switch s.Kind {
		case "worktree":
			err = removeWorktree(ctx, s)
		case "branch":
			err = removeBranch(ctx, s)
		}
		if err == nil {
			err = o.Store.RemovedCreated(ctx, s.ID)
		}
		if err != nil {
			s.Error = oneLine(err.Error())
			continue
		}
		s.Done = true
	}
	return steps
}

// removeWorktree removes the recorded worktree and its registration only
// (git worktree remove drops a registration whose directory is gone too),
// never pruning the repo's other registrations.
func removeWorktree(ctx context.Context, s *Step) error {
	if _, err := os.Stat(s.Repo); errors.Is(err, fs.ErrNotExist) {
		return nil // the repo is gone, and its registrations with it
	} else if err != nil {
		return err // keep the record so the next clean retries
	}
	if !registered(ctx, s.Repo, s.Name) {
		return nil
	}
	_, err := git(ctx, s.Repo, "worktree", "remove", "--force", s.Name)
	return err
}

// registered reports whether repo has a worktree registered at path (by
// its path as given or with symlinks resolved).
func registered(ctx context.Context, repo, path string) bool {
	out, err := git(ctx, repo, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return true // can't tell: let worktree remove decide
	}
	want := map[string]bool{filepath.Clean(path): true, realish(path): true}
	for _, field := range strings.Split(out, "\x00") {
		if p, ok := strings.CutPrefix(field, "worktree "); ok && (want[filepath.Clean(p)] || want[realish(p)]) {
			return true
		}
	}
	return false
}

// realish is p with the symlinks in its longest existing prefix resolved.
func realish(p string) string {
	rest := ""
	for d := filepath.Clean(p); ; d = filepath.Dir(d) {
		if r, err := filepath.EvalSymlinks(d); err == nil {
			return filepath.Join(r, rest)
		}
		if filepath.Dir(d) == d {
			return p
		}
		rest = filepath.Join(filepath.Base(d), rest)
	}
}

// removeBranch deletes a branch only while it is at the commit sift made:
// locally, checked again just before the delete; on origin, with that
// commit as the lease.
func removeBranch(ctx context.Context, s *Step) error {
	if !s.Local && !s.Remote {
		return nil
	}
	if s.commit == "" {
		return fmt.Errorf("no recorded commit for %s", s.Name)
	}
	if s.Local {
		at, _ := git(ctx, s.Repo, "rev-parse", "--verify", "-q", "refs/heads/"+s.Name)
		if at != s.commit {
			return fmt.Errorf("%s moved to %s since the plan, off the commit sift made (%s)", s.Name, short(at), short(s.commit))
		}
		if _, err := git(ctx, s.Repo, "branch", "-D", s.Name); err != nil {
			return err
		}
	}
	if s.Remote {
		// A delete pushes no code, but the repo's pre-push hook still runs
		// on it (some run a full test gate, minutes long): --no-verify,
		// for this delete only. The lease refuses the delete if the remote
		// branch moved off the commit sift made.
		if _, err := git(ctx, s.Repo, "push", "--no-verify", "-q", "--force-with-lease=refs/heads/"+s.Name+":"+s.commit, "origin", "--delete", s.Name); err != nil {
			return err
		}
	}
	return nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := discover.Git(ctx, dir, args...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func short(sha string) string { return sha[:min(len(sha), 7)] }

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
