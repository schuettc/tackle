package serve

import (
	"encoding/json"
	"slices"
	"testing"
)

// proposalsEvent is a "proposals" bus event's payload.
type proposalsEvent struct {
	IDs    []int64  `json:"ids"`
	Keys   []string `json:"keys"`
	State  string   `json:"state"`
	Source string   `json:"source"`
}

// proposalsEvents reads the "proposals" events published after cursor.
func (r *rig) proposalsEvents(t *testing.T, cursor int64) []proposalsEvent {
	t.Helper()
	evs, _, err := r.s.Bus.Since(ctx, cursor, 10000)
	if err != nil {
		t.Fatal(err)
	}
	var out []proposalsEvent
	for _, e := range evs {
		if e.Type != "proposals" {
			continue
		}
		var p proposalsEvent
		if err := json.Unmarshal(e.Data, &p); err != nil {
			t.Fatalf("proposals event %s: %v", e.Data, err)
		}
		out = append(out, p)
	}
	return out
}

// lastProposals is the newest "proposals" event after cursor in state.
func (r *rig) lastProposals(t *testing.T, cursor int64, state string) proposalsEvent {
	t.Helper()
	evs := r.proposalsEvents(t, cursor)
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].State == state {
			return evs[i]
		}
	}
	t.Fatalf("no %s proposals event after %d (got %+v)", state, cursor, evs)
	return proposalsEvent{}
}

// Every "proposals" event names the items its proposals are for, so the
// page can re-render the open item when it is one of them (whatever page
// of the list it is on): a new pending proposal, an accept, a change and
// a reject.
func TestProposalsEventsCarryKeys(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	keys := []string{"pr:schuettc/hail#3", "issue:schuettc/hail#4", "issue:schuettc/hail#5"}
	var res struct {
		Proposals []struct {
			ID  int64  `json:"id"`
			Key string `json:"key"`
		} `json:"proposals"`
	}

	head, _ := r.s.Bus.Head(ctx)
	if c := r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": keys, "disposition": "close", "note": "stale"}, &res); c != 200 || len(res.Proposals) != 3 {
		t.Fatalf("propose %d %+v", c, res)
	}
	p := r.lastProposals(t, head, "pending")
	if !slices.Equal(p.Keys, keys) || len(p.IDs) != 3 || p.Source != "pi:s1" {
		t.Fatalf("pending event %+v, want keys %v", p, keys)
	}
	id := map[string]int64{}
	for _, x := range res.Proposals {
		id[x.Key] = x.ID
	}

	head, _ = r.s.Bus.Head(ctx)
	r.do(t, "POST", "/api/proposals/accept", map[string]any{"ids": []int64{id[keys[0]]}}, nil)
	if p := r.lastProposals(t, head, "accepted"); !slices.Equal(p.Keys, keys[:1]) {
		t.Fatalf("accepted event %+v, want keys %v", p, keys[:1])
	}

	head, _ = r.s.Bus.Head(ctx)
	r.do(t, "POST", "/api/proposals/change", map[string]any{"id": id[keys[1]], "disposition": "keep", "note": "active"}, nil)
	if p := r.lastProposals(t, head, "changed"); !slices.Equal(p.Keys, keys[1:2]) {
		t.Fatalf("changed event %+v, want keys %v", p, keys[1:2])
	}

	head, _ = r.s.Bus.Head(ctx)
	r.do(t, "POST", "/api/proposals/reject", map[string]any{"ids": []int64{id[keys[2]]}, "reason": "no"}, nil)
	if p := r.lastProposals(t, head, "rejected"); !slices.Equal(p.Keys, keys[2:]) {
		t.Fatalf("rejected event %+v, want keys %v", p, keys[2:])
	}
}

// A rule's proposals reach the page as a "proposals" event too, naming
// their items: propose-once, and an active rule's proposals after a
// rebuild (activation).
func TestRuleProposalsPublishKeys(t *testing.T) {
	r, _ := newRuleRig(t)
	head, _ := r.s.Bus.Head(ctx)
	var result ProposeResult
	if c := r.do(t, "POST", "/api/rules/propose-once", map[string]any{"id": "kind-repo"}, &result); c != 200 || result.Proposed < 1 {
		t.Fatalf("propose-once %d %+v", c, result)
	}
	var want []string
	for _, p := range result.Proposals {
		want = append(want, p.Key)
	}
	p := r.lastProposals(t, head, "pending")
	if !slices.Equal(p.Keys, want) || p.Source != "rule:kind-repo" {
		t.Fatalf("propose-once event %+v, want keys %v from rule:kind-repo", p, want)
	}

	// Activation (on a serve of its own) proposes through the rebuild.
	r2, _ := newRuleRig(t)
	head, _ = r2.s.Bus.Head(ctx)
	var detail RuleDetailView
	if c := r2.do(t, "POST", "/api/rules/activate", map[string]any{"id": "kind-repo"}, &detail); c != 200 {
		t.Fatalf("activate %d", c)
	}
	pending, _ := r2.s.Props.Pending(ctx)
	var made []string
	for k, p := range pending {
		if p.Source == "rule:kind-repo" {
			made = append(made, k)
		}
	}
	p = r2.lastProposals(t, head, "pending")
	got := slices.Clone(p.Keys)
	slices.Sort(got)
	slices.Sort(made)
	if len(made) == 0 || !slices.Equal(got, made) || p.Source != "rule:kind-repo" {
		t.Fatalf("activation event %+v, want keys %v from rule:kind-repo", p, made)
	}
}
