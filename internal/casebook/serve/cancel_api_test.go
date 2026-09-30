package serve

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/schuettc/tackle/internal/casebook/apply"
)

// TestCancelDiscardsAnUnapprovedPlan: POST /api/apply/cancel {plan_id}
// discards a plan Court hasn't approved (Store.Cancel: planned → cancelled)
// and announces it; an approved job is not a plan and is refused (409), as
// is an unknown one (404).
func TestCancelDiscardsAnUnapprovedPlan(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	plan, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := r.s.Bus.Head(ctx)
	var jv JobView
	if c := r.do(t, "POST", "/api/apply/cancel", map[string]any{"plan_id": plan.ID}, &jv); c != http.StatusOK {
		t.Fatalf("cancel a plan: %d, want 200", c)
	}
	if jv.Job.ID != plan.ID || jv.Job.State != apply.JobCancelled {
		t.Fatalf("cancel replied %d %s, want job %d cancelled", jv.Job.ID, jv.Job.State, plan.ID)
	}
	if j, _ := r.s.Apply.Get(ctx, plan.ID); j.State != apply.JobCancelled {
		t.Fatalf("serve has the plan %s, want cancelled", j.State)
	}
	evs, _, _ := r.s.Bus.Since(ctx, before, 100)
	var announced bool
	for _, e := range evs {
		var d struct {
			ID    int64  `json:"id"`
			State string `json:"state"`
		}
		_ = json.Unmarshal(e.Data, &d)
		if e.Type == "job" && d.ID == plan.ID && d.State == string(apply.JobCancelled) {
			announced = true
		}
	}
	if !announced {
		t.Errorf("no job event says the plan was cancelled; events %+v", evs)
	}
	// Once more: it is no longer a plan.
	if c := r.do(t, "POST", "/api/apply/cancel", map[string]any{"plan_id": plan.ID}, nil); c != http.StatusConflict {
		t.Errorf("cancel a cancelled plan: %d, want 409", c)
	}

	// An approved job can't be discarded (pause it instead).
	job, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
	if err != nil {
		t.Fatal(err)
	}
	if c := r.do(t, "POST", "/api/apply/approve", map[string]any{"plan_id": job.ID, "session": "s1"}, nil); c != http.StatusOK {
		t.Fatalf("approve: %d", c)
	}
	if c := r.do(t, "POST", "/api/apply/cancel", map[string]any{"plan_id": job.ID}, nil); c != http.StatusConflict {
		t.Errorf("cancel an approved job: %d, want 409", c)
	}
	if j, _ := r.s.Apply.Get(ctx, job.ID); j.State == apply.JobCancelled {
		t.Error("an approved job was cancelled")
	}
	if c := r.do(t, "POST", "/api/apply/cancel", map[string]any{"plan_id": 9999}, nil); c != http.StatusNotFound {
		t.Errorf("cancel an unknown plan: %d, want 404", c)
	}
}
