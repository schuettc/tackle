package serve

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apply"
	"github.com/schuettc/tackle/internal/casebook/config"
	"github.com/schuettc/tackle/internal/casebook/deliver"
	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/observe"
)

// syncInterval is the default observation staleness threshold; it matches the
// config default (30m). The serve's live index uses the config's SyncInterval
// when available, or this fallback.
const defaultSyncInterval = 30 * time.Minute

// syncIntervalFor parses the config's SyncInterval or falls back to the
// default. Errors return the default.
func (s *Server) syncIntervalFor() time.Duration {
	si := s.App.Cfg.SyncInterval
	if si == "" {
		return defaultSyncInterval
	}
	// Config stores durations as Go duration strings ("30m", "1h", …).
	d, err := time.ParseDuration(si)
	if err != nil || d <= 0 {
		return defaultSyncInterval
	}
	return d
}

// buildEnv constructs the live Env the casebook lane uses to query the world
// right now. It uses gitx.Run for git operations and the app's gh runner for
// gh operations, with live decision and observation lookups from the index and
// the on-disk GitHub cache.
func (s *Server) buildEnv() apply.Env {
	// Load the GitHub cache for repos and merged-PR lookups.
	// A stale or missing cache is OK: lookups just return false.
	gh, _ := observe.LoadGitHub(config.CachePath())

	// Build repo and merged-PR lookup maps from the cache.
	repoMap := map[string]observe.RepoObs{}
	mergedMap := map[string]observe.MergedPRList{}
	if gh != nil {
		for _, owner := range gh.Owners {
			for _, r := range owner.Repos {
				repoMap[strings.ToLower(r.Repo)] = r
			}
		}
		for k, v := range gh.MergedPRs {
			mergedMap[strings.ToLower(k)] = v
		}
	}

	return apply.Env{
		RunGit: s.Runner.RunGit,
		Gh:     s.App.Gh,
		Decisions: func(key string) *item.Decision {
			it, ok := s.Index.Item(key)
			if !ok {
				return nil
			}
			return it.Decision
		},
		Repos: func(repo string) (observe.RepoObs, bool) {
			v, ok := repoMap[strings.ToLower(repo)]
			return v, ok
		},
		MergedPRs: func(repo string) (observe.MergedPRList, bool) {
			v, ok := mergedMap[strings.ToLower(repo)]
			return v, ok
		},
	}
}

