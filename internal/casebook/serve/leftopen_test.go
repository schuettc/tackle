package serve

import (
	"slices"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/config"
	"github.com/schuettc/tackle/internal/casebook/observe"
)

// What Leave it open and Not now leave behind (spec amendment 2026-10-07):
// a kept item stays in its views, in a left-open group under the items that
// need a decision, uncounted; someone else's newer activity brings it back
// to the top, needing a decision; casebook_next hands out only the latter.

type leftOpenItems struct {
	Total         int `json:"total"`
	LeftOpenTotal int `json:"left_open_total"`
	Items         []struct {
		Key         string `json:"key"`
		Status      string `json:"status"`
		DueReason   string `json:"due_reason"`
		NewActivity bool   `json:"new_activity"`
	} `json:"items"`
	LeftOpen []struct {
		Key      string `json:"key"`
		Status   string `json:"status"`
		Decision *struct {
			DecidedAt time.Time `json:"decided_at"`
		} `json:"decision"`
	} `json:"left_open"`
}

func (v leftOpenItems) keys() []string {
	var ks []string
	for _, it := range v.Items {
		ks = append(ks, it.Key)
	}
	return ks
}

func (v leftOpenItems) leftKeys() []string {
	var ks []string
	for _, it := range v.LeftOpen {
		ks = append(ks, it.Key)
	}
	return ks
}

func (r *rig) view(t *testing.T, view string) leftOpenItems {
	t.Helper()
	var v leftOpenItems
	if c := r.do(t, "GET", "/api/items?view="+view, nil, &v); c != 200 {
		t.Fatalf("items %s: %d", view, c)
	}
	return v
}

