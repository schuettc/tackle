package serve

// Round-2 fix tests.
//
//   TestSessionLeftCrossingsPublishEvent: a sessions event is published
//     after a session crosses the left threshold; exactly one per crossing.
//   TestSessionReturnFromLeftPublishesEvent: when a left session heartbeats,
//     the next checkLeftCrossings call sees it return and publishes.

import (
	"testing"
	"time"
)

// TestSessionLeftCrossingsPublishEvent verifies that checkLeftCrossings emits
// exactly one "sessions" event when a session first crosses the left threshold,
// and no further events on subsequent ticks where the state hasn't changed.
func TestSessionLeftCrossingsPublishEvent(t *testing.T) {
	r := newRig(t)
	r.s.Queue.LeftAfter = 50 * time.Millisecond

	// Register a session.
	r.attach(t, "s1")

	// Wait past the LeftAfter threshold without sending another heartbeat.
	time.Sleep(100 * time.Millisecond)

	// Record bus cursor before the first check.
	cursorBefore, err := r.s.Bus.Head(ctx)
	if err != nil {
		t.Fatalf("Bus.Head: %v", err)
	}

	// Simulate the first watch tick after the crossing.
	knownLeft := map[string]bool{}
	changed := r.s.checkLeftCrossings(ctx, knownLeft)
	if !changed {
		t.Error("expected checkLeftCrossings to detect the crossing; got changed=false")
	}
	if !knownLeft["s1"] {
		t.Error("expected s1 to be recorded as left in knownLeft")
	}

	// A sessions event must have been published.
	events, _, err := r.s.Bus.Since(ctx, cursorBefore, 50)
	if err != nil {
		t.Fatalf("Bus.Since: %v", err)
	}
	sessCount := 0
	for _, e := range events {
		if e.Type == "sessions" {
			sessCount++
		}
	}
	if sessCount == 0 {
		t.Error("expected at least one sessions event after left crossing, got none")
	}

	// Second tick: state hasn't changed — no new event.
	cursorAfter, _ := r.s.Bus.Head(ctx)
	changed2 := r.s.checkLeftCrossings(ctx, knownLeft)
	if changed2 {
		t.Error("expected no change on second tick with same left state")
	}
	events2, _, _ := r.s.Bus.Since(ctx, cursorAfter, 50)
	for _, e := range events2 {
		if e.Type == "sessions" {
			t.Errorf("unexpected sessions event on second tick: %+v", e)
		}
	}
}

// TestSessionReturnFromLeftPublishesEvent verifies that when a left session
// returns (sends a heartbeat), the next checkLeftCrossings detects the
// transition back to not-left and publishes one sessions event.
func TestSessionReturnFromLeftPublishesEvent(t *testing.T) {
	r := newRig(t)
	r.s.Queue.LeftAfter = 50 * time.Millisecond

	r.attach(t, "s1")
	time.Sleep(100 * time.Millisecond)

	// First crossing: s1 goes left.
	knownLeft := map[string]bool{}
	r.s.checkLeftCrossings(ctx, knownLeft)
	if !knownLeft["s1"] {
		t.Fatal("s1 not left after first crossing check")
	}

	// Session returns: send a fresh heartbeat (Touch resets last_seen).
	if c := r.do(t, "POST", "/api/agent/presence",
		map[string]any{"id": "s1", "harness": "pi", "label": "pi · ws", "cwd": "/w", "pid": 1}, nil); c != 200 {
		t.Fatalf("presence %d", c)
	}

	// Record cursor after the heartbeat (agentPresence already published
	// a sessions event; we want to see the one from checkLeftCrossings).
	cursorAfterHB, _ := r.s.Bus.Head(ctx)

	// Second check: s1 is no longer left → must publish.
	changed := r.s.checkLeftCrossings(ctx, knownLeft)
	if !changed {
		t.Error("expected checkLeftCrossings to detect the return; got changed=false")
	}
	if knownLeft["s1"] {
		t.Error("expected s1 to be removed from knownLeft after return")
	}

	events, _, _ := r.s.Bus.Since(ctx, cursorAfterHB, 50)
	sessCount := 0
	for _, e := range events {
		if e.Type == "sessions" {
			sessCount++
		}
	}
	if sessCount == 0 {
		t.Error("expected a sessions event after session returned from left, got none")
	}
}
