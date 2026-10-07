package serve

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/observe"
)

type recCounts struct {
	Recommended    int `json:"recommended"`
	NotRecommended int `json:"not_recommended"`
}

func (r *rig) recCounts(t *testing.T) recCounts {
	t.Helper()
	var s recCounts
	if c := r.do(t, "GET", "/api/summary", nil, &s); c != 200 {
		t.Fatalf("summary %d", c)
	}
	return s
}

// undecidedKeys is every item that needs a decision: the waiting, due and
// new views, each item once.
func (r *rig) undecidedKeys(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	var keys []string
	for _, v := range []string{"waiting", "due", "new"} {
		var list ItemsView
		if c := r.do(t, "GET", "/api/items?limit=500&view="+v, nil, &list); c != 200 {
			t.Fatalf("items %s %d", v, c)
		}
		for _, it := range list.Items {
			if !seen[it.ID] {
				seen[it.ID] = true
				keys = append(keys, it.ID)
			}
		}
	}
	return keys
}

// TestSummaryCountsRecommended: the summary counts the items that need a
// decision with a pending proposal (recommended) and without (not yet).
func TestSummaryCountsRecommended(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	all := r.undecidedKeys(t)
	if got := r.recCounts(t); got.Recommended != 0 || got.NotRecommended != len(all) {
		t.Fatalf("before any proposal: %+v, want 0 and %d", got, len(all))
	}
	r.recommend(t, "s1", "issue:schuettc/hail#4")
	r.recommend(t, "s1", "issue:schuettc/hail#5")
	r.recommend(t, "s1", "pr:schuettc/hail#3")
	if got := r.recCounts(t); got.Recommended != 3 || got.NotRecommended != len(all)-3 {
		t.Fatalf("after three proposals: %+v, want 3 and %d", got, len(all)-3)
	}
	// A decided item leaves both counts: one recommended, one not.
	r.decideOne(t, "issue:schuettc/hail#4", "keep")
	r.decideOne(t, "issue:schuettc/hail#6", "keep")
	got := r.recCounts(t)
	if got.Recommended != 2 || got.NotRecommended != len(all)-4 {
		t.Fatalf("after two decisions: %+v, want 2 and %d", got, len(all)-4)
	}
	// Not yet is what casebook_next has left to hand out.
	if v := r.next(t, "s1"); v.Left != got.NotRecommended {
		t.Fatalf("next has %d left, the summary says %d not yet", v.Left, got.NotRecommended)
	}
}

// TestRecommendedSortFirst: in an Attention view, the items with a pending
// proposal come first, each group in the view's own order.
func TestRecommendedSortFirst(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	var before ItemsView
	r.do(t, "GET", "/api/items?view=waiting&limit=500", nil, &before)
	r.recommend(t, "s1", "issue:schuettc/hail#9")
	r.recommend(t, "s1", "issue:schuettc/hail#12")
	var after ItemsView
	r.do(t, "GET", "/api/items?view=waiting&limit=500", nil, &after)
	var want []string
	for _, it := range before.Items {
		if it.ID == "issue:schuettc/hail#12" || it.ID == "issue:schuettc/hail#9" {
			want = append(want, it.ID)
		}
	}
	for _, it := range before.Items {
		if it.ID != "issue:schuettc/hail#12" && it.ID != "issue:schuettc/hail#9" {
			want = append(want, it.ID)
		}
	}
	var got []string
	for _, it := range after.Items {
		got = append(got, it.ID)
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("waiting order\n got %v\nwant %v", got, want)
	}
	// Paging follows the same order.
	var page ItemsView
	r.do(t, "GET", "/api/items?view=waiting&offset=1&limit=1", nil, &page)
	if len(page.Items) != 1 || page.Items[0].ID != want[1] {
		t.Fatalf("second page %+v, want %s", page.Items, want[1])
	}
}

// ghLog records every gh call serve makes.
type ghLog struct {
	inner observe.Runner
	mu    sync.Mutex
	calls []string
}

func (g *ghLog) Gh(ctx context.Context, args ...string) ([]byte, error) {
	g.mu.Lock()
	g.calls = append(g.calls, strings.Join(args, " "))
	g.mu.Unlock()
	return g.inner.Gh(ctx, args...)
}

