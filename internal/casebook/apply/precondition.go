package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/observe"
)

// Env carries the live seams a precondition (and the lane runner) uses to query
// the world right now, never the last snapshot (§5.3). Every check queries git
// or GitHub at the moment it runs; a query that cannot be answered is treated
// as not-ok with the error as its reason, never ok by default.
type Env struct {
	// RunGit runs git in dir and returns trimmed stdout. Tests inject a spy so
	// no real repository is touched; production wires gitx.Run.
	RunGit func(ctx context.Context, dir string, args ...string) (string, error)
	// Gh runs gh for the agent-lane preconditions (a fresh read, never a write).
	Gh observe.Runner
	// Decisions returns the decision for an item key, or nil. Used by the
	// activity preconditions to compare against decided_at.
	Decisions func(key string) *item.Decision
	// Repos returns the observed repo record (for its default branch), keyed by
	// lower-case owner/name. Used by the landed check.
	Repos func(repo string) (observe.RepoObs, bool)
	// MergedPRs returns the observation's merged-PR list for a repo (lower-case
	// owner/name). Used by the landed check when the default-branch ancestor
	// test does not already prove the tip landed.
	MergedPRs func(repo string) (observe.MergedPRList, bool)
}

// Checked is the outcome of a precondition query.
//
//   - OK: the precondition holds; the step may run.
//   - Done: the target state is already reached (the branch is already gone,
//     the remote branch is already absent, the worktree path no longer
//     exists). The step is verified, not skipped, and no command runs.
//   - neither OK nor Done: the precondition failed; Reason explains it and the
//     item returns to Attention.
//
// Tip and Branch carry the live values read while checking, so the restore
// record uses exactly what was observed now (the live tip, never the snapshot).
type Checked struct {
	OK     bool
	Done   bool
	Reason string
	Tip    string
	Branch string
}

// Check runs step's precondition against the world now. A step with no
// precondition is OK. Any git or gh error, a missing clone, or an
// unavailable merged-PR list that is needed, all yield a not-ok Checked with
// the reason; never ok by default.
func Check(ctx context.Context, step JobStep, env Env) (Checked, error) {
	switch step.Precondition {
	case "":
		return Checked{OK: true}, nil
	case "branch-tip-unchanged-and-landed":
		return checkBranchLocal(ctx, step, env)
	case "remote-tip-unchanged-and-landed":
		return checkBranchRemote(ctx, step, env)
	case "worktree-clean":
		return checkWorktreeClean(ctx, step, env)
	case "pr-no-new-activity":
		return checkPRNoActivity(ctx, step, env)
	case "repo-no-open-human-prs":
		return checkRepoNoHumanPRs(ctx, step, env)
	default:
		return Checked{Reason: "unknown precondition " + step.Precondition}, nil
	}
}

// checkBranchLocal implements branch-tip-unchanged-and-landed: the live tip of
// refs/heads/<b> in the clone equals the step's ExpectedTip, and that commit is
// landed. A branch that no longer exists locally is already done.
func checkBranchLocal(ctx context.Context, step JobStep, env Env) (Checked, error) {
	dir, args, err := gitCommand(step.Command)
	if err != nil {
		return Checked{}, err
	}
	branch := args[len(args)-1]
	if _, err := os.Stat(dir); err != nil {
		return Checked{Reason: "clone missing: " + dir}, nil
	}
	live, err := env.RunGit(ctx, dir, "rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil {
		// The branch is already gone locally: already done, not a failure.
		return Checked{Done: true}, nil
	}
	live = strings.TrimSpace(live)
	if step.ExpectedTip != "" && live != step.ExpectedTip {
		return Checked{Reason: fmt.Sprintf("branch tip moved: have %s, expected %s", short(live), short(step.ExpectedTip))}, nil
	}
	landed, reason := isLanded(ctx, env, dir, live, step.Key)
	if !landed {
		return Checked{Reason: reason}, nil
	}
	return Checked{OK: true, Tip: live, Branch: branch}, nil
}

