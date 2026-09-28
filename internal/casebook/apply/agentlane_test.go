package apply

import (
	"context"
	"errors"
	"testing"

	"github.com/schuettc/tackle/internal/casebook/observe"
)

// staticGh is a test fake that returns a fixed response for every gh call.
type staticGh struct {
	out []byte
	err error
}

func (g staticGh) Gh(_ context.Context, _ ...string) ([]byte, error) {
	return g.out, g.err
}

var _ observe.Runner = staticGh{}

func TestObserveGhPRClose(t *testing.T) {
	step := JobStep{Key: "pr:schuettc/hail#7", Action: "pr-close"}
	gh := staticGh{out: []byte(`{"state":"CLOSED"}`)}
	obs := ObserveGh(context.Background(), step, gh)
	if !obs.Known {
		t.Fatal("ObserveGh: observation should be known")
	}
	if obs.State != "CLOSED" {
		t.Fatalf("ObserveGh: state = %q, want CLOSED", obs.State)
	}
	if s := Verify(step, obs); s != StepVerified {
		t.Fatalf("Verify: %q, want %q", s, StepVerified)
	}
}

func TestObserveGhPRMerge(t *testing.T) {
	step := JobStep{Key: "pr:schuettc/hail#7", Action: "pr-merge"}
	gh := staticGh{out: []byte(`{"state":"MERGED"}`)}
	obs := ObserveGh(context.Background(), step, gh)
	if !obs.Known || obs.State != "MERGED" {
		t.Fatalf("ObserveGh: %+v", obs)
	}
	if s := Verify(step, obs); s != StepVerified {
		t.Fatalf("Verify: %q", s)
	}
}

func TestObserveGhIssueClose(t *testing.T) {
	step := JobStep{Key: "issue:schuettc/hail#4", Action: "issue-close"}
	gh := staticGh{out: []byte(`{"state":"CLOSED"}`)}
	obs := ObserveGh(context.Background(), step, gh)
	if !obs.Known || obs.State != "CLOSED" {
		t.Fatalf("ObserveGh: %+v", obs)
	}
	if s := Verify(step, obs); s != StepVerified {
		t.Fatalf("Verify: %q", s)
	}
}

func TestObserveGhRepoArchive(t *testing.T) {
	step := JobStep{Key: "repo:schuettc/hail", Action: "repo-archive"}
	gh := staticGh{out: []byte(`{"isArchived":true}`)}
	obs := ObserveGh(context.Background(), step, gh)
	if !obs.Known || !obs.Archived {
		t.Fatalf("ObserveGh: %+v", obs)
	}
	if s := Verify(step, obs); s != StepVerified {
		t.Fatalf("Verify: %q", s)
	}
}

func TestObserveGhRepoNotYetArchived(t *testing.T) {
	step := JobStep{Key: "repo:schuettc/hail", Action: "repo-archive"}
	gh := staticGh{out: []byte(`{"isArchived":false}`)}
	obs := ObserveGh(context.Background(), step, gh)
	if !obs.Known || obs.Archived {
		t.Fatalf("ObserveGh: %+v", obs)
	}
	// Not yet archived → inconclusive (StepReported).
	if s := Verify(step, obs); s != StepReported {
		t.Fatalf("Verify: %q, want %q", s, StepReported)
	}
}

func TestObserveGhRepoDeleteGone(t *testing.T) {
	step := JobStep{Key: "repo:schuettc/hail", Action: "repo-delete"}
	gh := staticGh{err: errors.New("not found")}
	obs := ObserveGh(context.Background(), step, gh)
	if !obs.Known || obs.Exists {
		t.Fatalf("ObserveGh delete gone: %+v", obs)
	}
	if s := Verify(step, obs); s != StepVerified {
		t.Fatalf("Verify delete: %q", s)
	}
}

func TestObserveGhRepoDeleteStillPresent(t *testing.T) {
	step := JobStep{Key: "repo:schuettc/hail", Action: "repo-delete"}
	gh := staticGh{out: []byte(`{"name":"hail"}`)}
	obs := ObserveGh(context.Background(), step, gh)
	if !obs.Known || !obs.Exists {
		t.Fatalf("ObserveGh delete still present: %+v", obs)
	}
	// Still present → drift (StepFailed).
	if s := Verify(step, obs); s != StepFailed {
		t.Fatalf("Verify: %q, want %q", s, StepFailed)
	}
}

func TestObserveGhNilRunner(t *testing.T) {
	step := JobStep{Key: "pr:schuettc/hail#7", Action: "pr-close"}
	obs := ObserveGh(context.Background(), step, nil)
	if obs.Known {
		t.Fatal("nil gh runner should return an unknown observation")
	}
}

func TestObserveGhUnknownAction(t *testing.T) {
	step := JobStep{Key: "branch:schuettc/hail@feat/x", Action: "branch-delete-local"}
	gh := staticGh{out: []byte(`{}`)}
	obs := ObserveGh(context.Background(), step, gh)
	// branch-delete-local is not an agent-lane action; observation is inconclusive.
	if obs.Known {
		t.Fatal("non-agent action should return unknown observation")
	}
}