// postApplyPlan is POST /api/apply/plan.
// Input: {"keys":["..."],"all":true}
// Returns PlanView or 409 when the index observation is stale.
func (s *Server) postApplyPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Keys []string `json:"keys"`
		All  bool     `json:"all"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()

	// Collect to-apply items from the live index.
	res := s.Index.Result()
	builtAt := s.Index.BuiltAt()
	now := s.Now()
	si := s.syncIntervalFor()

	// Filter items: all to-apply or only the requested keys.
	var items []engine.Item
	if in.All {
		for _, it := range res.Items {
			if it.Status == item.StatusToApply && it.Decision != nil {
				items = append(items, it)
			}
		}
	} else {
		keySet := make(map[string]bool, len(in.Keys))
		for _, k := range in.Keys {
			keySet[k] = true
		}
		for _, it := range res.Items {
			if keySet[it.ID] {
				items = append(items, it)
			}
		}
	}

	// Get this machine's snapshot for local step generation.
	snap, _, err := s.App.MachineSnapshot()
	if err != nil {
		reply(w, nil, fmt.Errorf("machine snapshot: %w", err))
		return
	}
	snap.Machine = s.App.Cfg.Machine

	// Build the plan (may return ErrStale).
	plan, err := apply.Build(items, snap, now, builtAt, si)
	if err != nil {
		if errors.Is(err, apply.ErrStale) {
			// 409 with a plain message — no command for Court (binding note,
			// decision 3).
			reply(w, nil, httpError{
				code: http.StatusConflict,
				msg:  "observation is older than one sync interval; sync first",
			})
			return
		}
		reply(w, nil, err)
		return
	}

	plan.Head = s.Index.Head()

	// Persist as a planned job.
	job, err := s.Apply.Create(ctx, plan, s.App.Cfg.Machine)
	if err != nil {
		reply(w, nil, err)
		return
	}

	s.Bus.Publish(ctx, "job", map[string]any{"id": job.ID, "state": apply.JobPlanned})

	reply(w, PlanView{Plan: plan, Groups: plan.Groups(), Job: job}, nil)
}

// postApplyApprove is POST /api/apply/approve.
// Input: {"plan_id":N,"session":"sid"}
// In order: approve the job, open batch card, dispatch agent job (idempotent),
// start casebook lane in background.
func (s *Server) postApplyApprove(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PlanID  int64  `json:"plan_id"`
		Session string `json:"session"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()

	// Validate session if provided.
	if in.Session != "" {
		if _, err := s.session(ctx, in.Session); err != nil {
			reply(w, nil, err)
			return
		}
	}

	// Step 1: Approve the job.
	job, err := s.Apply.Approve(ctx, in.PlanID, in.Session)
	if err != nil {
		var inv *apply.ErrInvalidTransition
		if errors.As(err, &inv) {
			reply(w, nil, httpError{code: http.StatusConflict, msg: err.Error()})
		} else {
			reply(w, nil, bad("%v", err))
		}
		return
	}

	s.Bus.Publish(ctx, "job", map[string]any{"id": job.ID, "state": apply.JobApproved})

	// Step 2: Count agent-lane steps and open batch card BEFORE dispatching.
	var agentCount int
	for _, st := range job.Steps {
		if st.Lane == apply.LaneAgent {
			agentCount++
		}
	}
	var batchCard apply.NeedsYou
	if agentCount > 0 {
		q := fmt.Sprintf("The agent has been dispatched with %d step(s). Confirm when ready to proceed.", agentCount)
		batchCard, err = s.Apply.OpenNeedsYou(ctx, job.ID, 0, "batch", q, "")
		if err != nil {
			reply(w, nil, err)
			return
		}
		s.Bus.Publish(ctx, "needs_you", batchCard)
	}

	// Step 3: Dispatch the agent job ONCE (idempotent via dispatched_at).
	if in.Session != "" && agentCount > 0 {
		first, err := s.Apply.MarkDispatched(ctx, job.ID)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if first {
			if err := s.dispatchAgentJob(ctx, job, in.Session); err != nil {
				reply(w, nil, err)
				return
			}
		}
	}

	// Step 4: Start the casebook lane once (single-flight guard).
	// Casebook-lane steps don't wait for the batch card (binding note).
	s.startCasebookLane(job)

	// Return the job with its open needs-you cards.
	cards, _ := s.Apply.NeedsYouFor(ctx, job.ID)
	reply(w, JobView{Job: job, NeedsYou: cards}, nil)
}

// getJobs is GET /api/jobs.
func (s *Server) getJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.Apply.List(r.Context())
	if err != nil {
		reply(w, nil, err)
		return
	}
	if jobs == nil {
		jobs = []apply.Job{}
	}
	reply(w, JobsView{Jobs: jobs}, nil)
}

// getJob is GET /api/job?id=.
func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id == 0 {
		reply(w, nil, bad("id required"))
		return
	}
	job, err := s.Apply.Get(ctx, id)
	if err != nil {
		reply(w, nil, httpError{code: http.StatusNotFound, msg: err.Error()})
		return
	}
	// Return only open needs-you cards.
	allCards, err := s.Apply.NeedsYouFor(ctx, id)
	if err != nil {
		reply(w, nil, err)
		return
	}
	var open []apply.NeedsYou
	for _, c := range allCards {
		if c.State == "open" {
			open = append(open, c)
		}
	}
	if open == nil {
		open = []apply.NeedsYou{}
	}
	reply(w, JobView{Job: job, NeedsYou: open}, nil)
}

// getNeedsYou is GET /api/needs-you — all open cards, for the bar count.
func (s *Server) getNeedsYou(w http.ResponseWriter, r *http.Request) {
	cards, err := s.Apply.OpenNeedsYouAll(r.Context())
	if err != nil {
		reply(w, nil, err)
		return
	}
	if cards == nil {
		cards = []apply.NeedsYou{}
	}
	reply(w, NeedsYouView{Cards: cards}, nil)
}

