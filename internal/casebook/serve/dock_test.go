package serve

import (
	"net/http"
	"testing"
	"time"
)

// TestSessionQueuedCount verifies that GET /api/sessions returns the queued
// message count on each session. When Court has queued messages waiting for the
// agent's next turn, that count appears on the session row.
func TestSessionQueuedCount(t *testing.T) {
	r := newRig(t)
	// Register two sessions.
	th1 := r.attach(t, "s1") // creates session s1 and a thread
	r.attach(t, "s2")        // session s2 (no messages)

	// Post two queued messages on s1's thread.
	r.send(t, th1, "first", false)
	r.send(t, th1, "second", false)

	var sv SessionsView
	if c := r.do(t, "GET", "/api/sessions", nil, &sv); c != http.StatusOK {
		t.Fatalf("sessions %d", c)
	}
	// Find s1 in the list.
	var found bool
	for _, s := range sv.Sessions {
		if s.ID == "s1" {
			found = true
			if s.Queued != 2 {
				t.Errorf("s1 queued: got %d, want 2", s.Queued)
			}
		}
		if s.ID == "s2" {
			if s.Queued != 0 {
				t.Errorf("s2 queued: got %d, want 0", s.Queued)
			}
		}
	}
	if !found {
		t.Error("s1 not in sessions response")
	}
}

// TestGetSessionDeliveryNoInflight verifies that GET /api/session/delivery
// returns {delivery: null} when no delivery is in flight.
func TestGetSessionDeliveryNoInflight(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")

	var dv DeliveryView
	if c := r.do(t, "GET", "/api/session/delivery?session=s1", nil, &dv); c != http.StatusOK {
		t.Fatalf("session/delivery %d", c)
	}
	if dv.Delivery != nil {
		t.Errorf("expected null delivery, got %+v", dv.Delivery)
	}
}

// TestGetSessionDeliveryInflight verifies that GET /api/session/delivery
// returns the in-flight delivery when one exists, including the stuck flag
// when the CASEBOOK_STUCK_AFTER threshold has elapsed.
func TestGetSessionDeliveryInflight(t *testing.T) {
	r := newRig(t)
	th := r.attach(t, "s1")

	// Post a message and pick it up (makes it inflight).
	r.send(t, th, "hello", false)
	var w waited
	if c := r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w); c != http.StatusOK {
		t.Fatalf("wait %d", c)
	}

	// GET /api/session/delivery should now return the delivery.
	var dv DeliveryView
	if c := r.do(t, "GET", "/api/session/delivery?session=s1", nil, &dv); c != http.StatusOK {
		t.Fatalf("session/delivery %d", c)
	}
	if dv.Delivery == nil {
		t.Fatal("expected in-flight delivery, got null")
	}
	if dv.Delivery.ID != w.Delivery.ID {
		t.Errorf("delivery id: got %d, want %d", dv.Delivery.ID, w.Delivery.ID)
	}
	if dv.Delivery.Stuck {
		t.Error("delivery should not be stuck yet")
	}
}

// TestGetSessionDeliveryStuck verifies that GET /api/session/delivery sets
// Stuck=true when the queue's StuckAfter threshold has elapsed. The
// CASEBOOK_STUCK_AFTER environment variable (parsed in New) controls this
// threshold so tests and probes can use a shorter duration.
func TestGetSessionDeliveryStuck(t *testing.T) {
	r := newRig(t)
	// Use a very short stuck threshold.
	r.s.Queue.StuckAfter = 50 * time.Millisecond
	th := r.attach(t, "s1")

	r.send(t, th, "stuck message", false)
	var w waited
	if c := r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w); c != http.StatusOK {
		t.Fatalf("wait %d", c)
	}

	// Advance past the short stuck threshold.
	time.Sleep(100 * time.Millisecond)

	var dv DeliveryView
	if c := r.do(t, "GET", "/api/session/delivery?session=s1", nil, &dv); c != http.StatusOK {
		t.Fatalf("session/delivery %d", c)
	}
	if dv.Delivery == nil {
		t.Fatal("expected in-flight delivery")
	}
	if !dv.Delivery.Stuck {
		t.Errorf("delivery should be stuck after %v, touched_at=%v", r.s.Queue.StuckAfter, dv.Delivery.TouchedAt)
	}
}

// TestSessionQueuedCountDropsAfterDelivery verifies that the queued count
// drops to zero after the agent receives the delivery (messages move from
// 'queued' to 'delivered').
func TestSessionQueuedCountDropsAfterDelivery(t *testing.T) {
	r := newRig(t)
	th := r.attach(t, "s1")
	r.send(t, th, "pick me up", false)

	// Confirm queued count is 1.
	var sv SessionsView
	r.do(t, "GET", "/api/sessions", nil, &sv)
	for _, s := range sv.Sessions {
		if s.ID == "s1" && s.Queued != 1 {
			t.Errorf("before pick-up: queued %d want 1", s.Queued)
		}
	}

	// Agent picks up the delivery.
	r.do(t, "GET", "/api/agent/wait?session=s1", nil, nil)

	// Now queued count should be 0 (messages are 'delivered', not 'queued').
	r.do(t, "GET", "/api/sessions", nil, &sv)
	for _, s := range sv.Sessions {
		if s.ID == "s1" && s.Queued != 0 {
			t.Errorf("after pick-up: queued %d want 0", s.Queued)
		}
	}
}