// comment records, in the GitHub cache as a sync would, a last comment on
// one of hail's open issues or its PR, and rebuilds the index.
func (r *rig) comment(t *testing.T, kind string, number int, author string, at time.Time) {
	t.Helper()
	g, err := observe.LoadGitHub(config.CachePath())
	if err != nil {
		t.Fatal(err)
	}
	repo := &g.Owners["schuettc"].Repos[0]
	list := repo.Issues
	if kind == "pr" {
		list = repo.PRs
	}
	found := false
	for i := range list {
		if list[i].Number == number {
			list[i].LastCommentAuthor, list[i].LastCommentAt, list[i].UpdatedAt = author, at, at
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s #%d in the cache", kind, number)
	}
	r.saveAndRebuild(t, g)
}

func (r *rig) saveAndRebuild(t *testing.T, g *observe.GitHub) {
	t.Helper()
	if err := observe.SaveGitHub(config.CachePath(), g); err != nil {
		t.Fatal(err)
	}
	if err := r.s.rebuild(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestKeepGoesToTheLeftOpenGroup: kept, an item leaves the view's items and
// its count, and is listed in the view's left-open group with its decision.
func TestKeepGoesToTheLeftOpenGroup(t *testing.T) {
	r := newRig(t)
	const key = "issue:schuettc/hail#4"
	before := r.view(t, ViewWaiting)
	counted := r.summary(t).Counts[ViewWaiting]
	if !slices.Contains(before.keys(), key) || before.LeftOpenTotal != 0 {
		t.Fatalf("before: %v left open %d", before.keys(), before.LeftOpenTotal)
	}
	r.decideOne(t, key, "keep")
	after := r.view(t, ViewWaiting)
	if slices.Contains(after.keys(), key) || after.Total != before.Total-1 {
		t.Fatalf("kept item still among the items: %v (total %d, was %d)", after.keys(), after.Total, before.Total)
	}
	if !slices.Equal(after.leftKeys(), []string{key}) || after.LeftOpenTotal != 1 {
		t.Fatalf("left open %v (%d), want [%s]", after.leftKeys(), after.LeftOpenTotal, key)
	}
	if lo := after.LeftOpen[0]; lo.Status != "left-open" || lo.Decision == nil || lo.Decision.DecidedAt.IsZero() {
		t.Fatalf("left-open row %+v: want status left-open with its decision", lo)
	}
	if got := r.summary(t).Counts[ViewWaiting]; got != counted-1 {
		t.Fatalf("waiting count %d, want %d: a left-open item isn't counted", got, counted-1)
	}
	// It would be new were it undecided: the new view lists it too. The due
	// view, for items whose decision came back, doesn't.
	if v := r.view(t, ViewNew); !slices.Contains(v.leftKeys(), key) || slices.Contains(v.keys(), key) {
		t.Fatalf("new view: items %v left open %v", v.keys(), v.leftKeys())
	}
	if v := r.view(t, ViewDue); len(v.LeftOpen) != 0 {
		t.Fatalf("due view left open %v, want none", v.leftKeys())
	}
	// A kept branch has no activity: it stays in the new view's left-open
	// group, and in no waiting view.
	const br = "branch:schuettc/hail@feat/client"
	r.decideOne(t, br, "keep")
	if v := r.view(t, ViewNew); !slices.Contains(v.leftKeys(), br) {
		t.Fatalf("kept branch not left open in new: %v", v.leftKeys())
	}
	if v := r.view(t, ViewWaiting); slices.Contains(v.leftKeys(), br) {
		t.Fatalf("kept branch in waiting's left-open group")
	}
	// To apply and tracked have no left-open group.
	if v := r.view(t, ViewToApply); len(v.LeftOpen) != 0 {
		t.Fatalf("to-apply left open %v", v.leftKeys())
	}
}

// TestNewActivityBringsAKeptItemBack: someone else's comment newer than the
// decision moves the item back up, needing a decision and saying why;
// Court's own doesn't.
func TestNewActivityBringsAKeptItemBack(t *testing.T) {
	r := newRig(t)
	const key = "issue:schuettc/hail#4"
	counted := r.summary(t).Counts[ViewWaiting]
	d := r.decideOne(t, key, "keep")

	// Court's own reply: still left open (in new; no longer waiting on him).
	r.comment(t, "issue", 4, "schuettc", d.DecidedAt.Add(time.Minute))
	if v := r.view(t, ViewNew); !slices.Contains(v.leftKeys(), key) {
		t.Fatalf("Court's own comment brought it back: items %v left open %v", v.keys(), v.leftKeys())
	}

	r.comment(t, "issue", 4, "alice", d.DecidedAt.Add(2*time.Minute))
	v := r.view(t, ViewWaiting)
	if len(v.Items) == 0 || v.Items[0].Key != key {
		t.Fatalf("brought back: want %s at the top, got %v", key, v.keys())
	}
	if it := v.Items[0]; it.Status != "due" || !it.NewActivity || it.DueReason != "new activity since you left it open" {
		t.Fatalf("brought back row %+v", it)
	}
	if slices.Contains(v.leftKeys(), key) {
		t.Fatal("brought back and still left open")
	}
	if got := r.summary(t).Counts[ViewWaiting]; got != counted {
		t.Fatalf("waiting count %d, want %d: a brought-back item counts", got, counted)
	}
	if v := r.view(t, ViewDue); !slices.Contains(v.keys(), key) {
		t.Fatalf("due view %v lacks the brought-back item", v.keys())
	}
}

// TestNotNowComesBackOnNewActivity: a Not now with a far condition comes
// back on someone else's newer comment, saying so.
func TestNotNowComesBackOnNewActivity(t *testing.T) {
	r := newRig(t)
	const key = "issue:schuettc/hail#5"
	var res DecideResult
	if c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{key}, "disposition": "wait", "until": "date(2030-01-01)"}, &res); c != 200 || res.Decided != 1 {
		t.Fatalf("decide %d %+v", c, res)
	}
	if v := r.view(t, ViewWaiting); slices.Contains(v.keys(), key) || slices.Contains(v.leftKeys(), key) {
		t.Fatalf("a Not now is listed: %v / %v", v.keys(), v.leftKeys())
	}
	d := r.decisionOf(t, key)
	r.comment(t, "issue", 5, "carol", d.DecidedAt.Add(time.Minute))
	v := r.view(t, ViewWaiting)
	if len(v.Items) == 0 || v.Items[0].Key != key || v.Items[0].DueReason != "new activity since Not now" {
		t.Fatalf("waiting after carol's comment: %+v", v.Items)
	}
}

// TestKeptThenClosedDropsOut: a kept issue closed on GitHub since is in no
// list and no left-open group.
func TestKeptThenClosedDropsOut(t *testing.T) {
	r := newRig(t)
	const key = "issue:schuettc/hail#6"
	r.decideOne(t, key, "keep")
	g, err := observe.LoadGitHub(config.CachePath())
	if err != nil {
		t.Fatal(err)
	}
	repo := &g.Owners["schuettc"].Repos[0]
	repo.Issues = slices.DeleteFunc(repo.Issues, func(p observe.PRObs) bool { return p.Number == 6 })
	g.Refs[key] = observe.Ref{Exists: true, State: "CLOSED"}
	r.saveAndRebuild(t, g)
	for _, view := range []string{ViewWaiting, ViewNew, ViewDue, ViewAll} {
		if v := r.view(t, view); slices.Contains(v.keys(), key) || slices.Contains(v.leftKeys(), key) {
			t.Fatalf("%s lists the closed kept issue: %v / %v", view, v.keys(), v.leftKeys())
		}
	}
}

// TestIgnoreIsNeverListed: Stop tracking it is gone, even after someone
// else's activity.
func TestIgnoreIsNeverListed(t *testing.T) {
	r := newRig(t)
	const key = "issue:schuettc/hail#7"
	d := r.decideOne(t, key, "ignore")
	r.comment(t, "issue", 7, "grace", d.DecidedAt.Add(time.Minute))
	for _, view := range []string{ViewWaiting, ViewNew, ViewDue, ViewAll} {
		if v := r.view(t, view); slices.Contains(v.keys(), key) || slices.Contains(v.leftKeys(), key) {
			t.Fatalf("%s lists the ignored issue", view)
		}
	}
}

// TestNextSkipsLeftOpenTakesBroughtBack: casebook_next never hands out a
// quiet left-open item, and hands out one new activity brought back.
func TestNextSkipsLeftOpenTakesBroughtBack(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	const quiet, back = "issue:schuettc/hail#4", "issue:schuettc/hail#5"
	r.decideOne(t, quiet, "keep")
	d := r.decideOne(t, back, "keep")
	r.comment(t, "issue", 5, "carol", d.DecidedAt.Add(time.Minute))
	first := r.next(t, "s1")
	if first.Item == nil || first.Item.Item.Key != back {
		t.Fatalf("first next %+v, want the brought-back %s", first.Item, back)
	}
	keys := r.drain(t, "s1")
	if slices.Contains(keys, quiet) {
		t.Fatalf("next handed out the quiet left-open %s: %v", quiet, keys)
	}
	if !slices.Contains(keys, back) {
		t.Fatalf("next never handed out the brought-back %s: %v", back, keys)
	}
	// And a recommendation for the quiet one is refused: it needs none.
	var out proposeOut
	if c := r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": []string{quiet},
		"disposition": "close", "note": "stale"}, &out); c != 200 || out.Proposed != 0 {
		t.Fatalf("propose on a left-open item: %d %+v", c, out)
	}
}
