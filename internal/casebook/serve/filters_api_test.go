package serve

import (
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/subaud-tools/tackle/internal/casebook/testgit"
)

type filterErr struct {
	Error string `json:"error"`
}

func (r *rig) filterRows(t *testing.T) FiltersView {
	t.Helper()
	var v FiltersView
	if c := r.do(t, "GET", "/api/filters", nil, &v); c != 200 {
		t.Fatalf("GET /api/filters: %d", c)
	}
	return v
}

func rowIDs(v FiltersView) []string {
	ids := []string{}
	for _, row := range v.Filters {
		ids = append(ids, row.Filter.ID)
	}
	return ids
}

func openIssues(kind string) map[string]any {
	return map[string]any{"id": "open-issues", "name": "Open issues",
		"match": []map[string]any{{"field": "kind", "op": "is", "value": kind}}}
}

func TestFiltersListsExamples(t *testing.T) {
	r := newRig(t)
	v := r.filterRows(t)
	if got, want := rowIDs(v), []string{"dormant-repos", "incoming", "outgoing-prs", "quiet-14d", "unpushed"}; !slices.Equal(got, want) {
		t.Fatalf("filters %v, want %v", got, want)
	}
	for _, row := range v.Filters {
		if row.Invalid != "" {
			t.Errorf("%s invalid: %s", row.Filter.ID, row.Invalid)
		}
	}
}

func TestFilterSaveCommitsAndPushes(t *testing.T) {
	r := newRig(t)
	var v FiltersView
	if c := r.do(t, "POST", "/api/filters/save?create=1", openIssues("issue"), &v); c != 200 {
		t.Fatalf("save: %d", c)
	}
	if !slices.Contains(rowIDs(v), "open-issues") {
		t.Fatalf("save reply lacks open-issues: %v", rowIDs(v))
	}
	if subj := testgit.Git(t, r.App.Repo.Dir, "log", "-1", "--format=%s"); subj != "filter open-issues saved by schuettc" {
		t.Fatalf("subject %q", subj)
	}
	r.s.pushWG.Wait()
	if n := testgit.Git(t, r.App.Repo.Dir, "rev-list", "--count", "origin/main..HEAD"); n != "0" {
		t.Fatalf("%s commits not pushed", n)
	}
	var e filterErr
	if c := r.do(t, "POST", "/api/filters/save?create=1", openIssues("issue"), &e); c != 409 || e.Error != `filter "open-issues" already exists` {
		t.Fatalf("second create: %d %q", c, e.Error)
	}
	// Without create, a save replaces it.
	if c := r.do(t, "POST", "/api/filters/save", openIssues("pr"), nil); c != 200 {
		t.Fatalf("replace: %d", c)
	}
	if f, err := r.App.Repo.ReadFilter("open-issues"); err != nil || f == nil || f.Match[0].Value != "pr" {
		t.Fatalf("after replace %+v %v", f, err)
	}
}

func TestFilterSaveRefusesInvalid(t *testing.T) {
	r := newRig(t)
	before := testgit.Git(t, r.App.Repo.Dir, "rev-parse", "HEAD")
	var e filterErr
	body := map[string]any{"id": "empty", "name": "Empty", "match": []map[string]any{}}
	if c := r.do(t, "POST", "/api/filters/save?create=1", body, &e); c != 400 || e.Error != "a filter needs at least one condition" {
		t.Fatalf("no conditions: %d %q", c, e.Error)
	}
	body = map[string]any{"id": "red", "name": "Red", "match": []map[string]any{{"field": "colour", "op": "is", "value": "red"}}}
	if c := r.do(t, "POST", "/api/filters/save?create=1", body, &e); c != 400 || !strings.Contains(e.Error, `condition 1: unknown field "colour"`) {
		t.Fatalf("bad field: %d %q", c, e.Error)
	}
	if after := testgit.Git(t, r.App.Repo.Dir, "rev-parse", "HEAD"); after != before {
		t.Fatalf("HEAD moved %s → %s", before, after)
	}
}

func TestFilterDelete(t *testing.T) {
	r := newRig(t)
	if c := r.do(t, "POST", "/api/filters/delete", map[string]any{"id": "incoming"}, nil); c != 200 {
		t.Fatalf("delete: %d", c)
	}
	if subj := testgit.Git(t, r.App.Repo.Dir, "log", "-1", "--format=%s"); subj != "filter incoming deleted by schuettc" {
		t.Fatalf("subject %q", subj)
	}
	if slices.Contains(rowIDs(r.filterRows(t)), "incoming") {
		t.Fatal("incoming still listed")
	}
	var e filterErr
	if c := r.do(t, "POST", "/api/filters/delete", map[string]any{"id": "nope"}, &e); c != 404 || e.Error != `filter "nope" not found` {
		t.Fatalf("delete nope: %d %q", c, e.Error)
	}
}