// postJobsPause is POST /api/jobs/pause. It sets paused=true and publishes a
// "job" event. Both lanes check the paused flag after each step.
func (s *Server) postJobsPause(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID int64 `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	if err := s.Apply.SetPaused(ctx, in.ID, true); err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	job, err := s.Apply.Get(ctx, in.ID)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	s.Bus.Publish(ctx, "job", map[string]any{"id": job.ID, "state": job.State, "paused": true})
	cards, _ := s.Apply.NeedsYouFor(ctx, in.ID)
	var open []apply.NeedsYou
	for _, c := range cards {
		if c.State == "open" {
			open = append(open, c)
		}
	}
	reply(w, JobView{Job: job, NeedsYou: open}, nil)
}

// postJobsResume is POST /api/jobs/resume. It clears the paused flag and
// publishes a "job" event. The lanes resume from where they stopped.
func (s *Server) postJobsResume(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID int64 `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	// Read the job first so we know whether it was actually paused: a resume on
	// a job that was never paused must not launch a second lane.
	prior, err := s.Apply.Get(ctx, in.ID)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	wasPaused := prior.Paused
	if err := s.Apply.SetPaused(ctx, in.ID, false); err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	job, err := s.Apply.Get(ctx, in.ID)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	s.Bus.Publish(ctx, "job", map[string]any{"id": job.ID, "state": job.State, "paused": false})

	// Relaunch the casebook lane only if the job was actually paused and no lane
	// is running (the single-flight guard enforces the latter). A resume on a
	// job that was never paused starts no second lane.
	if wasPaused {
		s.startCasebookLane(job)
	}

	cards, _ := s.Apply.NeedsYouFor(ctx, in.ID)
	var open []apply.NeedsYou
	for _, c := range cards {
		if c.State == "open" {
			open = append(open, c)
		}
	}
	reply(w, JobView{Job: job, NeedsYou: open}, nil)
}

// autoUndoable reports whether a restore command is one of the automatic,
// deterministic kinds that can be undone without human judgment (§5.5):
//   - branch recreate: "git -C <dir> branch <b> <tip>"
//   - remote branch recreate: "git -C <dir> push <remote> <tip>:refs/heads/<b>"
//   - worktree restore: "git -C <dir> worktree add <path> <ref>"
//   - gh repo unarchive
//   - gh pr reopen
//   - gh issue reopen
//
// A posted comment is never undoable. An empty restore is never undoable.
func autoUndoable(restore string) bool {
	if restore == "" {
		return false
	}
	if strings.HasPrefix(restore, "git -C ") {
		// branch recreate:   "git -C <dir> branch <b> <tip>"
		// remote recreate:   "git -C <dir> push <remote> <tip>:refs/heads/<b>"
		// worktree restore:  "git -C <dir> worktree add <path> <ref>"
		return strings.Contains(restore, " branch ") ||
			strings.Contains(restore, " push ") ||
			strings.Contains(restore, " worktree ")
	}
	for _, prefix := range []string{
		"gh repo unarchive ",
		"gh pr reopen ",
		"gh issue reopen ",
	} {
		if strings.HasPrefix(restore, prefix) {
			return true
		}
	}
	return false
}

