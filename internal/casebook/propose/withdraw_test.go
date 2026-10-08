package propose

import (
	"testing"
	"time"
)

// TestWithdrawIsNoJudgement: a withdrawn proposal is no longer pending and
// counts nowhere in a track record: not rejected, not overruled, not
// pending, and RejectedFor (what keeps a rule from re-proposing) never
// names its key. Rule proposals are never withdrawn.
func TestWithdrawIsNoJudgement(t *testing.T) {
	s, now := newStore(t)
	since := *now
	ps, _ := s.Propose(ctx, "pi:s1", []string{"pr:a/b#1", "pr:a/b#2", "pr:a/b#3"}, "close", "", "stale")
	rule, _ := s.Propose(ctx, "rule:stale", []string{"pr:a/b#1"}, "keep", "", "")
	*now = now.Add(time.Minute)

	got, err := s.Withdraw(ctx, []int64{ps[0].ID, ps[1].ID, rule[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "pr:a/b#1" || got[1].Key != "pr:a/b#2" {
		t.Fatalf("withdrawn %+v, want the two agent proposals", got)
	}
	if p, _ := s.Get(ctx, ps[0].ID); p.State != Withdrawn || p.SettledAt.IsZero() {
		t.Fatalf("withdrawn proposal %+v", p)
	}
	if p, _ := s.Get(ctx, rule[0].ID); p.State != Pending {
		t.Fatalf("the rule's proposal is %s, want pending", p.State)
	}
	if tal, _ := s.Tally(ctx, "pi:s1", since); tal != (Tally{Pending: 1}) {
		t.Fatalf("tally %+v, want only the one still pending", tal)
	}
	if _, total, _ := s.Overruled(ctx, "pi:s1", since, 10); total != 0 {
		t.Fatalf("overruled %d, want 0", total)
	}
	if rej, _ := s.RejectedFor(ctx, "pi:s1", since); len(rej) != 0 {
		t.Fatalf("rejected for %v, want none", rej)
	}
	// Withdrawing again withdraws nothing.
	if again, _ := s.Withdraw(ctx, []int64{ps[0].ID}); len(again) != 0 {
		t.Fatalf("withdrew twice: %+v", again)
	}
}

// TestWithdrawKeysAndAgentPendingKeys: WithdrawKeys withdraws every pending
// agent proposal for the keys (one per session), leaving rule proposals and
// other keys; AgentPendingKeys names the keys with a pending agent proposal.
func TestWithdrawKeysAndAgentPendingKeys(t *testing.T) {
	s, _ := newStore(t)
	_, _ = s.Propose(ctx, "pi:s1", []string{"pr:a/b#1", "pr:a/b#2", "pr:a/b#3"}, "close", "", "")
	_, _ = s.Propose(ctx, "claude:s2", []string{"pr:a/b#1"}, "keep", "", "")
	_, _ = s.Propose(ctx, "rule:r", []string{"pr:a/b#2", "pr:a/b#4"}, "keep", "", "")
	keys, err := s.AgentPendingKeys(ctx)
	if err != nil || len(keys) != 3 || !keys["pr:a/b#1"] || !keys["pr:a/b#2"] || !keys["pr:a/b#3"] {
		t.Fatalf("agent pending keys %v %v", keys, err)
	}
	got, err := s.WithdrawKeys(ctx, []string{"pr:a/b#1", "pr:a/b#2", "pr:a/b#4"})
	if err != nil || len(got) != 3 {
		t.Fatalf("withdrawn %+v %v, want pi and claude on #1 and pi on #2", got, err)
	}
	pending, _ := s.Pending(ctx)
	if len(pending) != 3 || pending["pr:a/b#2"].Source != "rule:r" || pending["pr:a/b#4"].Source != "rule:r" || pending["pr:a/b#3"].Source != "pi:s1" {
		t.Fatalf("pending after %+v", pending)
	}
	if keys, _ := s.AgentPendingKeys(ctx); len(keys) != 1 || !keys["pr:a/b#3"] {
		t.Fatalf("agent pending keys after %v", keys)
	}
}
