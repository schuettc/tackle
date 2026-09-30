package serve

import (
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/casebook/rules"
)

// writeRuleFile commits a rule file as a person or an older casebook might
// have written it, without serve's validation.
func writeRuleFile(t *testing.T, r *rig, id, body string) {
	t.Helper()
	if _, err := r.App.Repo.WriteFile("rules/"+id+".toml", []byte(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.App.Repo.Commit(ctx, "hand-written rule "+id); err != nil {
		t.Fatal(err)
	}
}

const badBotActive = `id = "bad-bot"
name = "Bot PRs"
status = "active"
created_by = "court"
created_at = 2026-09-27T10:00:00Z
edited_at = 2026-09-27T10:00:00Z
[[match]]
field = "kind"
op = "is"
value = "pr"
[[match]]
field = "bot"
op = "is"
value = "yes"
[propose]
disposition = "close"
`

// Spec §4.1: an unknown value is a validation error. A rule file holding one
// (hand-written, or older than the check) is listed and shown as invalid,
// naming the condition, never previewed or proposed from, and Court can get
// it out of that state: deactivate it, fix it and save.
func TestInvalidRuleIsShownRefusedAndRecoverable(t *testing.T) {
	r := newRig(t)
	writeRuleFile(t, r, "bad-bot", badBotActive)
	if err := r.s.rebuild(ctx); err != nil {
		t.Fatalf("rebuild with an invalid active rule: %v", err)
	}

	var list RulesView
	if code := r.do(t, "GET", "/api/rules", nil, &list); code != 200 {
		t.Fatalf("rules %d", code)
	}
	var row *RuleRow
	for i := range list.Rules {
		if list.Rules[i].Rule.ID == "bad-bot" {
			row = &list.Rules[i]
		}
	}
	if row == nil {
		t.Fatal("the invalid rule is not listed")
	}
	if !strings.HasPrefix(row.Invalid, "condition 2: ") {
		t.Errorf("list row invalid = %q, want serve's message naming condition 2", row.Invalid)
	}

	d := ruleDetail(t, r, "bad-bot")
	if !strings.HasPrefix(d.Invalid, "condition 2: ") || !strings.Contains(d.Invalid, `"yes"`) {
		t.Errorf("detail invalid = %q", d.Invalid)
	}
	if d.Matches.Total != 0 || len(d.Matches.Page) != 0 {
		t.Errorf("an invalid rule was previewed: %+v", d.Matches)
	}

	var out map[string]any
	if code := r.do(t, "POST", "/api/rules/propose-once", map[string]any{"id": "bad-bot"}, &out); code != 400 {
		t.Errorf("propose-once of an invalid rule: want 400, got %d (%v)", code, out)
	}
	if code := r.do(t, "POST", "/api/rules/activate", map[string]any{"id": "bad-bot"}, &out); code != 400 {
		t.Errorf("activate of an invalid rule: want 400, got %d", code)
	}

	// Deactivate: allowed, and the rule (with its bad value) is kept.
	var off RuleDetailView
	if code := r.do(t, "POST", "/api/rules/deactivate", map[string]any{"id": "bad-bot"}, &off); code != 200 {
		t.Fatalf("deactivate an invalid rule: %d", code)
	}
	ru, err := r.App.Repo.ReadRule("bad-bot")
	if err != nil || ru == nil || ru.Status != rules.StatusDraft || ru.Match[1].Value != "yes" {
		t.Fatalf("after deactivate: %+v %v", ru, err)
	}
	if !strings.HasPrefix(off.Invalid, "condition 2: ") {
		t.Errorf("deactivate's reply invalid = %q", off.Invalid)
	}

	// Fix and save.
	fixed := map[string]any{
		"id": "bad-bot", "name": "Bot PRs", "status": "draft",
		"match": []map[string]any{
			{"field": "kind", "op": "is", "value": "pr"},
			{"field": "bot", "op": "is", "value": "true"},
		},
		"propose": map[string]any{"disposition": "close"},
	}
	var saved RuleDetailView
	if code := r.do(t, "POST", "/api/rules/draft", fixed, &saved); code != 200 {
		t.Fatalf("save the fix: %d", code)
	}
	if saved.Invalid != "" {
		t.Errorf("the fixed rule is still invalid: %q", saved.Invalid)
	}
}

// An active invalid rule is fixed by saving directly: the save is allowed
// (serve otherwise edits only drafts) and leaves it a draft for Court to
// activate again.
func TestInvalidActiveRuleCanBeSavedFixed(t *testing.T) {
	r := newRig(t)
	writeRuleFile(t, r, "bad-bot", badBotActive)
	fixed := map[string]any{
		"id": "bad-bot", "name": "Bot PRs", "status": "draft",
		"match": []map[string]any{
			{"field": "kind", "op": "is", "value": "pr"},
			{"field": "bot", "op": "is", "value": "true"},
		},
		"propose": map[string]any{"disposition": "close"},
	}
	var saved RuleDetailView
	if code := r.do(t, "POST", "/api/rules/draft", fixed, &saved); code != 200 {
		t.Fatalf("save a fix over an invalid active rule: %d", code)
	}
	if saved.Rule.Status != rules.StatusDraft || saved.Invalid != "" {
		t.Fatalf("after the fix: status %q invalid %q", saved.Rule.Status, saved.Invalid)
	}
	// A valid active rule still can't be edited through the draft route.
	if code := r.do(t, "POST", "/api/rules/activate", map[string]any{"id": "bad-bot"}, nil); code != 200 {
		t.Fatalf("activate the fixed rule: %d", code)
	}
	if code := r.do(t, "POST", "/api/rules/draft", fixed, nil); code != 400 {
		t.Fatalf("editing a valid active rule: want 400, got %d", code)
	}
}

// A file that isn't a rule at all is listed and shown as invalid with its
// parse error, not dropped, and a save from the page replaces it.
func TestUnreadableRuleFileIsListedAndReplaceable(t *testing.T) {
	r := newRig(t)
	writeRuleFile(t, r, "broken", "this is [not toml\n")
	if err := r.s.rebuild(ctx); err != nil {
		t.Fatalf("rebuild with an unreadable rule: %v", err)
	}
	var list RulesView
	r.do(t, "GET", "/api/rules", nil, &list)
	found := false
	for _, row := range list.Rules {
		if row.Rule.ID == "broken" {
			found = true
			if row.Invalid == "" {
				t.Error("the unreadable rule's row is not marked invalid")
			}
		}
	}
	if !found {
		t.Fatal("the unreadable rule was dropped from the list")
	}
	d := ruleDetail(t, r, "broken")
	if d.Invalid == "" || d.Rule.ID != "broken" {
		t.Fatalf("detail of an unreadable rule: %+v", d)
	}
	body := map[string]any{
		"id": "broken", "name": "Now a rule", "status": "draft",
		"match":   []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
		"propose": map[string]any{"disposition": "keep"},
	}
	var saved RuleDetailView
	if code := r.do(t, "POST", "/api/rules/draft?version="+d.Version, body, &saved); code != 200 {
		t.Fatalf("save over the unreadable file: %d", code)
	}
	if saved.Invalid != "" || saved.Rule.Name != "Now a rule" {
		t.Fatalf("after the save: %+v", saved)
	}
}

// A draft needs only an id and a name to be saved (the page's "new rule");
// until it proposes something it is shown as not valid, and it can't be
// activated.
func TestNewDraftNeedsOnlyIDAndName(t *testing.T) {
	r := newRig(t)
	var made RuleDetailView
	body := map[string]any{"id": "fresh", "name": "Fresh rule", "status": "draft", "match": []any{}, "propose": map[string]any{}}
	if code := r.do(t, "POST", "/api/rules/draft?create=1", body, &made); code != 200 {
		t.Fatalf("create with only id and name: %d", code)
	}
	if made.Invalid == "" {
		t.Error("a rule with no disposition is not marked invalid")
	}
	if code := r.do(t, "POST", "/api/rules/activate", map[string]any{"id": "fresh"}, nil); code != 400 {
		t.Errorf("activate with no disposition: want 400, got %d", code)
	}
}
