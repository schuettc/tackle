package serve

import (
	"context"
	"net/http"
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

// TestUntilNamingUnknownItemIsRefused: a Not now condition naming an item
// casebook doesn't know (a well-formed key to a PR that isn't there) is
// refused with a 400 the page shows, and nothing is decided or proposed.
func TestUntilNamingUnknownItemIsRefused(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	const unknown = "pr:schuettc/hail#999"
	want := unknown + " isn't an item casebook knows; sync first if it's new"
	for _, until := range []string{"merged(" + unknown + ")", "closed(issue:schuettc/hail#998)", "released(repo:schuettc/nowhere)"} {
		var e struct {
			Error string `json:"error"`
		}
		c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"issue:schuettc/hail#4"}, "disposition": "wait", "until": until}, &e)
		if c != http.StatusBadRequest || !strings.Contains(e.Error, "isn't an item casebook knows; sync first if it's new") {
			t.Fatalf("decide until %s: %d %q", until, c, e.Error)
		}
	}
	var e struct {
		Error string `json:"error"`
	}
	r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"issue:schuettc/hail#4"}, "disposition": "wait", "until": "merged(" + unknown + ")"}, &e)
	if e.Error != want {
		t.Fatalf("message %q, want %q", e.Error, want)
	}
	if d, _ := r.App.Repo.ReadDecision(item.IssueKey("schuettc/hail", 4)); d != nil {
		t.Fatalf("decided anyway: %+v", d)
	}
	// The agent's recommendation is refused the same way.
	var pe struct {
		Error    string `json:"error"`
		Proposed int    `json:"proposed"`
	}
	c := r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": []string{"issue:schuettc/hail#4"}, "disposition": "wait", "until": "merged(" + unknown + ")", "note": "after the fix"}, &pe)
	if c != http.StatusBadRequest || pe.Error != want {
		t.Fatalf("propose: %d %+v", c, pe)
	}
	if got := r.recCounts(t); got.Recommended != 0 {
		t.Fatalf("proposed anyway: %+v", got)
	}
	// A key casebook knows is accepted.
	var dec DecideResult
	if c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"issue:schuettc/hail#4"}, "disposition": "wait", "until": "merged(pr:schuettc/hail#3)"}, &dec); c != 200 || dec.Decided != 1 {
		t.Fatalf("known key: %d %+v", c, dec)
	}
}
