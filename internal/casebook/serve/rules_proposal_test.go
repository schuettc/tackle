package serve

import (
	"slices"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/casebook/rules"
)

func draftBody(id, disp, until string, match ...map[string]any) map[string]any {
	if match == nil {
		match = []map[string]any{}
	}
	return map[string]any{
		"id": id, "name": id, "status": "draft", "match": match,
		"propose": map[string]any{"disposition": disp, "until": until},
	}
}

func kindCond(k string) map[string]any {
	return map[string]any{"field": "kind", "op": "is", "value": k}
}

// N1: a proposal serve would refuse for every item (an until it can't
// parse, a disposition the rule's kind doesn't allow) makes the rule
// invalid: shown, never activated or proposed from, and fixable.
func TestProposalFieldsMakeARuleInvalid(t *testing.T) {
	r := newRig(t)
	for _, tc := range []struct {
		id, disp, until, kind, want string
	}{
		{"wait-30d", "wait", "30d", "repo", `invalid until "30d"`},
		{"archive-branch", "archive", "", "branch", "archive can't be proposed for a branch"},
	} {
		var saved RuleDetailView
		if code := r.do(t, "POST", "/api/rules/draft", draftBody(tc.id, tc.disp, tc.until, kindCond(tc.kind)), &saved); code != 200 {
			t.Fatalf("%s: save %d", tc.id, code)
		}
		if !strings.Contains(saved.Invalid, tc.want) {
			t.Errorf("%s: invalid = %q, want it to say %q", tc.id, saved.Invalid, tc.want)
		}
		var out map[string]any
		if code := r.do(t, "POST", "/api/rules/activate", map[string]any{"id": tc.id, "version": saved.Version}, &out); code != 400 {
			t.Errorf("%s: activate want 400, got %d", tc.id, code)
		}
		if code := r.do(t, "POST", "/api/rules/propose-once", map[string]any{"id": tc.id}, &out); code != 400 {
			t.Errorf("%s: propose-once want 400, got %d", tc.id, code)
		}
		if ru, _ := r.App.Repo.ReadRule(tc.id); ru == nil || ru.Status != rules.StatusDraft {
			t.Errorf("%s: after the refusals: %+v", tc.id, ru)
		}
	}
	// Fixed, it activates.
	var fixed RuleDetailView
	if code := r.do(t, "POST", "/api/rules/draft", draftBody("wait-30d", "wait", "inactive(30d)", kindCond("repo")), &fixed); code != 200 || fixed.Invalid != "" {
		t.Fatalf("the fix: %d %q", code, fixed.Invalid)
	}
	if code := r.do(t, "POST", "/api/rules/activate", map[string]any{"id": "wait-30d", "version": fixed.Version}, nil); code != 200 {
		t.Fatalf("activate the fixed rule: %d", code)
	}
}

// N1: the page's disposition picker offers what the rule's kind allows:
// serve says which, with every preview and every rule it sends.
func TestPreviewSaysWhichDispositionsTheRuleMayPropose(t *testing.T) {
	r := newRig(t)
	var p MatchPreview
	if code := r.do(t, "POST", "/api/rules/preview", draftBody("x", "", "", kindCond("branch")), &p); code != 200 {
		t.Fatalf("preview %d", code)
	}
	if want := []string{"keep", "delete", "wait", "watch", "ignore"}; !slices.Equal(p.Dispositions, want) {
		t.Errorf("branch: dispositions %v, want %v", p.Dispositions, want)
	}
	if code := r.do(t, "POST", "/api/rules/preview", draftBody("x", "", ""), &p); code != 200 {
		t.Fatalf("preview %d", code)
	}
	if want := []string{"keep", "wait", "ignore"}; !slices.Equal(p.Dispositions, want) {
		t.Errorf("no kind condition: dispositions %v, want %v", p.Dispositions, want)
	}
	// A rule whose conditions aren't valid (a file as written by hand)
	// isn't previewed, but still says what it may propose.
	writeRuleFile(t, r, "bad-bot", badBotActive)
	d := ruleDetail(t, r, "bad-bot")
	if !strings.HasPrefix(d.Invalid, "condition 2: ") {
		t.Errorf("invalid = %q, want condition 2", d.Invalid)
	}
	if want := []string{"keep", "close", "merge", "wait", "watch", "ignore"}; !slices.Equal(d.Matches.Dispositions, want) {
		t.Errorf("invalid pr rule: dispositions %v, want %v", d.Matches.Dispositions, want)
	}
}

// (a): every place serve names a condition counts from 1.
func TestServeNumbersConditionsFromOne(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	bad := map[string]any{"field": "bot", "op": "is", "value": "yes"}
	body := draftBody("n", "close", "", kindCond("pr"), bad)
	var out map[string]any
	if code := r.do(t, "POST", "/api/rules/preview", body, &out); code != 400 {
		t.Errorf("preview: %d, want 400", code)
	}
	if msg, _ := out["error"].(string); !strings.HasPrefix(msg, "condition 2: ") {
		t.Errorf("preview: %q, want condition 2", msg)
	}
	out = nil
	if code := r.do(t, "POST", "/api/rules/draft", body, &out); code != 400 {
		t.Errorf("draft: %d, want 400", code)
	}
	if msg, _ := out["error"].(string); !strings.HasPrefix(msg, "condition 2: ") {
		t.Errorf("draft: %q, want condition 2", msg)
	}
	// A rule file holding one is shown with the same number.
	writeRuleFile(t, r, "bad-bot", badBotActive)
	if d := ruleDetail(t, r, "bad-bot"); !strings.HasPrefix(d.Invalid, "condition 2: ") {
		t.Errorf("rule file: %q, want condition 2", d.Invalid)
	}
	body["id"] = "n-agent"
	out = nil
	r.do(t, "POST", "/api/agent/rule-draft", map[string]any{"session": "s1", "rule": body}, &out)
	if msg, _ := out["error"].(string); !strings.Contains(msg, "condition 2: ") {
		t.Errorf("agent draft: %q, want condition 2", msg)
	}
}
