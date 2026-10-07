package serve

import (
	"net/url"
	"testing"

	"github.com/schuettc/tackle/internal/casebook/item"
)

// TestItemURLPerKind: serve builds an item's GitHub page from its key, for
// the page's kicker link. A PR, an issue and a repo have one; a branch and a
// worktree have none (an empty url).
func TestItemURLPerKind(t *testing.T) {
	r := newRig(t)
	for _, tc := range []struct{ key, want string }{
		{"pr:schuettc/hail#3", "https://github.com/schuettc/hail/pull/3"},
		{"issue:schuettc/hail#4", "https://github.com/schuettc/hail/issues/4"},
		{"repo:schuettc/hail", "https://github.com/schuettc/hail"},
		{"branch:schuettc/hail@feat/client", ""},
	} {
		var out struct {
			Item struct {
				Key string `json:"key"`
				URL string `json:"url"`
			} `json:"item"`
		}
		if c := r.do(t, "GET", "/api/item?key="+url.QueryEscape(tc.key), nil, &out); c != 200 {
			t.Fatalf("GET /api/item %s: %d", tc.key, c)
		}
		if out.Item.Key != tc.key || out.Item.URL != tc.want {
			t.Errorf("%s: url %q, want %q", tc.key, out.Item.URL, tc.want)
		}
		// The list says the same as the detail.
		var list struct {
			Items []struct {
				Key string `json:"key"`
				URL string `json:"url"`
			} `json:"items"`
		}
		if c := r.do(t, "GET", "/api/items?view=tracked&limit=500", nil, &list); c != 200 {
			t.Fatalf("GET /api/items: %d", c)
		}
		found := false
		for _, it := range list.Items {
			if it.Key == tc.key {
				found = true
				if it.URL != tc.want {
					t.Errorf("list %s: url %q, want %q", tc.key, it.URL, tc.want)
				}
			}
		}
		if !found {
			t.Errorf("list: %s not tracked", tc.key)
		}
	}
	// A worktree has no GitHub page.
	if got := githubURL(item.WorktreeKey("mbp", "/home/court/hail")); got != "" {
		t.Errorf("worktree: url %q, want empty", got)
	}
}