// newestFirst reports whether items are newest first, items with no time
// last.
func newestFirst(items []ItemView) bool {
	for i := 1; i < len(items); i++ {
		a, b := items[i-1].UpdatedAt, items[i].UpdatedAt
		if a.IsZero() && !b.IsZero() {
			return false
		}
		if !b.IsZero() && a.Before(b) {
			return false
		}
	}
	return true
}

// TestItemsFiltersAND: filters= lists every item matching all the filters,
// whatever its status (a Not now included), with no left-open group.
func TestItemsFiltersAND(t *testing.T) {
	r := newRig(t)
	// serve's clock is the rig's, so "updated a day ago" is a day ago.
	r.s.Now = func() time.Time { return r.Now }
	r.pageDecide(t, "issue:schuettc/hail#4", "wait", "date(2030-01-01)", "")
	r.comment(t, "issue", 5, "carol", r.Now.Add(-24*time.Hour))

	one := r.viewItems(t, "filters=incoming&group=false")
	status := map[string]string{}
	for _, it := range one.Items {
		status[it.ID] = string(it.Status)
	}
	if one.Total != 17 || status["issue:schuettc/hail#4"] != "waiting" || status["issue:schuettc/hail#5"] == "" || one.LeftOpenTotal != 0 {
		t.Fatalf("incoming: total %d, #4 %q, #5 %q, left open %d", one.Total, status["issue:schuettc/hail#4"], status["issue:schuettc/hail#5"], one.LeftOpenTotal)
	}
	if !newestFirst(one.Items) {
		t.Errorf("incoming not newest first")
	}

	two := r.viewItems(t, "filters=incoming,quiet-14d&group=false")
	keys := []string{}
	for _, it := range two.Items {
		keys = append(keys, it.ID)
	}
	if two.Total != 16 || slices.Contains(keys, "issue:schuettc/hail#5") {
		t.Fatalf("incoming+quiet: total %d, keys %v", two.Total, keys)
	}
	if !newestFirst(two.Items) {
		t.Errorf("incoming+quiet not newest first")
	}
}

func TestItemsUnknownFilter(t *testing.T) {
	r := newRig(t)
	var e filterErr
	if c := r.do(t, "GET", "/api/items?filters=nope", nil, &e); c != 400 || e.Error != `unknown filter "nope"` {
		t.Fatalf("filters=nope: %d %q", c, e.Error)
	}
}

func TestItemsInvalidFilter(t *testing.T) {
	r := newRig(t)
	testgit.Commit(t, r.App.Repo.Dir, "filters/bad.toml", "id = \"bad\"\nname = \"Bad\"\n[[match]]\nfield = \"colour\"\nop = \"is\"\nvalue = \"red\"\n")
	var e filterErr
	if c := r.do(t, "GET", "/api/items?filters=bad", nil, &e); c != 400 || !strings.Contains(e.Error, `filter "bad" is not valid`) {
		t.Fatalf("filters=bad: %d %q", c, e.Error)
	}
	found := false
	for _, row := range r.filterRows(t).Filters {
		if row.Filter.ID == "bad" {
			found = row.Invalid != ""
		}
	}
	if !found {
		t.Fatal("GET /api/filters doesn't list bad as not valid")
	}
}

// TestItemsDraftMatch: match= (a draft filter's conditions, as JSON) lists
// what saving it as a filter would: the same items, newest first. A bad
// condition is refused with the rules' message; no conditions list nothing.
func TestItemsDraftMatch(t *testing.T) {
	r := newRig(t)
	r.s.Now = func() time.Time { return r.Now }
	saved := r.viewItems(t, "filters=incoming&group=false")
	draft := r.viewItems(t, "group=false&match="+url.QueryEscape(`[{"field":"relation","op":"is","value":"incoming"}]`))
	ids := func(v ItemsView) []string {
		out := []string{}
		for _, it := range v.Items {
			out = append(out, it.ID)
		}
		return out
	}
	if draft.Total != saved.Total || !slices.Equal(ids(draft), ids(saved)) || draft.LeftOpenTotal != 0 {
		t.Fatalf("draft incoming: total %d %v, saved %d %v", draft.Total, ids(draft), saved.Total, ids(saved))
	}
	none := r.viewItems(t, "group=false&match="+url.QueryEscape(`[]`))
	if none.Total != 0 || len(none.Items) != 0 {
		t.Errorf("no conditions: total %d", none.Total)
	}
	var e filterErr
	bad := url.QueryEscape(`[{"field":"kind","op":"is","value":"issue"},{"field":"kind","op":"is","value":"pr"}]`)
	if c := r.do(t, "GET", "/api/items?match="+bad, nil, &e); c != 400 || e.Error != "condition 2: type is already used by condition 1" {
		t.Fatalf("two kind conditions: %d %q", c, e.Error)
	}
}
