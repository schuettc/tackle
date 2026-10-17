package serve

import (
	"fmt"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/subaud-tools/tackle/internal/casebook/engine"
	"github.com/subaud-tools/tackle/internal/casebook/item"
)

// TestItemsGroupFilterAllPages: a group bigger than a page. ?group= lists
// the whole group, page by page, and its count covers every page.
func TestItemsGroupFilterAllPages(t *testing.T) {
	r := newRig(t)
	var its []engine.Item
	var closing, merging []string
	for n := 1; n <= 460; n++ {
		it := newPR(n, false)
		its = append(its, it)
		if n <= 450 {
			closing = append(closing, it.ID)
		} else {
			merging = append(merging, it.ID)
		}
	}
	r.s.Index.set(engine.Result{Items: its}, r.s.Index.Head(), time.Now())
	if _, errs := r.s.Props.Propose(ctx, "pi:s1", closing, "close", "", ""); len(errs) != 0 {
		t.Fatal(errs)
	}
	if _, errs := r.s.Props.Propose(ctx, "pi:s1", merging, "merge", "", ""); len(errs) != 0 {
		t.Fatal(errs)
	}
	id := "pi:s1|close|Close it without merging"
	var got []string
	for _, off := range []int{0, 200, 400} {
		var l ItemsView
		if c := r.do(t, "GET", fmt.Sprintf("/api/items?view=proposed&group=%s&limit=200&offset=%d", url.QueryEscape(id), off), nil, &l); c != 200 {
			t.Fatalf("items %d", c)
		}
		if l.Total != 450 {
			t.Fatalf("offset %d: total %d, want 450", off, l.Total)
		}
		if i := slices.IndexFunc(l.Groups, func(g GroupView) bool { return g.ID == id }); i < 0 || l.Groups[i].Total != 450 {
			t.Fatalf("offset %d: groups %+v", off, l.Groups)
		}
		got = append(got, ids(l.Items)...)
	}
	slices.Sort(got)
	slices.Sort(closing)
	if !slices.Equal(got, closing) {
		t.Fatalf("the three pages hold %d keys, not the 450 closing ones", len(got))
	}
}

// TestWithdrawByGroup: {view, group} withdraws only that group's agent
// recommendations.
func TestWithdrawByGroup(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	propose := func(keys []string, d string) {
		t.Helper()
		var out proposeOut
		if c := r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": keys, "disposition": d, "note": "why"}, &out); c != 200 || out.Proposed != len(keys) {
			t.Fatalf("propose %v: %d %+v", keys, c, out)
		}
	}
	propose([]string{"issue:schuettc/hail#4", "issue:schuettc/hail#5"}, "close")
	propose([]string{"pr:schuettc/hail#3"}, "close")
	if n := r.withdraw(t, map[string]any{"view": "new", "group": "pi:s1|close|Close it"}); n != 2 {
		t.Fatalf("withdrew %d, want the 2 issues", n)
	}
	pending, _ := r.s.Props.Pending(ctx)
	for _, k := range []string{"issue:schuettc/hail#4", "issue:schuettc/hail#5"} {
		if _, ok := pending[k]; ok {
			t.Fatalf("%s still pending", k)
		}
	}
	if p := pending["pr:schuettc/hail#3"]; p.Source != "pi:s1" {
		t.Fatalf("the PR's close (another group) went: %+v", pending)
	}
}

// TestRecommendedByNamesSession: an item recommended by a known session
// names it; an unknown session gives its id only.
func TestRecommendedByNamesSession(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	r.info(t, map[string]any{"id": "s1", "name": "tools-workspace/casebook", "pid": 1})
	r.recommend(t, "s1", "issue:schuettc/hail#4")
	if _, errs := r.s.Props.Propose(ctx, "pi:ghost", []string{"issue:schuettc/hail#5"}, "keep", "", ""); len(errs) != 0 {
		t.Fatal(errs)
	}
	l := r.viewItems(t, "view=new")
	by := map[string]*ItemView{}
	for i := range l.Items {
		by[l.Items[i].ID] = &l.Items[i]
	}
	if s := by["issue:schuettc/hail#4"].RecommendedBy; s == nil || s.ID != "s1" || s.Name != "tools-workspace/casebook" {
		t.Fatalf("known session: %+v", s)
	}
	if s := by["issue:schuettc/hail#5"].RecommendedBy; s == nil || s.ID != "ghost" || s.Name != "" {
		t.Fatalf("unknown session: %+v", s)
	}
	for _, g := range l.Groups {
		if g.Source == "pi:s1" && (g.By == nil || g.By.Name != "tools-workspace/casebook") {
			t.Fatalf("group %s by %+v", g.ID, g.By)
		}
	}
	// No proposal, or a rule's: nobody.
	if s := by["pr:schuettc/hail#3"]; s == nil || s.RecommendedBy != nil {
		t.Fatalf("unrecommended item: %+v", s)
	}
}