// postJobsUndo is POST /api/jobs/undo.
// Input: {"step":N}
// Runs a verified step's restore command if autoUndoable; records undone_at.
// Casebook-lane git restores run locally; agent-lane restores (unarchive, reopen)
// are sent as a message to the job's session.
func (s *Server) postJobsUndo(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Step int64 `json:"step"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()

	step, err := s.Apply.GetStep(ctx, in.Step)
	if err != nil {
		reply(w, nil, httpError{code: http.StatusNotFound, msg: err.Error()})
		return
	}

	// Only verified steps can be undone.
	if step.State != apply.StepVerified {
		reply(w, nil, httpError{
			code: http.StatusConflict,
			msg:  fmt.Sprintf("step %d is %s; only verified steps can be undone", step.ID, step.State),
		})
		return
	}

	// Only auto-undoable restore commands.
	if !autoUndoable(step.Restore) {
		reply(w, nil, httpError{
			code: http.StatusConflict,
			msg:  fmt.Sprintf("step %d has no automatic restore command", step.ID),
		})
		return
	}

	// Get the job to find the session for agent-lane steps.
	job, err := s.Apply.Get(ctx, step.JobID)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}

	// Use the runner's RunGit for casebook-lane restores (allows test injection).
	runGit := s.Runner.RunGit

	// Claim the undo BEFORE executing the restore. MarkUndone is a one-time
	// compare-and-swap (WHERE undone_at = 0): only one concurrent caller wins;
	// the other gets a conflict so the restore command never runs twice.
	//
	// releaseUndo is a convenience closure that handles ReleaseUndoClaim
	// errors: it logs them and attempts a best-effort detail update so Court
	// can see the claim is still held.
	releaseUndo := func(detail string) {
		if err := s.Apply.ReleaseUndoClaim(ctx, step.ID, detail); err != nil {
			claimMsg := detail + "; undo-claim release also failed: " + err.Error() + " — undo claim still held, retry will be refused"
			fmt.Fprintf(os.Stderr, "casebook serve: ReleaseUndoClaim step %d: %v (undo claim still held)\n", step.ID, err)
			_ = s.Apply.SetStepDetail(ctx, step.ID, claimMsg)
		}
	}
	if err := s.Apply.MarkUndone(ctx, step.ID); err != nil {
		reply(w, nil, httpError{
			code: http.StatusConflict,
			msg:  fmt.Sprintf("step %d is already being undone", step.ID),
		})
		return
	}

	var sent bool
	if step.Lane == apply.LaneAgent {
		// Agent-lane: unarchive/reopen goes to the agent as a message.
		if job.Session == "" {
			// Release the claim so Court can retry after fixing the session.
			releaseUndo("no session for agent-lane undo")
			reply(w, nil, httpError{
				code: http.StatusConflict,
				msg:  fmt.Sprintf("job %d has no session; cannot send undo to agent", job.ID),
			})
			return
		}
		body := fmt.Sprintf("casebook undo for job %d, step %d [%s]:\nRun: %s", job.ID, step.ID, step.Key, step.Restore)
		thread, err := s.Queue.NewThread(ctx, job.Session, fmt.Sprintf("undo:job:%d:step:%d", job.ID, step.ID))
		if err != nil {
			releaseUndo("create thread for undo failed: " + err.Error())
			reply(w, nil, bad("create thread for undo: %v", err))
			return
		}
		if _, err := s.Queue.Post(ctx, thread.ID, body, deliver.Attached{Job: strconv.FormatInt(job.ID, 10)}, false); err != nil {
			releaseUndo("post undo message failed: " + err.Error())
			reply(w, nil, bad("post undo message: %v", err))
			return
		}
		s.wake(job.Session)
		sent = true
	} else {
		// Casebook-lane git restore: run locally via the runner's RunGit.
		// If the restore fails, release the claim so Court can retry.
		if err := runRestoreCommand(ctx, step.Restore, runGit); err != nil {
			releaseUndo("restore failed: " + err.Error())
			reply(w, nil, bad("restore command failed: %v", err))
			return
		}
		sent = false
	}

	s.Bus.Publish(ctx, "step", map[string]any{"id": step.ID, "job_id": step.JobID, "undone": true, "sent": sent})
	reply(w, UndoResult{StepID: step.ID, Sent: sent}, nil)
}

// runRestoreCommand runs a "git -C <dir> ..." restore command locally via the
// supplied runGit function. It only handles commands that start with "git -C "
// (the exact format produced by RestoreFor for casebook-lane steps).
func runRestoreCommand(ctx context.Context, restore string, runGit func(ctx context.Context, dir string, args ...string) (string, error)) error {
	if !strings.HasPrefix(restore, "git -C ") {
		return fmt.Errorf("unsupported restore command format: %q", restore)
	}
	words, err := apply.ShellSplit(restore)
	if err != nil || len(words) < 4 {
		return fmt.Errorf("could not parse restore command %q: %v", restore, err)
	}
	// words[0]="git", words[1]="-C", words[2]=dir, words[3:]= subcommand
	dir := words[2]
	if runGit != nil {
		_, err := runGit(ctx, dir, words[3:]...)
		return err
	}
	// Fallback to exec (only reached when runGit is nil, i.e., in degenerate tests).
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, words[3:]...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// postJobsAnswer is POST /api/jobs/answer.
// Input: {"needs_you":N,"action":"...","text":"..."}
// Dispatches based on the card's kind and the chosen action.
func (s *Server) postJobsAnswer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		NeedsYou int64  `json:"needs_you"`
		Action   string `json:"action"`
		Text     string `json:"text"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()

	// Load the card.
	cards, err := s.Apply.OpenNeedsYouAll(ctx)
	if err != nil {
		reply(w, nil, err)
		return
	}
	var card apply.NeedsYou
	var found bool
	for _, c := range cards {
		if c.ID == in.NeedsYou {
			card = c
			found = true
			break
		}
	}
	if !found {
		reply(w, nil, httpError{code: http.StatusNotFound, msg: fmt.Sprintf("needs_you %d not found or not open", in.NeedsYou)})
		return
	}

	job, err := s.Apply.Get(ctx, card.JobID)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}

	// Load the card's step up front: a card that names a missing step is a 404,
	// and no message is ever sent with an empty step key.
	var step apply.JobStep
	if card.StepID != 0 {
		step, err = s.Apply.GetStep(ctx, card.StepID)
		if err != nil {
			reply(w, nil, httpError{code: http.StatusNotFound, msg: err.Error()})
			return
		}
	}

	switch card.Kind {
	case "batch":
		card, err = s.answerBatch(ctx, card, job, in.Action)
	case "text":
		card, err = s.answerText(ctx, card, job, step, in.Action, in.Text)
	case "failed":
		card, err = s.answerFailed(ctx, card, job, step, in.Action)
	case "paused":
		card, err = s.answerPaused(ctx, card, job, step, in.Action)
	default:
		reply(w, nil, bad("unknown card kind %q", card.Kind))
		return
	}
	if err != nil {
		var he httpError
		if errors.As(err, &he) {
			reply(w, nil, he)
		} else {
			reply(w, nil, bad("%v", err))
		}
		return
	}

	// After any card answer, check whether the job has reached a terminal state.
	// This handles skip-batch (agent steps skipped), skip (step skipped or failed
	// step's card closed), and any other action that may drain remaining work.
	s.settleJob(ctx, card.JobID)
	s.Bus.Publish(ctx, "needs_you", card)
	reply(w, AnswerResult{NeedsYou: card}, nil)
}

