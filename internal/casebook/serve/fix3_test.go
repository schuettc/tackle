package serve

// Task 13 fix tests.
//
//   TestWaitersPrunedOnReap: waiters map is pruned after agentSettled.

import (
	"net/http"
	"testing"
	"time"
)

// TestWaitersPrunedOnReap verifies that the waiters map entry for a session is
// removed when the agent calls POST /api/agent/settled (the turn reap point).
// Before the fix, waiters accumulated unboundedly; after the fix, a settled
// session's entry is deleted so the next agentWait starts fresh.
func TestWaitersPrunedOnReap(t *testing.T) {
	t.Run("sequential", func(t *testing.T) {
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
	})

	// concurrent: a long-poll (agentWait) blocking with no delivery while
	// agentSettled runs concurrently.  The blocked poll must return (woken by
	// agentSettled's wake() or by its own timeout), and a message sent
	// afterward must still reach a subsequent fresh poll for the same session.
	t.Run("concurrent", func(t *testing.T) {
		r := newRig(t)
		r.s.Wait = 500 * time.Millisecond // shorten timeout so the test is fast
		th := r.attach(t, "s2")

		// Start a long-poll with no delivery queued – it will block.
		// Use ?timeout=1 so the poll can't outlive the test by more than 1 s.
		pollDone := make(chan int, 1)
		go func() {
			var w waited
			code := r.do(t, "GET", "/api/agent/wait?session=s2&timeout=1", nil, &w)
			pollDone <- code
		}()

		// Give the goroutine time to reach the select and register its waiter.
		time.Sleep(80 * time.Millisecond)

		// Run agentSettled while the poll is blocked.
		// agentSettled wakes the poll (via s.wake) and then prunes the waiter.
		if code := r.do(t, "POST", "/api/agent/settled", map[string]any{
			"session": "s2",
			"shown":   []int64{},
		}, nil); code != http.StatusOK {
			t.Fatalf("agentSettled: got %d, want 200", code)
		}

		// The blocked poll must return – either woken immediately or timed out.
		select {
		case code := <-pollDone:
			// 200 (delivery) or 204 (no content / timeout) are both valid here;
			// anything else is an error.
			if code != http.StatusOK && code != http.StatusNoContent {
				t.Fatalf("blocked poll returned unexpected status %d", code)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("blocked poll did not return after agentSettled")
		}

		// The waiter must have been pruned by agentSettled.
		r.s.mu.Lock()
		_, stillThere := r.s.waiters["s2"]
		r.s.mu.Unlock()
		if stillThere {
			t.Error("waiter for s2 was not pruned after agentSettled")
		}

		// A message sent NOW must reach a fresh poll for the same session.
		r.send(t, th, "after settlement", false)
		var w2 waited
		if code := r.do(t, "GET", "/api/agent/wait?session=s2", nil, &w2); code != http.StatusOK {
			t.Fatalf("new poll: got %d, want 200", code)
		}
		if w2.Delivery.ID == 0 || len(w2.Delivery.Messages) == 0 {
			t.Fatalf("new poll returned no delivery/messages: %+v", w2)
		}
	})
}
