package apply

import (
	"context"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/observe"
	"github.com/schuettc/tackle/internal/casebook/store"
)

// DetailDrift is the step detail recorded when a step's command "ran" but a
// fresh observation contradicts the intended result (the target is still
// present). It is a StepFailed with no needs-you card of its own; later tasks
// key on this exact value to route the item back to Attention (\u00a75.5).
const DetailDrift = "drift"

// Runner executes a single casebook-lane step and re-observes it. Repo is the
// casebook-data repository where restore records are committed; RunGit is the
// git seam (gitx.Run in production, a spy in tests) that runs the destructive
// command and the targeted re-observation; Now supplies the restore file date.
type Runner struct {
	Repo   *store.Repo
	Gh     observe.Runner
	RunGit func(ctx context.Context, dir string, args ...string) (string, error)
	Now    func() time.Time
}

func (r Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// StepResult is the outcome of RunStep. Restore is the deterministic undo
// command committed before the step ran (empty for non-destructive or
// already-done steps), which the lane persists on the step with SetStepRestore.
type StepResult struct {
	State   StepState
	Detail  string
	Restore string
}

// RunStep runs one casebook-lane step in the fixed, load-bearing order (§5.3,
// §5.5):
//
//  1. Check the precondition against the world now. On failure: StepSkipped
//     with the reason, no restore record, no command (the item returns to
//     Attention). When the target is already reached: StepReported with no
//     command, so re-observation promotes it to verified.
//  2. Build the restore record and commit it to casebook-data. If the commit
//     fails, abort with StepFailed — the command never runs.
//  3. Run the exact command.
//  4. Return StepReported. Verification is a separate, observation-driven
//     promotion (RunCasebookLane calls Verify).
func (r Runner) RunStep(ctx context.Context, step JobStep, env Env) (StepResult, error) {
	// TSV safety: a tab or newline anywhere in the command or key would corrupt
	// a restores/<date>.tsv line (its fields are TAB-separated, one per line).
	// Refuse such a step outright, before the precondition or any command runs.
	if strings.ContainsAny(step.Command, "\t\n") || strings.ContainsAny(step.Key, "\t\n") {
		return StepResult{State: StepFailed, Detail: "unsafe path: contains a tab or newline"}, nil
	}
	chk, err := Check(ctx, step, env)
	if err != nil {
		return StepResult{State: StepFailed, Detail: err.Error()}, nil //nolint:nilerr // step failure → StepResult.Detail, not a program error
	}
	if !chk.OK && !chk.Done {
		return StepResult{State: StepSkipped, Detail: chk.Reason}, nil
	}

	var restoreCmd string
	if !chk.Done {
		// A branch delete without an expected tip would be unconditional
		// (`update-ref -d <ref>` with no old value deletes whatever is there), so it
		// never runs: the compare-and-delete guard depends on the tip.
		if (step.Action == "branch-delete-local" || step.Action == "branch-delete-remote") && step.ExpectedTip == "" {
			return StepResult{State: StepFailed, Detail: "no expected tip: refusing an unconditional delete"}, nil
		}
		// Restore record BEFORE the destructive command (Review Focus 5).
		if rec, has := RestoreFor(step, chk); has {
			committed, err := r.Repo.AppendRestore(ctx, r.now().UTC().Format("2006-01-02"), rec)
			if err != nil {
				return StepResult{State: StepFailed, Detail: "restore record commit failed: " + err.Error()}, nil //nolint:nilerr // step failure → StepResult.Detail, not a program error
			}
			if !committed {
				return StepResult{State: StepFailed, Detail: "restore record was not committed"}, nil
			}
			restoreCmd = rec.RestoreCommand
		}
		dir, args, err := gitCommand(step.Command)
		if err != nil {
			return StepResult{State: StepFailed, Detail: err.Error()}, nil //nolint:nilerr // step failure → StepResult.Detail, not a program error
		}
		if _, err := r.RunGit(ctx, dir, args...); err != nil {
			return StepResult{State: StepFailed, Detail: "command failed: " + err.Error()}, nil //nolint:nilerr // step failure → StepResult.Detail, not a program error
		}
	}
	return StepResult{State: StepReported, Restore: restoreCmd}, nil
}

// Observe re-reads just this step's key from the world (a targeted git read),
// so Verify can promote a reported step (§5.5).
func (r Runner) Observe(ctx context.Context, step JobStep) item.Observed {
	switch step.Action {
	case "branch-delete-local":
		dir, branch, err := parseLocalDelete(step.Command)
		if err != nil {
			return item.Observed{}
		}
		_, err = r.RunGit(ctx, dir, "rev-parse", "--verify", "refs/heads/"+branch)
		return item.Observed{Known: true, Exists: err == nil}
	case "branch-delete-remote":
		dir, remote, branch, err := parseRemoteDelete(step.Command)
		if err != nil {
			return item.Observed{}
		}
		out, err := r.RunGit(ctx, dir, "ls-remote", remote, "refs/heads/"+branch)
		if err != nil {
			return item.Observed{}
		}
		return item.Observed{Known: true, Exists: strings.TrimSpace(out) != ""}
	case "worktree-remove":
		dir, path, err := parseWorktreeRemove(step.Command)
		if err != nil {
			return item.Observed{}
		}
		out, err := r.RunGit(ctx, dir, "worktree", "list", "--porcelain")
		if err != nil {
			return item.Observed{}
		}
		return item.Observed{Known: true, Exists: worktreeListed(out, resolvePath(path))}
	}
	return item.Observed{}
}

// RunCasebookLane runs the job's runnable casebook-lane steps in order. After
// each step it honours pause() (§5.3), stopping both lanes after the current
// step. A failed step opens a needs-you card of kind "failed" so Court can hand
// it to the agent lane (§5.2); a drift is recorded as failed with detail
// "drift" and returns the item to Attention (§5.5).
func (s *Store) RunCasebookLane(ctx context.Context, job Job, r Runner, env Env, pause func() bool) error {
	// Move the job to running so ClaimNext keeps handing out steps; ignore an
	// invalid transition (it may already be running).
	_ = s.SetJobState(ctx, job.ID, JobRunning)

	for {
		if pause != nil && pause() {
			return nil
		}
		step, ok, err := s.ClaimNext(ctx, job.ID, LaneCasebook)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}

		res, err := r.RunStep(ctx, step, env)
		if err != nil {
			return err
		}

		switch res.State {
		case StepSkipped:
			if err := s.SetStepState(ctx, step.ID, StepSkipped, res.Detail); err != nil {
				return err
			}
		case StepFailed:
			if err := s.SetStepState(ctx, step.ID, StepFailed, res.Detail); err != nil {
				return err
			}
			if _, err := s.OpenNeedsYou(ctx, job.ID, step.ID, "failed", res.Detail, ""); err != nil {
				return err
			}
		case StepReported:
			if res.Restore != "" {
				if err := s.SetStepRestore(ctx, step.ID, res.Restore); err != nil {
					return err
				}
			}
			if err := s.SetStepState(ctx, step.ID, StepReported, ""); err != nil {
				return err
			}
			// Step 5: verify by re-observing just this key.
			obs := r.Observe(ctx, step)
			switch Verify(step, obs) {
			case StepVerified:
				if err := s.SetStepState(ctx, step.ID, StepVerified, ""); err != nil {
					return err
				}
			case StepFailed:
				if err := s.SetStepState(ctx, step.ID, StepFailed, DetailDrift); err != nil {
					return err
				}
			}
		}

		if pause != nil && pause() {
			return nil
		}
	}
}
