package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apply"
)

// TestCasebookLaneAnnouncesItsSteps: the page follows a job live (spec §3.2,
// the To apply section), so the casebook lane announces what it does: the
// job going to running, each step's state as it moves (running, reported,
// verified; a failure with the needs-you card it opens), in that order.
// Without it a job of 849 local deletions shows nothing until it settles.
func TestCasebookLaneAnnouncesItsSteps(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	tip := strings.Repeat("a", 40)

	// feat/gone is already gone (verified, no command); feat/bad is in a
	// clone that doesn't exist (the precondition fails: skipped); feat/x's
	// command fails (failed, with a card).
	r.s.Runner.RunGit = func(_ context.Context, _ string, args ...string) (string, error) {
		switch {
		case len(args) >= 2 && args[0] == "rev-parse" && args[1] == "--git-dir":
			return ".git", nil
		case len(args) >= 3 && args[0] == "rev-parse" && args[1] == "--verify" && strings.HasSuffix(args[2], "feat/gone"):
			return "", errors.New("no such ref")
		case len(args) >= 3 && args[0] == "rev-parse" && args[1] == "--verify":
			return tip, nil
		case len(args) >= 1 && args[0] == "merge-base":
			return "", nil
		case len(args) >= 1 && args[0] == "update-ref":
			return "", errors.New("boom")
		}
		return "", nil
	}
	mk := func(clone, branch string) apply.Step {
		return apply.Step{
			Key:          "branch:schuettc/hail@" + branch,
			Action:       "branch-delete-local",
			Lane:         apply.LaneCasebook,
			Command:      "git -C '" + clone + "' update-ref -d 'refs/heads/" + branch + "' " + tip,
			ExpectedTip:  tip,
			Precondition: "branch-tip-unchanged-and-landed",
		}
	}
	plan := apply.Plan{BuiltAt: time.Now(), Head: "h", Steps: []apply.Step{
		mk(dir, "feat/gone"), mk(dir+"/missing", "feat/bad"),
	}}
	job, err := r.s.Apply.Create(ctx, plan, "mbp")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := r.s.Bus.Head(ctx)
	if c := r.do(t, "POST", "/api/apply/approve", map[string]any{"plan_id": job.ID}, nil); c != http.StatusOK {
		t.Fatalf("approve: %d", c)
	}

	type stepEv struct {
		ID    int64  `json:"id"`
		JobID int64  `json:"job_id"`
		State string `json:"state"`
	}
	want := []string{
		"job running",
		"step 0 running", "step 0 reported", "step 0 verified",
		"step 1 running", "step 1 skipped",
		"job done",
	}
	idx := map[int64]int{job.Steps[0].ID: 0, job.Steps[1].ID: 1}
	var got []string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		evs, _, err := r.s.Bus.Since(ctx, before, 100)
		if err != nil {
			t.Fatal(err)
		}
		got = got[:0]
		for _, e := range evs {
			var v stepEv
			_ = json.Unmarshal(e.Data, &v)
			switch e.Type {
			case "job":
				if v.ID == job.ID && v.State != apply.JobApproved {
					got = append(got, "job "+v.State)
				}
			case "step":
				if v.JobID == job.ID {
					got = append(got, "step "+string(rune('0'+idx[v.ID]))+" "+v.State)
				}
			}
		}
		if len(got) >= len(want) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Fatalf("events:\n got  %s\n want %s", strings.Join(got, ", "), strings.Join(want, ", "))
	}
}

// TestCasebookLaneAnnouncesAFailedStepsCard: a casebook-lane step that fails
// opens a needs-you card, and the page hears of both.
func TestCasebookLaneAnnouncesAFailedStepsCard(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	tip := strings.Repeat("b", 40)
	r.s.Runner.RunGit = func(_ context.Context, _ string, args ...string) (string, error) {
		switch {
		case len(args) >= 3 && args[0] == "rev-parse" && args[1] == "--verify":
			return tip, nil
		case len(args) >= 1 && args[0] == "update-ref":
			return "", errors.New("boom")
		}
		return "", nil
	}
	// Landed: the tip is an ancestor of main (merge-base --is-ancestor ok).
	plan := apply.Plan{BuiltAt: time.Now(), Head: "h", Steps: []apply.Step{{
		Key:          "branch:schuettc/hail@feat/x",
		Action:       "branch-delete-local",
		Lane:         apply.LaneCasebook,
		Command:      "git -C '" + dir + "' update-ref -d 'refs/heads/feat/x' " + tip,
		ExpectedTip:  tip,
		Precondition: "branch-tip-unchanged-and-landed",
	}}}
	job, err := r.s.Apply.Create(ctx, plan, "mbp")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := r.s.Bus.Head(ctx)
	if c := r.do(t, "POST", "/api/apply/approve", map[string]any{"plan_id": job.ID}, nil); c != http.StatusOK {
		t.Fatalf("approve: %d", c)
	}
	var failed, card bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (!failed || !card) {
		evs, _, _ := r.s.Bus.Since(ctx, before, 100)
		for _, e := range evs {
			var v struct {
				ID    int64  `json:"id"`
				JobID int64  `json:"job_id"`
				State string `json:"state"`
				Kind  string `json:"kind"`
			}
			_ = json.Unmarshal(e.Data, &v)
			if e.Type == "step" && v.ID == job.Steps[0].ID && v.State == apply.StepFailed {
				failed = true
			}
			if e.Type == "needs_you" && v.JobID == job.ID && v.Kind == "failed" && v.State == "open" {
				card = true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !failed || !card {
		t.Fatalf("failed step announced %v, its card announced %v", failed, card)
	}
}
