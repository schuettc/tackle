package serve

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/propose"
)

// TestListFiltersByRelationBotAndAge verifies that Query.Relation, Query.Bot
// and Query.Age each independently narrow the result. The test builds a
// minimal in-memory Index with known items.
func TestListFiltersByRelationBotAndAge(t *testing.T) {
	// Build a small set of items with distinct relation, bot and age values.
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	// pr:schuettc/hail#1 — incoming, human author, created 57 days ago
	prIncoming := engine.Item{
		ID:          "pr:schuettc/hail#1",
		Kind:        item.Kind("pr"),
		Repo:        "schuettc/hail",
		Relation:    "incoming",
		Status:      item.StatusNew,
		Author:      "bob",
		AuthorIsBot: false,
		CreatedAt:   now.Add(-57 * 24 * time.Hour),
		Title:       "fix nudge",
		Observed:    item.Observed{Known: true, Exists: true, State: "OPEN"},
	}

	// pr:schuettc/hail#2 — outgoing, bot author, created 5 days ago
	prOutgoing := engine.Item{
		ID:          "pr:schuettc/hail#2",
		Kind:        item.Kind("pr"),
		Repo:        "schuettc/hail",
		Relation:    "outgoing",
		Status:      item.StatusNew,
		Author:      "dependabot[bot]",
		AuthorIsBot: true,
		CreatedAt:   now.Add(-5 * 24 * time.Hour),
		Title:       "bump deps",
		Observed:    item.Observed{Known: true, Exists: true, State: "OPEN"},
	}

	// issue:schuettc/hail#3 — self, human, created 60 days ago
	issueSelf := engine.Item{
		ID:          "issue:schuettc/hail#3",
		Kind:        item.Kind("issue"),
		Repo:        "schuettc/hail",
		Relation:    "own",
		Status:      item.StatusNew,
		Author:      "schuettc",
		AuthorIsBot: false,
		CreatedAt:   now.Add(-60 * 24 * time.Hour),
		Title:       "chime improvement",
		Observed:    item.Observed{Known: true, Exists: true, State: "OPEN"},
	}

	idx := &Index{}
	idx.set(engine.Result{Items: []engine.Item{prIncoming, prOutgoing, issueSelf}}, "abc123", now)
	pending := map[string]interface{}{}
	_ = pending

	// Helper to list with no pending proposals.
	// Always sets Now to the fixed clock so the age filter can never drift.
	list := func(q Query) []ItemView {
		q.Now = now
		items, _ := idx.List(q, nil)
		return items
	}

	t.Run("relation=incoming narrows to one item", func(t *testing.T) {
		got := list(Query{Relation: "incoming"})
		if len(got) != 1 || got[0].ID != prIncoming.ID {
			t.Fatalf("relation=incoming: got %v", ids(got))
		}
	})

	t.Run("relation=outgoing narrows to one item", func(t *testing.T) {
		got := list(Query{Relation: "outgoing"})
		if len(got) != 1 || got[0].ID != prOutgoing.ID {
			t.Fatalf("relation=outgoing: got %v", ids(got))
		}
	})

	t.Run("bot=bot narrows to bot-authored items", func(t *testing.T) {
		got := list(Query{Bot: "bot"})
		if len(got) != 1 || got[0].ID != prOutgoing.ID {
			t.Fatalf("bot=bot: got %v", ids(got))
		}
	})

	t.Run("bot=human narrows to human-authored items", func(t *testing.T) {
		got := list(Query{Bot: "human"})
		if len(got) != 2 {
			t.Fatalf("bot=human: want 2, got %v", ids(got))
		}
	})

	t.Run("age=30d narrows to items older than 30 days", func(t *testing.T) {
		// prIncoming (57d) and issueSelf (60d) qualify; prOutgoing (5d) does not.
		got := list(Query{Age: "30d"})
		if len(got) != 2 {
			t.Fatalf("age=30d: want 2, got %v", ids(got))
		}
		for _, it := range got {
			if it.ID == prOutgoing.ID {
				t.Fatalf("age=30d: prOutgoing (5d) should not appear")
			}
		}
	})

	t.Run("relation + bot combined", func(t *testing.T) {
		// incoming AND human → prIncoming only
		got := list(Query{Relation: "incoming", Bot: "human"})
		if len(got) != 1 || got[0].ID != prIncoming.ID {
			t.Fatalf("relation=incoming bot=human: got %v", ids(got))
		}
	})

	t.Run("age=1h narrows to items older than 1 hour", func(t *testing.T) {
		// All three items are >1h old (57d, 5d, 60d) → all 3 match.
		got := list(Query{Age: "1h"})
		if len(got) != 3 {
			t.Fatalf("age=1h: want 3, got %v", ids(got))
		}
	})

	t.Run("age=1w narrows to items older than 1 week", func(t *testing.T) {
		// prIncoming (57d) and issueSelf (60d) are >1w; prOutgoing (5d) is not.
		got := list(Query{Age: "1w"})
		if len(got) != 2 {
			t.Fatalf("age=1w: want 2, got %v", ids(got))
		}
		for _, it := range got {
			if it.ID == prOutgoing.ID {
				t.Fatalf("age=1w: prOutgoing (5d) should not appear")
			}
		}
	})
}

func ids(items []ItemView) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

