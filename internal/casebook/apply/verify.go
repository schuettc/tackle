package apply

import "github.com/schuettc/tackle/internal/casebook/item"

// Verify promotes a reported step against a fresh observation (§5.5). It
// returns:
//
//   - StepVerified when obs shows the intended result (the branch is gone, the
//     worktree is removed, the repo is archived, the PR is closed/merged);
//   - StepFailed when obs contradicts the result — the destructive command
//     "ran" but the target is still present. The lane records this as drift and
//     returns the item to Attention;
//   - StepReported when the observation is inconclusive (unknown): the regular
//     sync remains the backstop.
func Verify(step JobStep, obs item.Observed) StepState {
	if !obs.Known {
		return StepReported
	}
	switch step.Action {
	case "branch-delete-local", "branch-delete-remote", "worktree-remove", "repo-delete":
		if !obs.Exists {
			return StepVerified
		}
		return StepFailed // drift: still present
	case "repo-archive":
		if obs.Exists && obs.Archived {
			return StepVerified
		}
		return StepReported
	case "pr-close", "issue-close":
		if obs.State == "CLOSED" || obs.State == "MERGED" {
			return StepVerified
		}
		return StepReported
	case "pr-merge":
		if obs.State == "MERGED" {
			return StepVerified
		}
		return StepReported
	}
	return StepReported
}