// TestCountWord: the count word the page puts in a group button's {n}.
func TestCountWord(t *testing.T) {
	for _, c := range []struct {
		ticked, total int
		want          string
	}{
		{0, 1, "it"},
		{0, 2, "both"},
		{0, 3, "all 3"},
		{0, 450, "all 450"},
		{3, 27, "3"},
		{1, 2, "1"},
		{2, 2, "both"},
		{27, 27, "all 27"},
	} {
		if got := CountWord(c.ticked, c.total); got != c.want {
			t.Errorf("CountWord(%d, %d) = %q, want %q", c.ticked, c.total, got, c.want)
		}
	}
}

// TestVocabularyGroupWords: the group buttons' words come from the
// vocabulary, each choice with its Many.
func TestVocabularyGroupWords(t *testing.T) {
	r := newRig(t)
	var v DecisionVocabView
	if c := r.do(t, "GET", "/api/decisions/vocabulary", nil, &v); c != 200 {
		t.Fatalf("vocabulary %d", c)
	}
	if v.LookMany != "Look into {n}" || v.DiscardMany != "Discard {n}" || v.RecommendThese != "Ask {session} to recommend {n}" || v.DiscardAll != "discard all {n}" {
		t.Fatalf("group words %q %q %q %q", v.LookMany, v.DiscardMany, v.RecommendThese, v.DiscardAll)
	}
	if v.Summary != (SummaryVocab{ToDecide: "{n} to decide", CameBack: "{n} came back", Group: "{who} \u00b7 {decision} \u00b7 {n}", NotRecommended: "{n} not recommended yet"}) {
		t.Fatalf("summary %+v", v.Summary)
	}
	if v.CameBack["drift"] == "" || v.CameBack["conflict"] == "" || v.CameBack["due"] == "" {
		t.Fatalf("came back words %+v", v.CameBack)
	}
	if v.CountWords != (CountWords{One: "it", Two: "both", All: "all {n}", Some: "{n}"}) {
		t.Fatalf("count words %+v", v.CountWords)
	}
	if v.ToApplyHint != "goes to apply first" || v.Untick != "untick" {
		t.Fatalf("hint %q, untick %q", v.ToApplyHint, v.Untick)
	}
	if v.LeftOpen != "left open" {
		t.Fatalf("left-open group label %q", v.LeftOpen)
	}
	for _, k := range v.Kinds {
		for _, c := range k.Choices {
			if want := item.Choices(item.Kind(k.Kind)); c.Many == "" || c.Many != want[slices.IndexFunc(want, func(w item.Choice) bool { return w.Label == c.Label })].Many {
				t.Errorf("%s %s: many %q", k.Kind, c.Label, c.Many)
			}
		}
	}
}

// TestItemDetailNamesRecommender: GET /api/item names the session whose
// pending proposal the item has, as GET /api/items does (the recommendation
// box's "‹session› recommends").
func TestItemDetailNamesRecommender(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	r.info(t, map[string]any{"id": "s1", "name": "tools-workspace/casebook", "pid": 1})
	r.recommend(t, "s1", "issue:schuettc/hail#4")
	var d ItemDetailView
	if c := r.do(t, "GET", "/api/item?key="+url.QueryEscape("issue:schuettc/hail#4"), nil, &d); c != 200 {
		t.Fatalf("GET /api/item: %d", c)
	}
	if s := d.Item.RecommendedBy; s == nil || s.ID != "s1" || s.Name != "tools-workspace/casebook" {
		t.Fatalf("recommended_by: %+v", s)
	}
	var none ItemDetailView
	if c := r.do(t, "GET", "/api/item?key="+url.QueryEscape("pr:schuettc/hail#3"), nil, &none); c != 200 {
		t.Fatalf("GET /api/item: %d", c)
	}
	if none.Item.RecommendedBy != nil {
		t.Fatalf("unrecommended item: %+v", none.Item.RecommendedBy)
	}
}

// TestVocabularyItemPageWords: the item page's words come from serve: the
// recommendation box's kicker, the « ‹ › » row and its tooltips, and the
// list's collapse control.
func TestVocabularyItemPageWords(t *testing.T) {
	r := newRig(t)
	var v DecisionVocabView
	if c := r.do(t, "GET", "/api/decisions/vocabulary", nil, &v); c != 200 {
		t.Fatalf("vocabulary %d", c)
	}
	if v.Recommends != "{session} recommends" {
		t.Fatalf("recommends %q", v.Recommends)
	}
	want := NavVocab{
		Position:  "{pos} of {total} in {group}",
		PrevItem:  "Previous item: {target} (k)",
		NextItem:  "Next item: {target} (j)",
		PrevGroup: "Previous group: {target} ([)",
		NextGroup: "Next group: {target} (])",
		None:      "none",
		Later:     "on the next page",
	}
	if v.Nav != want {
		t.Fatalf("nav %+v", v.Nav)
	}
	if v.HideList != "Hide the list" || v.ShowList != "Show the list" {
		t.Fatalf("hide %q, show %q", v.HideList, v.ShowList)
	}
}

