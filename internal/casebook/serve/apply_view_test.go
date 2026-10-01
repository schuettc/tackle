package serve

import (
	"net/http"
	"testing"
)

// TestToApplyViewListsDecidedItems: the page's To apply section lists the
// decided items waiting to be applied (spec §5.1: Court selects decided items
// in the to-apply state, or applies all), and the bar counts them
// ("to apply N", spec §3.1). They are not attention items, so the view reads
// every item, not the attention set.
func TestToApplyViewListsDecidedItems(t *testing.T) {
	r := newRig(t)

	var dec map[string]any
	if c := r.do(t, "POST", "/api/decide", map[string]any{
		"keys":        []string{"pr:schuettc/hail#3", "issue:schuettc/hail#4"},
		"disposition": "close",
		"note":        "stale",
	}, &dec); c != http.StatusOK || dec["decided"].(float64) != 2 {
		t.Fatalf("decide %d %v", c, dec)
	}

	var items ItemsView
	if c := r.do(t, "GET", "/api/items?view=to-apply", nil, &items); c != http.StatusOK {
		t.Fatalf("items: %d", c)
	}
	if items.Total != 2 || len(items.Items) != 2 {
		t.Fatalf("to-apply view: total %d, %d items; want the 2 decided", items.Total, len(items.Items))
	}
	for i, want := range []string{"issue:schuettc/hail#4", "pr:schuettc/hail#3"} {
		it := items.Items[i]
		if it.ID != want || it.Status != "to-apply" || it.Decision == nil || string(it.Decision.Disposition) != "close" {
			t.Errorf("item %d = %s (%s, %+v), want %s to-apply, decided close", i, it.ID, it.Status, it.Decision, want)
		}
	}

	// Filters apply as in any view.
	var prs ItemsView
	r.do(t, "GET", "/api/items?view=to-apply&kind=pr", nil, &prs)
	if prs.Total != 1 || prs.Items[0].ID != "pr:schuettc/hail#3" {
		t.Errorf("to-apply&kind=pr: %+v", prs)
	}

	var sum SummaryView
	r.do(t, "GET", "/api/summary", nil, &sum)
	if sum.Counts["to-apply"] != 2 {
		t.Errorf(`summary counts["to-apply"] = %d, want 2 (%v)`, sum.Counts["to-apply"], sum.Counts)
	}

	// Attention views are unchanged: a decided item isn't in them.
	var all ItemsView
	r.do(t, "GET", "/api/items?view=all", nil, &all)
	for _, it := range all.Items {
		if it.Status == "to-apply" && len(it.Hits) == 0 {
			t.Errorf("attention's all view lists to-apply %s", it.ID)
		}
	}
}
