package serve

import (
	"testing"
)

type vocabChoice struct {
	Disposition string `json:"disposition"`
	Label       string `json:"label"`
	Says        string `json:"says"`
	Outward     bool   `json:"outward"`
	NeedsUntil  bool   `json:"needs_until"`
}

type vocabKind struct {
	Kind     string        `json:"kind"`
	Allowed  []string      `json:"allowed"`
	Question string        `json:"question"`
	Choices  []vocabChoice `json:"choices"`
}

type vocabNotNow struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Template string `json:"template"`
	Asks     string `json:"asks"`
	Days     int    `json:"days"`
}

type vocabView struct {
	Kinds  []vocabKind   `json:"kinds"`
	NotNow []vocabNotNow `json:"not_now"`
}

func (r *rig) vocab(t *testing.T) vocabView {
	t.Helper()
	var v vocabView
	if c := r.do(t, "GET", "/api/decisions/vocabulary", nil, &v); c != 200 {
		t.Fatalf("vocabulary %d", c)
	}
	return v
}

const (
	saysLeaveOpen = "It stays open and stays in your list, at the bottom. It moves back up when someone replies or it changes."
	saysKeep      = "It stays as it is and stays in your list, at the bottom."
	saysNotNow    = "Hidden until a date or an event you pick, or until someone replies or it changes. Then it asks again."
	saysIgnore    = "casebook never asks about it again. Nothing is done on GitHub."
	saysClose     = "Goes to To apply with your closing comment. Nothing changes until you approve the plan, and you see the comment again before it's posted."
)

// TestVocabularyWordingPerKind pins the question and every choice's label
// and sentence per kind, in order: the page and the guide render only these.
func TestVocabularyWordingPerKind(t *testing.T) {
	r := newRig(t)
	v := r.vocab(t)
	type c = vocabChoice
	want := map[string]struct {
		q       string
		choices []c
	}{
		"pr": {"What should happen to this pull request?", []c{
			{"keep", "Leave it open", saysLeaveOpen, false, false},
			{"merge", "Merge it", "Goes to To apply. Nothing changes until you approve the plan, which shows the exact command.", true, false},
			{"close", "Close it without merging", saysClose, true, false},
			{"wait", "Not now", saysNotNow, false, true},
			{"ignore", "Stop tracking it", saysIgnore, false, false},
		}},
		"issue": {"What should happen to this issue?", []c{
			{"keep", "Leave it open", saysLeaveOpen, false, false},
			{"close", "Close it", saysClose, true, false},
			{"wait", "Not now", saysNotNow, false, true},
			{"ignore", "Stop tracking it", saysIgnore, false, false},
		}},
		"branch": {"What should happen to this branch?", []c{
			{"keep", "Keep it", saysKeep, false, false},
			{"delete", "Delete it", "Goes to To apply. Nothing is deleted until you approve the plan, and a restore record is kept.", true, false},
			{"wait", "Not now", saysNotNow, false, true},
			{"ignore", "Stop tracking it", saysIgnore, false, false},
		}},
		"worktree": {"What should happen to this worktree?", []c{
			{"keep", "Keep it", saysKeep, false, false},
			{"delete", "Remove it", "Goes to To apply. Nothing is removed until you approve the plan, and a restore record is kept.", true, false},
			{"wait", "Not now", saysNotNow, false, true},
			{"ignore", "Stop tracking it", saysIgnore, false, false},
		}},
		"repo": {"What should happen to this repository?", []c{
			{"keep", "Keep it", saysKeep, false, false},
			{"archive", "Archive it on GitHub", "Goes to To apply. Nothing changes until you approve the plan.", true, false},
			{"delete", "Delete it", "Goes to To apply. Nothing is deleted until you approve the plan.", true, false},
			{"wait", "Not now", saysNotNow, false, true},
			{"ignore", "Stop tracking it", saysIgnore, false, false},
		}},
	}
	if len(v.Kinds) != len(want) {
		t.Fatalf("kinds %d, want %d", len(v.Kinds), len(want))
	}
	for _, k := range v.Kinds {
		w, ok := want[k.Kind]
		if !ok {
			t.Errorf("unexpected kind %q", k.Kind)
			continue
		}
		if k.Question != w.q {
			t.Errorf("%s question %q, want %q", k.Kind, k.Question, w.q)
		}
		if len(k.Choices) != len(w.choices) {
			t.Errorf("%s choices %+v, want %+v", k.Kind, k.Choices, w.choices)
			continue
		}
		for i := range w.choices {
			if k.Choices[i] != w.choices[i] {
				t.Errorf("%s choice %d = %+v, want %+v", k.Kind, i, k.Choices[i], w.choices[i])
			}
		}
		// watch is read and accepted (Allowed) but never offered (Choices).
		for _, ch := range k.Choices {
			if ch.Disposition == "watch" {
				t.Errorf("%s offers watch", k.Kind)
			}
		}
		if k.Kind != "worktree" && !contains(k.Allowed, "watch") {
			t.Errorf("%s allowed %v lacks watch", k.Kind, k.Allowed)
		}
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// TestVocabularyOutwardFlags: a choice is outward exactly when it changes
// something outside casebook (it goes to To apply).
func TestVocabularyOutwardFlags(t *testing.T) {
	r := newRig(t)
	outward := map[string]bool{"merge": true, "close": true, "archive": true, "delete": true}
	n := 0
	for _, k := range r.vocab(t).Kinds {
		for _, ch := range k.Choices {
			n++
			if ch.Outward != outward[ch.Disposition] {
				t.Errorf("%s %s outward=%v", k.Kind, ch.Disposition, ch.Outward)
			}
		}
	}
	if n == 0 {
		t.Fatal("no choices")
	}
}

// TestVocabularyNotNowForms: the seven Not now conditions, in order, as
// the endpoint serves them.
func TestVocabularyNotNowForms(t *testing.T) {
	r := newRig(t)
	want := []vocabNotNow{
		{"in-1w", "in 1 week", "date(%s)", "days", 7},
		{"in-1m", "in 1 month", "date(%s)", "days", 30},
		{"on-date", "on a date…", "date(%s)", "date", 0},
		{"pr-merges", "when a PR merges…", "merged(%s)", "pr", 0},
		{"closes", "when a PR or issue closes…", "closed(%s)", "pr-or-issue", 0},
		{"quiet-90", "when it goes quiet for 90 days", "inactive(90d)", "", 0},
		{"release", "when a repo releases…", "released(%s)", "repo", 0},
	}
	got := r.vocab(t).NotNow
	if len(got) != len(want) {
		t.Fatalf("not_now %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("not_now[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