// answerBatch handles batch card actions: confirm | skip-batch.
func (s *Server) answerBatch(ctx context.Context, card apply.NeedsYou, job apply.Job, action string) (apply.NeedsYou, error) {
	switch action {
	case "confirm":
		// Tell the agent the batch is confirmed and it may proceed.
		if job.Session != "" {
			body := fmt.Sprintf("Batch confirmed for job %d. You may proceed with all steps.", job.ID)
			thread, err := s.Queue.NewThread(ctx, job.Session, fmt.Sprintf("batch:job:%d", job.ID))
			if err != nil {
				return card, fmt.Errorf("batch confirm thread: %w", err)
			}
			if _, err := s.Queue.Post(ctx, thread.ID, body, deliver.Attached{Job: strconv.FormatInt(job.ID, 10)}, false); err != nil {
				return card, fmt.Errorf("batch confirm post: %w", err)
			}
			s.wake(job.Session)
		}
		return s.Apply.AnswerNeedsYou(ctx, card.ID, "confirm")
	case "skip-batch":
		// Skip all pending agent-lane steps for this job.
		for _, st := range job.Steps {
			if st.Lane == apply.LaneAgent && st.State == apply.StepPending {
				_ = s.Apply.SetStepState(ctx, st.ID, apply.StepSkipped, "batch skipped")
				s.Bus.Publish(ctx, "step", map[string]any{"id": st.ID, "job_id": job.ID, "state": apply.StepSkipped})
			}
		}
		return s.Apply.AnswerNeedsYou(ctx, card.ID, "skip-batch")
	default:
		return card, httpError{code: http.StatusBadRequest, msg: fmt.Sprintf("batch: unknown action %q (confirm | skip-batch)", action)}
	}
}