// TestAgreeAllOutwardGoesToApply: accepting several close recommendations
// at once (agree with all) records the decisions, which go to To apply;
// no job is made and nothing runs on GitHub.
func TestAgreeAllOutwardGoesToApply(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	log := &ghLog{inner: r.App.Gh}
	r.App.Gh = log
	keys := []string{"issue:schuettc/hail#7", "issue:schuettc/hail#8", "pr:schuettc/hail#3"}
	var out struct {
		Proposals []struct {
			ID int64 `json:"id"`
		} `json:"proposals"`
	}
	if c := r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": keys, "disposition": "close", "note": "stale"}, &out); c != 200 || len(out.Proposals) != 3 {
		t.Fatalf("propose %d %+v", c, out)
	}
	ids := []int64{out.Proposals[0].ID, out.Proposals[1].ID, out.Proposals[2].ID}
	var acc AcceptResult
	if c := r.do(t, "POST", "/api/proposals/accept", map[string]any{"ids": ids}, &acc); c != 200 || acc.Accepted != 3 {
		t.Fatalf("accept %d %+v", c, acc)
	}
	for _, k := range keys {
		key, _ := item.ParseKey(k)
		d, _ := r.App.Repo.ReadDecision(key)
		if d == nil || d.Disposition != "close" || d.ProposedBy != "pi:s1" {
			t.Fatalf("%s decision %+v", k, d)
		}
	}
	var apply ItemsView
	r.do(t, "GET", "/api/items?view=to-apply&limit=500", nil, &apply)
	inApply := map[string]bool{}
	for _, it := range apply.Items {
		inApply[it.ID] = true
	}
	for _, k := range keys {
		if !inApply[k] {
			t.Fatalf("%s is not in To apply: %+v", k, apply.Items)
		}
	}
	var jobs JobsView
	if c := r.do(t, "GET", "/api/jobs", nil, &jobs); c != 200 || len(jobs.Jobs) != 0 {
		t.Fatalf("jobs %d %+v, want none", c, jobs.Jobs)
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	for _, c := range log.calls {
		for _, verb := range []string{"close", "merge", "archive", "delete", "comment", "edit"} {
			if strings.Contains(c, " "+verb) {
				t.Fatalf("gh ran an outward step: %s", c)
			}
		}
	}
}

// TestUntilNamingUntrackedItemIsAccepted: a Not now condition may name any
// well-formed key, tracked or not (an upstream PR); a sync that can't find it
// brings the item back. A malformed condition is still refused.
func TestUntilNamingUntrackedItemIsAccepted(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	// The agent's recommendation may name an untracked PR too.
	var pe ProposeResult
	if c := r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": []string{"issue:schuettc/hail#4"}, "disposition": "wait", "until": "merged(pr:up/stream#58)", "note": "after the fix"}, &pe); c != 200 || len(pe.Errors) != 0 || pe.Proposed != 1 {
		t.Fatalf("propose: %d %+v", c, pe)
	}
	for key, until := range map[string]string{
		"issue:schuettc/hail#4": "merged(pr:schuettc/hail#999)",
		"issue:schuettc/hail#5": "closed(issue:up/stream#998)",
		"pr:schuettc/hail#3":    "released(repo:schuettc/nowhere)",
	} {
		var dec DecideResult
		if c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{key}, "disposition": "wait", "until": until}, &dec); c != 200 || dec.Decided != 1 || len(dec.Errors) != 0 {
			t.Fatalf("decide %s until %s: %d %+v", key, until, c, dec)
		}
		d, err := r.App.Repo.ReadDecision(mustKey(t, key))
		if err != nil || d == nil || d.Until != until {
			t.Fatalf("%s decision %+v %v, want until %s", key, d, err, until)
		}
	}
	// A malformed key is still refused, and nothing is decided.
	var dec DecideResult
	r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"issue:schuettc/hail#5"}, "disposition": "wait", "until": "merged(tackle#58)"}, &dec)
	if dec.Decided != 0 || len(dec.Errors) != 1 || !strings.Contains(dec.Errors[0], "invalid until") {
		t.Fatalf("malformed until: %+v", dec)
	}
}

// TestItemsTrackedViewListsEveryItem: view=tracked searches every item
// casebook tracks (the Not now picker's search), decided ones included,
// with the kind and q filters.
func TestItemsTrackedViewListsEveryItem(t *testing.T) {
	r := newRig(t)
	r.decideOne(t, "pr:schuettc/hail#3", "keep")
	ids := func(path string) []string {
		var list ItemsView
		if c := r.do(t, "GET", path, nil, &list); c != 200 {
			t.Fatalf("%s: %d", path, c)
		}
		var out []string
		for _, it := range list.Items {
			out = append(out, it.ID)
		}
		return out
	}
	if got := ids("/api/items?view=all&kind=pr&q=hail%233"); len(got) != 0 {
		t.Fatalf("a kept PR in attention: %v", got)
	}
	if got := ids("/api/items?view=tracked&kind=pr&q=hail%233"); len(got) != 1 || got[0] != "pr:schuettc/hail#3" {
		t.Fatalf("tracked pr hail#3: %v", got)
	}
	for _, id := range ids("/api/items?view=tracked&kind=issue") {
		if !strings.HasPrefix(id, "issue:") {
			t.Fatalf("kind=issue listed %s", id)
		}
	}
}
