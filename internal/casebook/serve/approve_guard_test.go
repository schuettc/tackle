package serve

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apply"
)

// TestApproveOutwardStepsNeedsAPresentSession: a plan with agent-lane steps
// (archiving repos, closing PRs) goes to a session Court names, so serve
// refuses to approve it with no session (the store's check), or with a
// session that has left (serve's: named but not present);
// the plan stays planned and nothing is dispatched. A plan of local steps
// alone still approves without one.
func TestApproveOutwardStepsNeedsAPresentSession(t *testing.T) {
	r := newRig(t)
	job, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
	if err != nil {
		t.Fatal(err)
	}

	var out map[string]any
	if c := r.do(t, "POST", "/api/apply/approve", map[string]any{"plan_id": job.ID}, &out); c != http.StatusBadRequest {
		t.Fatalf("approve outward steps with no session: %d %v, want 400", c, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "session") {
		t.Errorf("refusal %q doesn't say a session is needed", msg)
	}

	// A session that has gone quiet (left) is not present.
	r.attach(t, "gone")
	r.s.Queue.LeftAfter = 50 * time.Millisecond
	time.Sleep(120 * time.Millisecond)
	out = nil
	if c := r.do(t, "POST", "/api/apply/approve", map[string]any{"plan_id": job.ID, "session": "gone"}, &out); c != http.StatusConflict {
		t.Fatalf("approve with a left session: %d %v, want 409", c, out)
	}
	j, _ := r.s.Apply.Get(ctx, job.ID)
	if j.State != apply.JobPlanned || j.Session != "" {
		t.Fatalf("after the refusals the job is %s (session %q), want planned with none", j.State, j.Session)
	}
	if cards, _ := r.s.Apply.NeedsYouFor(ctx, job.ID); len(cards) != 0 {
		t.Fatalf("a refused approve opened cards: %+v", cards)
	}

	// Present again: it approves.
	r.s.Queue.LeftAfter = time.Minute
	r.attach(t, "gone")
	if c := r.do(t, "POST", "/api/apply/approve", map[string]any{"plan_id": job.ID, "session": "gone"}, nil); c != http.StatusOK {
		t.Fatalf("approve with a present session: %d, want 200", c)
	}

	// Local steps only: no session needed.
	local := apply.Plan{BuiltAt: time.Now(), Head: "h", Steps: []apply.Step{{
		Key: "branch:schuettc/hail@feat/z", Action: "branch-delete-local", Lane: apply.LaneCasebook,
		Command:     "git -C '/nowhere' update-ref -d 'refs/heads/feat/z' " + strings.Repeat("a", 40),
		ExpectedTip: strings.Repeat("a", 40), Precondition: "branch-tip-unchanged-and-landed",
	}}}
	lj, err := r.s.Apply.Create(ctx, local, "mbp")
	if err != nil {
		t.Fatal(err)
	}
	if c := r.do(t, "POST", "/api/apply/approve", map[string]any{"plan_id": lj.ID}, nil); c != http.StatusOK {
		t.Fatalf("approve local steps with no session: %d, want 200", c)
	}
}