// answerText handles text card actions: post-and-close | close-without-comment | edit-text | skip.
func (s *Server) answerText(ctx context.Context, card apply.NeedsYou, job apply.Job, step apply.JobStep, action, text string) (apply.NeedsYou, error) {
	switch action {
	case "post-and-close":
		// Record the approved text on the step.
		if card.StepID != 0 {
			if err := s.Apply.SetStepText(ctx, card.StepID, text); err != nil {
				return card, fmt.Errorf("SetStepText: %w", err)
			}
			s.Bus.Publish(ctx, "step", map[string]any{"id": card.StepID, "job_id": job.ID, "text_set": true})
		}
		// Send a message to the job's session telling the agent to post exactly
		// that text and close. Court confirms; casebook never runs gh itself.
		if job.Session != "" {
			body := fmt.Sprintf(
				"Court approved text for job %d step %d [%s].\n"+
					"Post this exact text and close:\n\n%s",
				job.ID, card.StepID, step.Key, text)
			thread, err := s.findOrCreateJobThread(ctx, job)
			if err != nil {
				return card, fmt.Errorf("find job thread: %w", err)
			}
			if _, err := s.Queue.Post(ctx, thread, body, deliver.Attached{Job: strconv.FormatInt(job.ID, 10)}, false); err != nil {
				return card, fmt.Errorf("post-and-close message: %w", err)
			}
			s.wake(job.Session)
		}
		return s.Apply.AnswerNeedsYou(ctx, card.ID, "post-and-close")
	case "close-without-comment":
		// Tell the agent to close without a comment.
		if job.Session != "" {
			body := fmt.Sprintf(
				"Court approved close-without-comment for job %d step %d [%s].\n"+
					"Close it without posting any comment.",
				job.ID, card.StepID, step.Key)
			thread, err := s.findOrCreateJobThread(ctx, job)
			if err != nil {
				return card, fmt.Errorf("find job thread: %w", err)
			}
			if _, err := s.Queue.Post(ctx, thread, body, deliver.Attached{Job: strconv.FormatInt(job.ID, 10)}, false); err != nil {
				return card, fmt.Errorf("close-without-comment message: %w", err)
			}
			s.wake(job.Session)
		}
		return s.Apply.AnswerNeedsYou(ctx, card.ID, "close-without-comment")
	case "edit-text":
		// Update the draft on the card; the card stays open.
		if err := s.Apply.EditNeedsYouText(ctx, card.ID, text); err != nil {
			return card, fmt.Errorf("EditNeedsYouText: %w", err)
		}
		// Reload the card to return the updated state.
		allCards, _ := s.Apply.NeedsYouFor(ctx, job.ID)
		for _, c := range allCards {
			if c.ID == card.ID {
				return c, nil
			}
		}
		return card, nil
	case "skip":
		// Mark the step skipped and return the card as answered.
		if card.StepID != 0 {
			if err := s.Apply.SetStepState(ctx, card.StepID, apply.StepSkipped, "skipped by Court"); err != nil {
				return card, fmt.Errorf("SetStepState skipped: %w", err)
			}
			s.Bus.Publish(ctx, "step", map[string]any{"id": card.StepID, "job_id": job.ID, "state": apply.StepSkipped})
		}
		return s.Apply.AnswerNeedsYou(ctx, card.ID, "skip")
	default:
		return card, httpError{code: http.StatusBadRequest, msg: fmt.Sprintf("text: unknown action %q (post-and-close | close-without-comment | edit-text | skip)", action)}
	}
}

