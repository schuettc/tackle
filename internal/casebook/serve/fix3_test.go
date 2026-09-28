package serve

// Task 13 fix tests.
//
//   TestWaitersPrunedOnReap: waiters map is pruned after agentSettled.

import (
	"testing"
)

// TestWaitersPrunedOnReap verifies that the waiters map entry for a session is
// removed when the agent calls POST /api/agent/settled (the turn reap point).
// Before the fix, waiters accumulated unboundedly; after the fix, a settled
// session's entry is deleted so the next agentWait starts fresh.
func TestWaitersPrunedOnReap(t *testing.T) {
	r := newRig(t)
	th := r.attach(t, "s1")
	r.send(t, th, "hello", false)

	// agentWait creates the waiter and returns the delivery immediately (a
	// message is already queued).
	var w waited
	if code := r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w); code != 200 {
		t.Fatalf("agentWait: got %d", code)
	}
	if w.Delivery.ID == 0 {
		t.Fatal("agentWait returned no delivery")
	}

	// The waiter must exist now (created by agentWait).
	r.s.mu.Lock()
	_, exists := r.s.waiters["s1"]
	r.s.mu.Unlock()
	if !exists {
		t.Fatal("expected waiter to exist after agentWait")
	}

	// Settle the turn.
	if code := r.do(t, "POST", "/api/agent/settled", map[string]any{
		"session": "s1",
		"shown":   []int64{w.Delivery.ID},
	}, nil); code != 200 {
		t.Fatalf("agentSettled: got %d", code)
	}

	// After the fix the waiter must be gone.
	r.s.mu.Lock()
	_, stillThere := r.s.waiters["s1"]
	r.s.mu.Unlock()
	if stillThere {
		t.Error("waiter for s1 was not pruned after agentSettled")
	}
}
