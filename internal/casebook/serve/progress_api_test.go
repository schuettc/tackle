package serve

import (
	"net/http"
	"testing"
)

// TestGetSessionProgressNone verifies that GET /api/session/progress returns
// {progress: null} when the session has no live progress line.
func TestGetSessionProgressNone(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")

	var pv SessionProgressView
	if c := r.do(t, "GET", "/api/session/progress?session=s1", nil, &pv); c != http.StatusOK {
		t.Fatalf("session/progress %d", c)
	}
	if pv.Progress != nil {
		t.Errorf("expected null progress, got %+v", pv.Progress)
	}
}

// TestGetSessionProgressWithLine verifies that GET /api/session/progress
// returns the current progress line after POST /api/agent/progress sets it.
func TestGetSessionProgressWithLine(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")

	// Set progress via the agent API.
	if c := r.do(t, "POST", "/api/agent/progress", map[string]any{
		"session": "s1",
		"text":    "checking CI on #671",
		"n":       2,
		"total":   4,
	}, nil); c != http.StatusOK {
		t.Fatalf("agent/progress %d", c)
	}

	var pv SessionProgressView
	if c := r.do(t, "GET", "/api/session/progress?session=s1", nil, &pv); c != http.StatusOK {
		t.Fatalf("session/progress %d", c)
	}
	if pv.Progress == nil {
		t.Fatal("expected progress, got null")
	}
	if pv.Progress.Text != "checking CI on #671" {
		t.Errorf("text: got %q, want %q", pv.Progress.Text, "checking CI on #671")
	}
	if pv.Progress.N != 2 {
		t.Errorf("n: got %d, want 2", pv.Progress.N)
	}
	if pv.Progress.Total != 4 {
		t.Errorf("total: got %d, want 4", pv.Progress.Total)
	}
}

// TestGetSessionProgressClearedOnSettled verifies that after POST
// /api/agent/settled, the session's progress is nil again.
func TestGetSessionProgressClearedOnSettled(t *testing.T) {
	r := newRig(t)
	th := r.attach(t, "s1")

	// Post a message so we can pick it up and settle it.
	r.send(t, th, "hello", false)

	if c := r.do(t, "POST", "/api/agent/progress", map[string]any{
		"session": "s1",
		"text":    "working on it",
	}, nil); c != http.StatusOK {
		t.Fatalf("agent/progress %d", c)
	}

	// Confirm progress is set.
	var pv1 SessionProgressView
	if c := r.do(t, "GET", "/api/session/progress?session=s1", nil, &pv1); c != http.StatusOK {
		t.Fatalf("session/progress before settle %d", c)
	}
	if pv1.Progress == nil {
		t.Fatal("expected progress before settle")
	}

	// Pick up the delivery.
	var w waited
	if c := r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w); c != http.StatusOK {
		t.Fatalf("wait %d", c)
	}

	// Settle the delivery.
	if c := r.do(t, "POST", "/api/agent/settled", map[string]any{
		"session": "s1",
		"shown":   []int64{w.Delivery.ID},
	}, nil); c != http.StatusOK {
		t.Fatalf("settled %d", c)
	}

	// After settle, progress should be cleared.
	var pv2 SessionProgressView
	if c := r.do(t, "GET", "/api/session/progress?session=s1", nil, &pv2); c != http.StatusOK {
		t.Fatalf("session/progress after settle %d", c)
	}
	if pv2.Progress != nil {
		t.Errorf("expected null progress after settle, got %+v", pv2.Progress)
	}
}

// TestGetSessionProgressMissingSession verifies that GET /api/session/progress
// without a session parameter returns 400.
func TestGetSessionProgressMissingSession(t *testing.T) {
	r := newRig(t)
	if c := r.do(t, "GET", "/api/session/progress", nil, nil); c != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing session, got %d", c)
	}
}
