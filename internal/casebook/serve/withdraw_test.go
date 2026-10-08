package serve

import (
	"slices"
	"strings"
	"testing"
)

type withdrawOut struct {
	Withdrawn int    `json:"withdrawn"`
	Error     string `json:"error"`
}

func (r *rig) withdraw(t *testing.T, body map[string]any) int {
	t.Helper()
	var out withdrawOut
	if c := r.do(t, "POST", "/api/proposals/withdraw", body, &out); c != 200 {
		t.Fatalf("withdraw %v: %d %+v", body, c, out)
	}
	return out.Withdrawn
}

// viewItems is a view's first page, with its agent recommendation count.
func (r *rig) viewItems(t *testing.T, query string) ItemsView {
	t.Helper()
	var l ItemsView
	if c := r.do(t, "GET", "/api/items?limit=500&"+query, nil, &l); c != 200 {
		t.Fatalf("items %s: %d", query, c)
	}
	return l
}

// TestWithdrawByView: {view, filters…} withdraws the pending agent
// recommendations of exactly that view and filter, every page of it: a
// rule's recommendation and other views' are left. The items reply counts
// the view's agent recommendations, and a "proposals" event says withdrawn
// with the keys.
func TestWithdrawByView(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	for _, k := range []string{"issue:schuettc/hail#4", "issue:schuettc/hail#5", "pr:schuettc/hail#3",
		"branch:schuettc/hail@feat/client", "repo:schuettc/hail"} {
		r.recommend(t, "s1", k)
	}
	if _, errs := r.s.Props.Propose(ctx, "rule:stale", []string{"issue:schuettc/hail#6"}, "keep", "", ""); len(errs) != 0 {
		t.Fatal(errs)
	}
	if n := r.viewItems(t, "view=waiting").AgentRecommended; n != 3 {
		t.Fatalf("waiting has %d agent recommendations, want 3 (the rule's isn't one)", n)
	}
	if n := r.viewItems(t, "view=waiting&kind=pr").AgentRecommended; n != 1 {
		t.Fatalf("waiting pr has %d agent recommendations, want 1", n)
	}

	// The filter narrows it: only the PR.
	if n := r.withdraw(t, map[string]any{"view": "waiting", "kind": "pr"}); n != 1 {
		t.Fatalf("withdrew %d by waiting+pr, want 1", n)
	}
	head, _ := r.s.Bus.Head(ctx)
	// Every page: limit and offset are not part of it.
	if n := r.withdraw(t, map[string]any{"view": "waiting"}); n != 2 {
		t.Fatalf("withdrew %d by waiting, want 2", n)
	}
	ev := r.lastProposals(t, head, "withdrawn")
	if !slices.Equal(ev.Keys, []string{"issue:schuettc/hail#4", "issue:schuettc/hail#5"}) || len(ev.IDs) != 2 {
		t.Fatalf("withdrawn event %+v", ev)
	}
	pending, _ := r.s.Props.Pending(ctx)
	for _, k := range []string{"issue:schuettc/hail#4", "issue:schuettc/hail#5", "pr:schuettc/hail#3"} {
		if p, ok := pending[k]; ok {
			t.Fatalf("%s still has a pending proposal %+v", k, p)
		}
	}
	if p := pending["issue:schuettc/hail#6"]; p.Source != "rule:stale" {
		t.Fatalf("the rule's recommendation went: %+v", pending)
	}
	for _, k := range []string{"branch:schuettc/hail@feat/client", "repo:schuettc/hail"} {
		if p := pending[k]; p.Source != "pi:s1" {
			t.Fatalf("%s (not in waiting) lost its recommendation: %+v", k, pending)
		}
	}
	if n := r.viewItems(t, "view=waiting").AgentRecommended; n != 0 {
		t.Fatalf("waiting still counts %d agent recommendations", n)
	}
	if n := r.viewItems(t, "view=new").AgentRecommended; n != 2 {
		t.Fatalf("new counts %d agent recommendations, want 2", n)
	}
	// Nothing left: nothing withdrawn.
	if n := r.withdraw(t, map[string]any{"view": "waiting"}); n != 0 {
		t.Fatalf("withdrew %d again", n)
	}
	// Neither ids nor a view: a bad request.
	var out withdrawOut
	if c := r.do(t, "POST", "/api/proposals/withdraw", map[string]any{}, &out); c != 400 {
		t.Fatalf("empty withdraw: %d %+v", c, out)
	}
}