// waitingPR is a pull request in the waiting view (the policy flags it).
func waitingPR(n int, newActivity bool) engine.Item {
	return engine.Item{
		ID:          fmt.Sprintf("pr:schuettc/hail#%d", n),
		Kind:        item.KindPR,
		Repo:        "schuettc/hail",
		Status:      item.StatusNew,
		NewActivity: newActivity,
		Hits:        []item.Hit{{Rule: "incoming-no-reply"}},
		Observed:    item.Observed{Known: true, Exists: true, State: "OPEN"},
	}
}

// TestListOrdersByGroup: an Attention view lists its items group by group
// (the biggest group first, the not recommended last), new activity first
// within a group, and returns the groups with their counts.
func TestListOrdersByGroup(t *testing.T) {
	idx := &Index{}
	var its []engine.Item
	for n := 1; n <= 6; n++ {
		its = append(its, waitingPR(n, n == 6))
	}
	idx.set(engine.Result{Items: its}, "abc", time.Now())
	pending := map[string]propose.Proposal{
		"pr:schuettc/hail#3": {Key: "pr:schuettc/hail#3", Source: "pi:s1", Disposition: "merge"},
		"pr:schuettc/hail#4": {Key: "pr:schuettc/hail#4", Source: "pi:s1", Disposition: "close"},
		"pr:schuettc/hail#5": {Key: "pr:schuettc/hail#5", Source: "pi:s1", Disposition: "close"},
		"pr:schuettc/hail#6": {Key: "pr:schuettc/hail#6", Source: "pi:s1", Disposition: "close"},
	}
	q := Query{View: ViewWaiting}
	items, total := idx.List(q, pending)
	want := []string{"pr:schuettc/hail#6", "pr:schuettc/hail#4", "pr:schuettc/hail#5", "pr:schuettc/hail#3", "pr:schuettc/hail#1", "pr:schuettc/hail#2"}
	if got := ids(items); total != 6 || !slices.Equal(got, want) {
		t.Fatalf("list %v (total %d), want %v", got, total, want)
	}
	closeID, mergeID := "pi:s1|close|Close it without merging", "pi:s1|merge|Merge it"
	for i, g := range []string{closeID, closeID, closeID, mergeID, "none", "none"} {
		if items[i].Group != g {
			t.Errorf("%s is in group %q, want %q", items[i].ID, items[i].Group, g)
		}
	}
	wantGroups := []GroupView{
		{ID: closeID, Source: "pi:s1", Disposition: "close", Label: "Close it without merging", Outward: true, Many: "Close {n}", Total: 3},
		{ID: mergeID, Source: "pi:s1", Disposition: "merge", Label: "Merge it", Outward: true, Many: "Merge {n}", Total: 1},
		{ID: "none", Label: "Not recommended yet", Total: 2},
	}
	if got := idx.Groups(q, pending); !reflect.DeepEqual(got, wantGroups) {
		t.Fatalf("groups\n %+v\nwant\n %+v", got, wantGroups)
	}
	// A group filter lists only that group.
	q.Group = mergeID
	if items, total := idx.List(q, pending); total != 1 || items[0].ID != "pr:schuettc/hail#3" {
		t.Fatalf("group %s lists %v (total %d)", mergeID, ids(items), total)
	}
	// To apply and tracked keep key order and have no groups.
	if g := idx.Groups(Query{View: ViewTracked}, pending); len(g) != 0 {
		t.Fatalf("tracked has groups %+v", g)
	}
	if items, _ := idx.List(Query{View: ViewTracked}, pending); !slices.IsSorted(ids(items)) {
		t.Fatalf("tracked not in key order: %v", ids(items))
	}
}

// TestGroupSplitsByKindLabel: a PR close and an issue close from one source
// are two groups, each with its kind's label; watch reads as wait.
func TestGroupSplitsByKindLabel(t *testing.T) {
	idx := &Index{}
	issue := waitingPR(2, false)
	issue.ID, issue.Kind = "issue:schuettc/hail#2", item.KindIssue
	idx.set(engine.Result{Items: []engine.Item{waitingPR(1, false), issue, waitingPR(3, false)}}, "abc", time.Now())
	pending := map[string]propose.Proposal{
		"pr:schuettc/hail#1":    {Key: "pr:schuettc/hail#1", Source: "pi:s1", Disposition: "close"},
		"issue:schuettc/hail#2": {Key: "issue:schuettc/hail#2", Source: "pi:s1", Disposition: "close"},
		"pr:schuettc/hail#3":    {Key: "pr:schuettc/hail#3", Source: "pi:s1", Disposition: "watch", Until: "inactive(90d)"},
	}
	var labels []string
	for _, g := range idx.Groups(Query{View: ViewWaiting}, pending) {
		labels = append(labels, g.Label)
	}
	// Equal totals: the order of the disposition in the kind's choices
	// (an issue's close is its second choice, a PR's its third).
	if want := []string{"Close it", "Close it without merging", "Not now"}; !slices.Equal(labels, want) {
		t.Fatalf("labels %v, want %v", labels, want)
	}
	items, _ := idx.List(Query{View: ViewWaiting}, pending)
	if items[2].Group != "pi:s1|wait|Not now" {
		t.Fatalf("watch's group %q", items[2].Group)
	}
}