// answerFailed handles failed card actions: hand-to-agent | skip.
func (s *Server) answerFailed(ctx context.Context, card apply.NeedsYou, job apply.Job, step apply.JobStep, action string) (apply.NeedsYou, error) {
	switch action {
	case "hand-to-agent":
		// Only allowed if the job has a session.
		if job.Session == "" {
			return card, httpError{
				code: http.StatusConflict,
				msg:  fmt.Sprintf("job %d has no session; cannot hand to agent", job.ID),
			}
		}
		body := fmt.Sprintf(
			"casebook-lane step %d [%s] failed and Court handed it to you.\n"+
				"Reason: %s\n\n"+
				"Command that failed: %s\n"+
				"Please handle this step and report back with casebook_job_step.",
			step.ID, step.Key, card.Question, step.Command)
		thread, err := s.findOrCreateJobThread(ctx, job)
		if err != nil {
			return card, fmt.Errorf("find job thread for hand-to-agent: %w", err)
		}
		if _, err := s.Queue.Post(ctx, thread, body, deliver.Attached{Job: strconv.FormatInt(job.ID, 10)}, false); err != nil {
			return card, fmt.Errorf("hand-to-agent message: %w", err)
		}
		s.wake(job.Session)
		return s.Apply.AnswerNeedsYou(ctx, card.ID, "hand-to-agent")
	case "skip":
		return s.Apply.AnswerNeedsYou(ctx, card.ID, "skip")
	default:
		return card, httpError{code: http.StatusBadRequest, msg: fmt.Sprintf("failed: unknown action %q (hand-to-agent | skip)", action)}
	}
}

// answerPaused handles paused card actions: resume | skip.
//
// resume is lane-aware: a casebook-lane step goes back to pending and its lane
// is relaunched through the single-flight guard; an agent-lane step gets a
// message to the job's session telling it to resume that step (the agent moves
// the step itself when it starts again).
func (s *Server) answerPaused(ctx context.Context, card apply.NeedsYou, job apply.Job, step apply.JobStep, action string) (apply.NeedsYou, error) {
	switch action {
	case "resume":
		if card.StepID != 0 {
			if step.Lane == apply.LaneCasebook {
				if err := s.Apply.SetStepState(ctx, card.StepID, apply.StepPending, ""); err != nil {
					return card, fmt.Errorf("requeue paused step: %w", err)
				}
				s.Bus.Publish(ctx, "step", map[string]any{"id": card.StepID, "job_id": job.ID, "state": apply.StepPending})
				s.startCasebookLane(job)
			} else if job.Session != "" {
				body := fmt.Sprintf(
					"Court resumed job %d step %d [%s].\n"+
						"Resume that step: run it now, following the usual protocol.",
					job.ID, step.ID, step.Key)
				thread, err := s.findOrCreateJobThread(ctx, job)
				if err != nil {
					return card, fmt.Errorf("find job thread for resume: %w", err)
				}
				if _, err := s.Queue.Post(ctx, thread, body, deliver.Attached{Job: strconv.FormatInt(job.ID, 10)}, false); err != nil {
					return card, fmt.Errorf("resume message: %w", err)
				}
				s.wake(job.Session)
			}
		}
		return s.Apply.AnswerNeedsYou(ctx, card.ID, "resume")
	case "skip":
		if card.StepID != 0 {
			if err := s.Apply.SetStepState(ctx, card.StepID, apply.StepSkipped, "skipped by Court"); err != nil {
				return card, fmt.Errorf("skip paused step: %w", err)
			}
			s.Bus.Publish(ctx, "step", map[string]any{"id": card.StepID, "job_id": job.ID, "state": apply.StepSkipped})
		}
		return s.Apply.AnswerNeedsYou(ctx, card.ID, "skip")
	default:
		return card, httpError{code: http.StatusBadRequest, msg: fmt.Sprintf("paused: unknown action %q (resume | skip)", action)}
	}
}

// findOrCreateJobThread finds the job's existing thread (created by
// dispatchAgentJob) or creates a new one if the job was dispatched without a
// thread (no-session jobs). Returns the thread ID.
func (s *Server) findOrCreateJobThread(ctx context.Context, job apply.Job) (int64, error) {
	if job.Session == "" {
		return 0, fmt.Errorf("job %d has no session", job.ID)
	}
	// The dispatch thread is named "job:<id>".
	threadName := "job:" + strconv.FormatInt(job.ID, 10)
	threads, err := s.Queue.Threads(ctx, job.Session)
	if err != nil {
		return 0, err
	}
	for _, t := range threads {
		if t.Name == threadName {
			return t.ID, nil
		}
	}
	// Create a new thread for this job's follow-up messages.
	t, err := s.Queue.NewThread(ctx, job.Session, threadName)
	if err != nil {
		return 0, err
	}
	return t.ID, nil
}