// checkBranchRemote implements remote-tip-unchanged-and-landed: the remote's
// live tip (read with ls-remote) is landed, and equals ExpectedTip when that is
// set. An already-absent remote branch is already done.
func checkBranchRemote(ctx context.Context, step JobStep, env Env) (Checked, error) {
	dir, remote, branch, err := parseRemoteDelete(step.Command)
	if err != nil {
		return Checked{}, err
	}
	if _, err := os.Stat(dir); err != nil {
		return Checked{Reason: "clone missing: " + dir}, nil
	}
	out, err := env.RunGit(ctx, dir, "ls-remote", remote, "refs/heads/"+branch)
	if err != nil {
		return Checked{Reason: "ls-remote failed: " + err.Error()}, nil
	}
	out = strings.TrimSpace(out)
	if out == "" {
		// Another machine (or GitHub auto-delete) already removed it: done.
		return Checked{Done: true}, nil
	}
	liveTip := strings.Fields(out)[0]
	if step.ExpectedTip != "" && liveTip != step.ExpectedTip {
		return Checked{Reason: fmt.Sprintf("remote tip moved: have %s, expected %s", short(liveTip), short(step.ExpectedTip))}, nil
	}
	// Make sure the object is local before the landed check; fetch it if not.
	if _, err := env.RunGit(ctx, dir, "cat-file", "-e", liveTip); err != nil {
		if _, err := env.RunGit(ctx, dir, "fetch", remote, "refs/heads/"+branch); err != nil {
			return Checked{Reason: "fetch remote tip failed: " + err.Error()}, nil
		}
	}
	landed, reason := isLanded(ctx, env, dir, liveTip, step.Key)
	if !landed {
		return Checked{Reason: reason}, nil
	}
	return Checked{OK: true, Tip: liveTip, Branch: branch}, nil
}

// checkWorktreeClean implements worktree-clean: git status --porcelain is empty
// (untracked included) and the worktree is not locked. A worktree path that no
// longer exists is already done.
func checkWorktreeClean(ctx context.Context, step JobStep, env Env) (Checked, error) {
	dir, path, err := parseWorktreeRemove(step.Command)
	if err != nil {
		return Checked{}, err
	}
	if _, err := os.Stat(path); err != nil {
		return Checked{Done: true}, nil
	}
	out, err := env.RunGit(ctx, path, "status", "--porcelain")
	if err != nil {
		return Checked{Reason: "status failed: " + err.Error()}, nil
	}
	if strings.TrimSpace(out) != "" {
		return Checked{Reason: "worktree not clean"}, nil
	}
	list, err := env.RunGit(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return Checked{Reason: "worktree list failed: " + err.Error()}, nil
	}
	if worktreeLocked(list, resolvePath(path)) {
		return Checked{Reason: "worktree is locked"}, nil
	}
	head, _ := env.RunGit(ctx, path, "rev-parse", "HEAD")
	branch, _ := env.RunGit(ctx, path, "symbolic-ref", "--quiet", "--short", "HEAD")
	return Checked{OK: true, Tip: strings.TrimSpace(head), Branch: strings.TrimSpace(branch)}, nil
}