// TestVocabularyDiscardAndAskedWords: the discard confirm (its title, its
// sentence with the count and the view, its buttons) and the not
// recommended group's "asked ‹session›" come from serve.
func TestVocabularyDiscardAndAskedWords(t *testing.T) {
	r := newRig(t)
	var v DecisionVocabView
	if c := r.do(t, "GET", "/api/decisions/vocabulary", nil, &v); c != 200 {
		t.Fatalf("vocabulary %d", c)
	}
	got := []string{v.DiscardTitle, v.DiscardConfirm, v.DiscardOne, v.DiscardSome, v.DiscardButton, v.DiscardCancel, v.Asked}
	want := []string{
		"discard recommendations",
		"Discard {count} in {view}? Their items go back to the agent to recommend again. Nothing is decided or rejected.",
		"1 recommendation",
		"{n} recommendations",
		"discard",
		"cancel",
		"asked {session}",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("word %d: %q, want %q", i, got[i], want[i])
		}
	}
}

// proposeFor has session s1 recommend d for keys.
func (r *rig) proposeFor(t *testing.T, keys []string, d string) {
	t.Helper()
	var out proposeOut
	if c := r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": keys, "disposition": d, "note": "why"}, &out); c != 200 || out.Proposed != len(keys) {
		t.Fatalf("propose %v: %d %+v", keys, c, out)
	}
}

// TestItemsGroupFalseIsFlat: group=false lists the view newest first
// across its groups: a recommended old issue keeps its date's place.
func TestItemsGroupFalseIsFlat(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	const key = "issue:schuettc/hail#4"
	r.proposeFor(t, []string{key}, "close")
	l := r.viewItems(t, "view=new&group=false")
	at := -1
	for i, it := range l.Items {
		if it.ID == key {
			at = i
		}
		if i > 0 && it.UpdatedAt.After(l.Items[i-1].UpdatedAt) && !l.Items[i-1].UpdatedAt.IsZero() {
			t.Fatalf("%s (%s) after %s (%s): not newest first", it.ID, it.UpdatedAt, l.Items[i-1].ID, l.Items[i-1].UpdatedAt)
		}
		if i > 0 && l.Items[i-1].UpdatedAt.IsZero() && !it.UpdatedAt.IsZero() {
			t.Fatalf("%s has a time but follows %s, which has none", it.ID, l.Items[i-1].ID)
		}
	}
	if at <= 0 {
		t.Fatalf("%s at %d in %v: want it in its date's place, not first", key, at, l.Items)
	}
	g := r.viewItems(t, "view=new&group="+url.QueryEscape("pi:s1|close|Close it"))
	if len(g.Items) != 1 || g.Items[0].ID != key {
		t.Fatalf("close group lists %+v, want only %s", g.Items, key)
	}
}

// TestNewActivityPutsIssueOnTop: a fresh comment puts its issue first in
// new; a fresher one on another issue puts that one first.
func TestNewActivityPutsIssueOnTop(t *testing.T) {
	r := newRig(t)
	r.comment(t, "issue", 5, "github-actions", time.Now().Add(time.Hour))
	if l := r.viewItems(t, "view=new"); len(l.Items) == 0 || l.Items[0].ID != "issue:schuettc/hail#5" {
		t.Fatalf("new starts with %v, want issue #5", l.Items)
	}
	r.comment(t, "issue", 4, "github-actions", time.Now().Add(2*time.Hour))
	if l := r.viewItems(t, "view=new"); len(l.Items) == 0 || l.Items[0].ID != "issue:schuettc/hail#4" {
		t.Fatalf("new starts with %v, want issue #4", l.Items)
	}
}

// TestWithdrawFlatIsTheWholeView: withdrawing the flat list (group=false)
// withdraws the whole view's agent recommendations, not none.
func TestWithdrawFlatIsTheWholeView(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	r.proposeFor(t, []string{"issue:schuettc/hail#4"}, "close")
	r.proposeFor(t, []string{"issue:schuettc/hail#5"}, "keep")
	if n := r.withdraw(t, map[string]any{"view": "new", "group": "false"}); n != 2 {
		t.Fatalf("withdrew %d from the flat list, want 2", n)
	}
}
