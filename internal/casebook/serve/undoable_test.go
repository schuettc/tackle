package serve

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apply"
)

// TestAStepSaysWhetherItCanBeUndone: "undo appears on a finished step when
// its restore command is automatic" (spec §5.5). serve decides that (the
// same rule POST /api/jobs/undo applies), and says so on the step, so the
// page shows undo exactly where serve would run it and nowhere else.
func TestAStepSaysWhetherItCanBeUndone(t *testing.T) {
	r := newRig(t)
	r.s.Runner.RunGit = func(_ context.Context, _ string, _ ...string) (string, error) { return "", nil }

	mk := func(branch string) apply.Step {
		return apply.Step{
			Key:     "branch:schuettc/hail@" + branch,
			Action:  "branch-delete-local",
			Lane:    apply.LaneCasebook,
			Command: "git -C '/tmp/clone' update-ref -d refs/heads/" + branch + " abc",
		}
	}
	plan := apply.Plan{BuiltAt: time.Now(), Head: "h", Steps: []apply.Step{mk("feat/auto"), mk("feat/none"), mk("feat/pending")}}
	job, err := r.s.Apply.Create(ctx, plan, "mbp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.Apply.Approve(ctx, job.ID, ""); err != nil {
		t.Fatal(err)
	}
	verify := func(st apply.JobStep) {
		for _, s := range []string{apply.StepRunning, apply.StepReported, apply.StepVerified} {
			if err := r.s.Apply.SetStepState(ctx, st.ID, s, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	auto, none, pending := job.Steps[0], job.Steps[1], job.Steps[2]
	verify(auto)
	verify(none)
	if err := r.s.Apply.SetStepRestore(ctx, auto.ID, "git -C '/tmp/clone' branch feat/auto abc"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.Apply.SetStepRestore(ctx, pending.ID, "git -C '/tmp/clone' branch feat/pending abc"); err != nil {
		t.Fatal(err)
	}

	undoable := func() map[int64]bool {
		var jv JobView
		if c := r.do(t, "GET", "/api/job?id="+strconv.FormatInt(job.ID, 10), nil, &jv); c != http.StatusOK {
			t.Fatalf("job: %d", c)
		}
		out := map[int64]bool{}
		for _, st := range jv.Job.Steps {
			out[st.ID] = st.Undoable
		}
		return out
	}
	got := undoable()
	if !got[auto.ID] || got[none.ID] || got[pending.ID] {
		t.Fatalf("undoable: auto %v (want true), no restore %v (want false), not finished %v (want false)",
			got[auto.ID], got[none.ID], got[pending.ID])
	}
	var jobs JobsView
	r.do(t, "GET", "/api/jobs", nil, &jobs)
	for _, j := range jobs.Jobs {
		for _, st := range j.Steps {
			if st.ID == auto.ID && !st.Undoable {
				t.Error("GET /api/jobs doesn't say the step can be undone")
			}
		}
	}

	if c := r.do(t, "POST", "/api/jobs/undo", map[string]any{"step": auto.ID}, nil); c != http.StatusOK {
		t.Fatalf("undo: %d", c)
	}
	if undoable()[auto.ID] {
		t.Error("an undone step still says it can be undone")
	}
}