// checkPRNoActivity implements pr-no-new-activity: a fresh gh pr view whose
// updatedAt is not after the decision's decided_at.
func checkPRNoActivity(ctx context.Context, step JobStep, env Env) (Checked, error) {
	k, err := item.ParseKey(step.Key)
	if err != nil {
		return Checked{}, err
	}
	if env.Gh == nil {
		return Checked{Reason: "no gh available"}, nil
	}
	b, err := env.Gh.Gh(ctx, "pr", "view", fmt.Sprint(k.Number), "-R", k.Repo(), "--json", "updatedAt")
	if err != nil {
		return Checked{Reason: "gh pr view failed: " + err.Error()}, nil
	}
	var v struct {
		UpdatedAt time.Time `json:"updatedAt"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return Checked{Reason: "gh pr view parse: " + err.Error()}, nil
	}
	var decidedAt time.Time
	if env.Decisions != nil {
		if d := env.Decisions(step.Key); d != nil {
			decidedAt = d.DecidedAt
		}
	}
	if v.UpdatedAt.After(decidedAt) {
		return Checked{Reason: "new activity since the decision"}, nil
	}
	return Checked{OK: true}, nil
}

// checkRepoNoHumanPRs implements repo-no-open-human-prs: a fresh gh pr list of
// open PRs with no non-bot author.
func checkRepoNoHumanPRs(ctx context.Context, step JobStep, env Env) (Checked, error) {
	k, err := item.ParseKey(step.Key)
	if err != nil {
		return Checked{}, err
	}
	if env.Gh == nil {
		return Checked{Reason: "no gh available"}, nil
	}
	b, err := env.Gh.Gh(ctx, "pr", "list", "-R", k.Repo(), "--state", "open", "--json", "author")
	if err != nil {
		return Checked{Reason: "gh pr list failed: " + err.Error()}, nil
	}
	var prs []struct {
		Author struct {
			Login string `json:"login"`
			IsBot bool   `json:"is_bot"`
			Type  string `json:"__typename"`
		} `json:"author"`
	}
	if err := json.Unmarshal(b, &prs); err != nil {
		return Checked{Reason: "gh pr list parse: " + err.Error()}, nil
	}
	for _, pr := range prs {
		if !pr.Author.IsBot && pr.Author.Type != "Bot" {
			return Checked{Reason: "repo has an open human PR"}, nil
		}
	}
	return Checked{OK: true}, nil
}

// isLanded reports whether tip is landed for the repo of key: an ancestor of
// the default branch (remote-tracking, then local), or the head of a merged PR
// or an ancestor of one. A merged-PR list that is needed but unavailable makes
// this not-landed with that reason (never landed by default).
func isLanded(ctx context.Context, env Env, dir, tip, key string) (bool, string) {
	repo := repoOfKey(key)
	def := "main"
	if env.Repos != nil {
		if r, ok := env.Repos(strings.ToLower(repo)); ok && r.DefaultBranch != "" {
			def = r.DefaultBranch
		}
	}
	// Default-branch ancestor: try each remote-tracking ref, then the local ref.
	var refs []string
	if remotes, err := env.RunGit(ctx, dir, "remote"); err == nil {
		for _, rm := range strings.Fields(remotes) {
			refs = append(refs, "refs/remotes/"+rm+"/"+def)
		}
	}
	refs = append(refs, "refs/heads/"+def)
	for _, ref := range refs {
		if _, err := env.RunGit(ctx, dir, "merge-base", "--is-ancestor", tip, ref); err == nil {
			return true, ""
		}
	}
	// Merged-PR head (or an ancestor of one).
	if env.MergedPRs == nil {
		return false, "cannot verify landed: no merged-PR source"
	}
	ml, ok := env.MergedPRs(strings.ToLower(repo))
	if !ok || !ml.Fetched {
		return false, "cannot verify landed: merged-PR list unavailable"
	}
	for _, pr := range ml.PRs {
		if pr.HeadRefOid == "" {
			continue
		}
		if pr.HeadRefOid == tip {
			return true, ""
		}
		if _, err := env.RunGit(ctx, dir, "cat-file", "-e", pr.HeadRefOid); err != nil {
			continue
		}
		if _, err := env.RunGit(ctx, dir, "merge-base", "--is-ancestor", tip, pr.HeadRefOid); err == nil {
			return true, ""
		}
	}
	return false, "branch is not landed"
}

// repoOfKey returns owner/name for a branch/pr/issue/repo key, or "".
func repoOfKey(key string) string {
	k, err := item.ParseKey(key)
	if err != nil {
		return ""
	}
	return k.Repo()
}

// worktreeLocked reports whether the worktree at path is locked, from the
// porcelain output of `git worktree list --porcelain`.
func worktreeLocked(list, path string) bool {
	inBlock := false
	for _, line := range strings.Split(list, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			inBlock = strings.TrimPrefix(line, "worktree ") == path
		case line == "":
			inBlock = false
		case inBlock && (line == "locked" || strings.HasPrefix(line, "locked ")):
			return true
		}
	}
	return false
}

// worktreeListed reports whether path appears as a worktree in the porcelain
// output of `git worktree list --porcelain`.
func worktreeListed(list, path string) bool {
	for _, line := range strings.Split(list, "\n") {
		if strings.HasPrefix(line, "worktree ") && strings.TrimPrefix(line, "worktree ") == path {
			return true
		}
	}
	return false
}

// resolvePath returns path with symlinks resolved, so it matches the resolved
// paths that `git worktree list --porcelain` prints (macOS /var -> /private/var).
func resolvePath(path string) string {
	if rp, err := filepath.EvalSymlinks(path); err == nil {
		return rp
	}
	return path
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
