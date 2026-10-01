package serve

import (
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/item"
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
