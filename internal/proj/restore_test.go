package proj

import (
	"slices"
	"testing"
)

func names(ss []SavedSession) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Name)
	}
	return out
}

func TestSavedSessionsOrder(t *testing.T) {
	rec := Record{Version: 1,
		Sessions: map[string]Entry{
			"a/1": {Project: "a"}, "b/1": {Project: "b"}, "c/1": {Project: "c"},
			"z/9": {Project: "z"}, "d/2": {Project: "d"},
		},
		Layout: GhosttyLayout{Windows: [][]string{{"b/1", "a/1"}, {"c/1", "ghost/1", "b/1"}}},
	}
	got := SavedSessions(rec, LiveState{}, Transcripts{})
	if want := []string{"b/1", "a/1", "c/1", "d/2", "z/9"}; !slices.Equal(names(got), want) {
		t.Fatalf("order = %v want %v", names(got), want)
	}
	var wins []int
	for _, s := range got {
		wins = append(wins, s.Window)
	}
	if want := []int{1, 1, 2, 0, 0}; !slices.Equal(wins, want) {
		t.Fatalf("windows = %v want %v", wins, want)
	}
}

func TestSavedSessionsFlags(t *testing.T) {
	tr := transcriptTree(t)
	rec := Record{Version: 1, Sessions: map[string]Entry{
		"a/1": {Conversation: "01a0ed7a"},
		"b/1": {Conversation: "gone"},
		"c/1": {},
	}}
	live := LiveState{
		Running:  map[string]bool{"a/1": true, "c/1": true},
		Attached: map[string]bool{"a/1": true},
	}
	by := map[string]SavedSession{}
	for _, s := range SavedSessions(rec, live, tr) {
		by[s.Name] = s
	}
	if a := by["a/1"]; !a.Running || !a.Attached || !a.Transcript || a.NeedsRestore() {
		t.Fatalf("a/1 = %+v", a)
	}
	if b := by["b/1"]; b.Transcript || b.Running || !b.NeedsRestore() {
		t.Fatalf("b/1 = %+v", b)
	}
	if c := by["c/1"]; !c.Running || c.Attached || !c.NeedsRestore() {
		t.Fatalf("c/1 (running, no client) = %+v", c)
	}
}

func TestPlanGhosttySkipsAttached(t *testing.T) {
	sel := []SavedSession{
		{Name: "a", Window: 1, Running: true, Attached: true},
		{Name: "b", Window: 1},
		{Name: "c", Window: 2, Running: true, Attached: true},
		{Name: "d", Window: 0},
	}
	got := PlanGhostty(sel)
	if len(got) != 2 || !slices.Equal(names(got[0]), []string{"b"}) || !slices.Equal(names(got[1]), []string{"d"}) {
		t.Fatalf("plan = %v", got)
	}
}

func TestPlanGhosttyUnplacedLast(t *testing.T) {
	sel := []SavedSession{{Name: "u", Window: 0}, {Name: "w2", Window: 2}, {Name: "w1", Window: 1}}
	got := PlanGhostty(sel)
	var order []string
	for _, g := range got {
		order = append(order, names(g)...)
	}
	if !slices.Equal(order, []string{"w1", "w2", "u"}) {
		t.Fatalf("order = %v want w1 w2 u", order)
	}
}

func TestSavedSessionsEmptyRecord(t *testing.T) {
	if got := SavedSessions(Record{}, LiveState{}, Transcripts{}); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
