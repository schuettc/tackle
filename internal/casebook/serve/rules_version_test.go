package serve

import (
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/casebook/rules"
)

func ruleDetail(t *testing.T, r *rig, id string) RuleDetailView {
	t.Helper()
	var d RuleDetailView
	if code := r.do(t, "GET", "/api/rule?id="+id, nil, &d); code != 200 {
		t.Fatalf("GET rule %s: %d", id, code)
	}
	return d
}

// A page activates exactly the rule it shows: activate and save carry the
// version the page read, and serve refuses (409) when its copy has moved on,
// here because the agent changed the proposal from archive to delete.
func TestActivateAndSaveRefuseAStaleVersion(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	agentDraft := func(disp string) {
		t.Helper()
		code := r.do(t, "POST", "/api/agent/rule-draft", map[string]any{
			"session": "s1",
			"rule": map[string]any{
				"id":      "agent-arch",
				"name":    "Dormant repos",
				"status":  "draft",
				"match":   []map[string]any{{"field": "kind", "op": "is", "value": "repo"}},
				"propose": map[string]any{"disposition": disp},
			},
		}, nil)
		if code != 200 {
			t.Fatalf("agent draft %d", code)
		}
	}
	agentDraft("archive")
	seen := ruleDetail(t, r, "agent-arch")
	if seen.Version == "" {
		t.Fatal("GET /api/rule carries no version")
	}
	agentDraft("delete")
	now := ruleDetail(t, r, "agent-arch")
	if now.Version == seen.Version {
		t.Fatal("changing the proposal did not change the version")
	}

	var out map[string]any
	code := r.do(t, "POST", "/api/rules/activate", map[string]any{"id": "agent-arch", "version": seen.Version}, &out)
	if code != 409 {
		t.Fatalf("activate with the version Court saw: want 409, got %d (%v)", code, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "changed") {
		t.Errorf("409 message %q does not say the rule changed", msg)
	}
	if ru, _ := r.App.Repo.ReadRule("agent-arch"); ru.Status != rules.StatusDraft {
		t.Fatalf("a refused activate left the rule %q", ru.Status)
	}

	// Saving Court's edits over the agent's, from the stale version: refused.
	body := map[string]any{
		"id":      "agent-arch",
		"name":    "Dormant repos",
		"status":  "draft",
		"match":   []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
		"propose": map[string]any{"disposition": "close"},
	}
	if code := r.do(t, "POST", "/api/rules/draft?version="+seen.Version, body, nil); code != 409 {
		t.Fatalf("save from a stale version: want 409, got %d", code)
	}
	if ru, _ := r.App.Repo.ReadRule("agent-arch"); ru.Propose.Disposition != "delete" || ru.Match[0].Value != "repo" {
		t.Fatalf("a refused save changed the rule: %+v", ru)
	}

	// From the current version both go through.
	var saved RuleDetailView
	if code := r.do(t, "POST", "/api/rules/draft?version="+now.Version, body, &saved); code != 200 {
		t.Fatalf("save from the current version: %d", code)
	}
	if code := r.do(t, "POST", "/api/rules/activate", map[string]any{"id": "agent-arch", "version": saved.Version}, nil); code != 200 {
		t.Fatalf("activate the current version: %d", code)
	}
}

// A rename moves the version too (it doesn't move edited_at): the page must
// not activate a rule whose name it didn't show.
func TestRenameChangesVersion(t *testing.T) {
	r, _ := newRuleRig(t)
	a := ruleDetail(t, r, "kind-repo")
	body := map[string]any{
		"id":      "kind-repo",
		"name":    "Repos, renamed",
		"status":  "draft",
		"match":   []map[string]any{{"field": "kind", "op": "is", "value": "repo"}},
		"propose": map[string]any{"disposition": "archive"},
	}
	if code := r.do(t, "POST", "/api/rules/draft", body, nil); code != 200 {
		t.Fatalf("rename %d", code)
	}
	if b := ruleDetail(t, r, "kind-repo"); b.Version == a.Version {
		t.Fatal("a rename kept the version")
	}
}

// create=1 makes a new draft and never overwrites an existing rule.
func TestDraftCreateRefusesAnExistingID(t *testing.T) {
	r, _ := newRuleRig(t)
	body := map[string]any{"id": "kind-repo", "name": "Clash", "status": "draft", "match": []any{}, "propose": map[string]any{"disposition": "keep"}}
	var out map[string]any
	if code := r.do(t, "POST", "/api/rules/draft?create=1", body, &out); code != 409 {
		t.Fatalf("create over an existing id: want 409, got %d (%v)", code, out)
	}
	if ru, _ := r.App.Repo.ReadRule("kind-repo"); ru.Name != "Repos rule" {
		t.Fatalf("create overwrote the rule: %q", ru.Name)
	}
	body["id"] = "brand-new"
	var made RuleDetailView
	if code := r.do(t, "POST", "/api/rules/draft?create=1", body, &made); code != 200 {
		t.Fatalf("create a new id: %d", code)
	}
	if made.Rule.Name != "Clash" || made.Rule.Status != rules.StatusDraft {
		t.Fatalf("created %+v", made.Rule)
	}
}