// TestWithdrawnComeBackFromNext: once every item is recommended
// casebook_next is done; withdrawing a view's recommendations hands its
// items out again (undecided, no pending recommendation).
func TestWithdrawnComeBackFromNext(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	r.drain(t, "s1")
	if v := r.next(t, "s1"); !v.Done {
		t.Fatalf("not done after the drain: %+v", v)
	}
	waiting := r.viewItems(t, "view=waiting")
	if n := r.withdraw(t, map[string]any{"view": "waiting"}); n != waiting.Total {
		t.Fatalf("withdrew %d, want the %d waiting", n, waiting.Total)
	}
	var want []string
	for _, it := range waiting.Items {
		want = append(want, it.ID)
	}
	got := r.drain(t, "s1")
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("next handed out\n %v\nwant the withdrawn\n %v", got, want)
	}
}

// TestWithdrawnIsNotRejected: the since-summary (the agent's track record)
// counts a rejection but not a withdrawal.
func TestWithdrawnIsNotRejected(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	var res struct {
		Proposals []struct{ ID int64 } `json:"proposals"`
	}
	keys := []string{"issue:schuettc/hail#4", "issue:schuettc/hail#5", "issue:schuettc/hail#6"}
	r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": keys, "disposition": "close"}, &res)
	if len(res.Proposals) != 3 {
		t.Fatalf("proposed %d", len(res.Proposals))
	}
	r.do(t, "POST", "/api/proposals/reject", map[string]any{"ids": []int64{res.Proposals[0].ID}, "reason": "still in use"}, nil)
	if n := r.withdraw(t, map[string]any{"ids": []int64{res.Proposals[1].ID, res.Proposals[2].ID}}); n != 2 {
		t.Fatalf("withdrew %d", n)
	}
	var st struct {
		Since string `json:"since"`
	}
	r.do(t, "GET", "/api/agent/status?session=s1", nil, &st)
	if !strings.HasPrefix(st.Since, "schuettc accepted 0 of your 1 settled proposals, rejected 1.") {
		t.Fatalf("since %q", st.Since)
	}
	if strings.Contains(st.Since, "hail#5") || strings.Contains(st.Since, "hail#6") || strings.Contains(st.Since, "withdrawn") {
		t.Fatalf("since names a withdrawn proposal: %q", st.Since)
	}
}

// TestWithdrawByIDs: {ids} withdraws exactly those agent recommendations;
// a rule's id among them is left pending.
func TestWithdrawByIDs(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	var res struct {
		Proposals []struct {
			ID  int64  `json:"id"`
			Key string `json:"key"`
		} `json:"proposals"`
	}
	keys := []string{"issue:schuettc/hail#4", "issue:schuettc/hail#5", "issue:schuettc/hail#6"}
	r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": keys, "disposition": "keep"}, &res)
	rule, _ := r.s.Props.Propose(ctx, "rule:stale", []string{"issue:schuettc/hail#7"}, "keep", "", "")
	head, _ := r.s.Bus.Head(ctx)
	if n := r.withdraw(t, map[string]any{"ids": []int64{res.Proposals[0].ID, res.Proposals[1].ID, rule[0].ID}}); n != 2 {
		t.Fatalf("withdrew %d, want 2", n)
	}
	if ev := r.lastProposals(t, head, "withdrawn"); !slices.Equal(ev.Keys, keys[:2]) {
		t.Fatalf("withdrawn event %+v", ev)
	}
	pending, _ := r.s.Props.Pending(ctx)
	if _, ok := pending[keys[0]]; ok {
		t.Fatalf("%s still pending", keys[0])
	}
	if _, ok := pending[keys[1]]; ok {
		t.Fatalf("%s still pending", keys[1])
	}
	if p := pending[keys[2]]; p.Source != "pi:s1" {
		t.Fatalf("%s lost its recommendation: %+v", keys[2], pending)
	}
	if p := pending["issue:schuettc/hail#7"]; p.Source != "rule:stale" {
		t.Fatalf("the rule's recommendation went: %+v", pending)
	}
}
