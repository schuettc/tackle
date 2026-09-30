package serve

import (
	"testing"
	"time"
)

// testClock is an advancing clock for serve and its proposal store, so the
// order of an edit, a rejection and a re-save is the order they happened in.
type testClock struct{ t time.Time }

func (c *testClock) now() time.Time      { return c.t }
func (c *testClock) add(d time.Duration) { c.t = c.t.Add(d) }

func useClock(r *rig) *testClock {
	c := &testClock{t: time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)}
	r.s.Now = c.now
	r.s.Props.Now = c.now
	return c
}

// proposeOnceAndReject proposes rule id's matches and rejects them all.
func proposeOnceAndReject(t *testing.T, r *rig, id string) int {
	t.Helper()
	var res ProposeResult
	if code := r.do(t, "POST", "/api/rules/propose-once", map[string]any{"id": id}, &res); code != 200 {
		t.Fatalf("propose-once %d", code)
	}
	if res.Proposed == 0 {
		t.Fatal("the rule proposed nothing; the fixture must match")
	}
	ids := make([]int64, 0, len(res.Proposals))
	for _, p := range res.Proposals {
		ids = append(ids, p.ID)
	}
	if code := r.do(t, "POST", "/api/proposals/reject", map[string]any{"ids": ids, "reason": "no"}, nil); code != 200 {
		t.Fatalf("reject %d", code)
	}
	return res.Proposed
}

func reproposed(t *testing.T, r *rig, id string) int {
	t.Helper()
	var res ProposeResult
	if code := r.do(t, "POST", "/api/rules/propose-once", map[string]any{"id": id}, &res); code != 200 {
		t.Fatalf("propose-once %d", code)
	}
	return res.Proposed
}

func diskEditedAt(t *testing.T, r *rig, id string) time.Time {
	t.Helper()
	ru, err := r.App.Repo.ReadRule(id)
	if err != nil || ru == nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return ru.EditedAt
}

// Spec §4.1: edited_at is the last edit of the conditions or the proposal.
// Court re-saving an unchanged draft, or only renaming it, changes neither,
// so his rejections for the rule stand (§4.2).
func TestCourtUnchangedDraftKeepsEditedAt(t *testing.T) {
	r := newRig(t)
	c := useClock(r)
	draft := map[string]any{
		"id":      "keep-me",
		"name":    "Repos",
		"status":  "draft",
		"match":   []map[string]any{{"field": "kind", "op": "is", "value": "repo"}},
		"propose": map[string]any{"disposition": "archive"},
	}
	if code := r.do(t, "POST", "/api/rules/draft", draft, nil); code != 200 {
		t.Fatalf("create %d", code)
	}
	made := diskEditedAt(t, r, "keep-me")
	c.add(time.Minute)
	proposeOnceAndReject(t, r, "keep-me")
	c.add(time.Minute)

	if code := r.do(t, "POST", "/api/rules/draft", draft, nil); code != 200 {
		t.Fatalf("re-save %d", code)
	}
	if got := diskEditedAt(t, r, "keep-me"); !got.Equal(made) {
		t.Errorf("an unchanged re-save moved edited_at %v -> %v", made, got)
	}
	draft["name"] = "Repos, renamed"
	c.add(time.Minute)
	if code := r.do(t, "POST", "/api/rules/draft", draft, nil); code != 200 {
		t.Fatalf("rename %d", code)
	}
	if got := diskEditedAt(t, r, "keep-me"); !got.Equal(made) {
		t.Errorf("a rename moved edited_at %v -> %v", made, got)
	}
	if n := reproposed(t, r, "keep-me"); n != 0 {
		t.Errorf("after an unchanged save and a rename, %d rejected items were proposed again", n)
	}

	// A real edit (the proposal) does move it, and re-opens the rejections.
	draft["propose"] = map[string]any{"disposition": "keep"}
	c.add(time.Minute)
	if code := r.do(t, "POST", "/api/rules/draft", draft, nil); code != 200 {
		t.Fatalf("edit %d", code)
	}
	if got := diskEditedAt(t, r, "keep-me"); !got.After(made) {
		t.Errorf("an edit of the proposal left edited_at at %v", got)
	}
}

// The same for an agent re-drafting its own rule.
func TestAgentUnchangedRedraftKeepsEditedAt(t *testing.T) {
	r := newRig(t)
	c := useClock(r)
	r.attach(t, "s1")
	body := func(name string, match string) map[string]any {
		return map[string]any{
			"session": "s1",
			"rule": map[string]any{
				"id":      "agent-keep",
				"name":    name,
				"status":  "draft",
				"match":   []map[string]any{{"field": "kind", "op": "is", "value": match}},
				"propose": map[string]any{"disposition": "archive"},
			},
		}
	}
	if code := r.do(t, "POST", "/api/agent/rule-draft", body("Repos", "repo"), nil); code != 200 {
		t.Fatalf("agent create %d", code)
	}
	made := diskEditedAt(t, r, "agent-keep")
	c.add(time.Minute)
	proposeOnceAndReject(t, r, "agent-keep")
	c.add(time.Minute)
	for _, name := range []string{"Repos", "Repos (renamed)"} {
		if code := r.do(t, "POST", "/api/agent/rule-draft", body(name, "repo"), nil); code != 200 {
			t.Fatalf("agent re-draft %d", code)
		}
		if got := diskEditedAt(t, r, "agent-keep"); !got.Equal(made) {
			t.Errorf("agent re-draft %q moved edited_at %v -> %v", name, made, got)
		}
		c.add(time.Minute)
	}
	if n := reproposed(t, r, "agent-keep"); n != 0 {
		t.Errorf("after the agent's unchanged re-drafts, %d rejected items were proposed again", n)
	}
	if code := r.do(t, "POST", "/api/agent/rule-draft", body("Repos", "branch"), nil); code != 200 {
		t.Fatalf("agent edit %d", code)
	}
	if got := diskEditedAt(t, r, "agent-keep"); !got.After(made) {
		t.Errorf("an agent's edit of the conditions left edited_at at %v", got)
	}
}
