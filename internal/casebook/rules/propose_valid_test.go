package rules

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/item"
)

// rule builds a draft with conditions and a proposal, for Validate.
func rule(disp, until string, match ...Condition) Rule {
	return Rule{ID: "r", Name: "r", Status: StatusDraft, Match: match,
		Propose: RuleAction{Disposition: disp, Until: until}}
}

func kindIs(k string) Condition { return Condition{Field: "kind", Op: "is", Value: k} }

// N1: an until serve can't parse makes the rule invalid (every proposal it
// made would be refused, so it would silently propose nothing).
func TestValidateParsesUntil(t *testing.T) {
	err := rule("wait", "30d", kindIs("repo")).Validate()
	if err == nil || !strings.Contains(err.Error(), `invalid until "30d"`) {
		t.Errorf("wait until 30d: err = %v, want serve's until message", err)
	}
	if err := rule("wait", "inactive(30d)", kindIs("repo")).Validate(); err != nil {
		t.Errorf("wait until inactive(30d): %v", err)
	}
	// An until on a disposition that doesn't need one is still parsed, as
	// every proposal's decision parses it.
	if err := rule("keep", "soon", kindIs("repo")).Validate(); err == nil {
		t.Error("keep until soon: want an error")
	}
}

// N1: the disposition is checked against the kinds the rule can match.
func TestValidateDispositionAgainstTheRulesKinds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		r     Rule
		valid bool
	}{
		{"archive on a branch", rule("archive", "", kindIs("branch")), false},
		{"close on a branch", rule("close", "", kindIs("branch")), false},
		{"delete on a branch", rule("delete", "", kindIs("branch")), true},
		{"archive on a repo", rule("archive", "", kindIs("repo")), true},
		{"close on pr or issue", rule("close", "", Condition{Field: "kind", Op: "in", Value: "pr, issue"}), true},
		{"merge on pr or issue", rule("merge", "", Condition{Field: "kind", Op: "in", Value: "pr,issue"}), false},
		{"merge on a pr", rule("merge", "", kindIs("pr")), true},
		{"watch on not-a-worktree", rule("watch", "inactive(9d)", Condition{Field: "kind", Op: "is-not", Value: "worktree"}), true},
		{"watch with no kind condition", rule("watch", "inactive(9d)"), false},
		{"archive with no kind condition", rule("archive", "", Condition{Field: "archived", Op: "is", Value: "false"}), false},
		{"keep with no kind condition", rule("keep", ""), true},
		{"ignore with no kind condition", rule("ignore", ""), true},
	} {
		err := tc.r.Validate()
		if (err == nil) != tc.valid {
			t.Errorf("%s: err = %v, want valid=%v", tc.name, err, tc.valid)
		}
	}
	err := rule("archive", "", kindIs("branch")).Validate()
	if err == nil || !strings.Contains(err.Error(), "branch") || !strings.Contains(err.Error(), "keep, delete, wait, watch, ignore") {
		t.Errorf("archive on a branch: %v, want the kind and what it allows", err)
	}
	if err := rule("delete", "", kindIs("branch"), kindIs("repo")).Validate(); err == nil ||
		!strings.Contains(err.Error(), "no kind") {
		t.Errorf("kind is branch and kind is repo: %v, want 'no kind'", err)
	}
}

func TestDispositionsFollowTheKindConditions(t *testing.T) {
	for _, tc := range []struct {
		match []Condition
		want  []string
	}{
		{[]Condition{kindIs("branch")}, []string{"keep", "delete", "wait", "watch", "ignore"}},
		{[]Condition{kindIs("pr")}, []string{"keep", "close", "merge", "wait", "watch", "ignore"}},
		{[]Condition{{Field: "kind", Op: "in", Value: "pr,issue"}}, []string{"keep", "close", "wait", "watch", "ignore"}},
		{nil, []string{"keep", "wait", "ignore"}},
		{[]Condition{kindIs("pr"), kindIs("repo")}, []string{}},
	} {
		if got := Dispositions(tc.match); !slices.Equal(got, tc.want) {
			t.Errorf("Dispositions(%v) = %v, want %v", tc.match, got, tc.want)
		}
	}
}

// N1: a rule that validates never has a proposal refused for its proposal
// fields: for every kind it can match, the decision it proposes is valid.
func TestAValidRuleNeverProposesARefusedDecision(t *testing.T) {
	kindConds := [][]Condition{nil}
	for _, k := range []string{"repo", "pr", "issue", "branch", "worktree"} {
		kindConds = append(kindConds,
			[]Condition{kindIs(k)},
			[]Condition{{Field: "kind", Op: "is-not", Value: k}},
			[]Condition{{Field: "kind", Op: "in", Value: k + ",pr"}},
			[]Condition{{Field: "kind", Op: "not-in", Value: k + ",issue"}})
	}
	untils := []string{"", "30d", "inactive(30d)", "date(2026-12-01)", "merged(pr:o/r#1)", "merged(issue:o/r#1)", "released(repo:o/r)"}
	disps := []string{"keep", "archive", "close", "delete", "merge", "wait", "watch", "ignore", "nuke"}
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	checked := 0
	for _, m := range kindConds {
		for _, d := range disps {
			for _, u := range untils {
				r := rule(d, u, m...)
				if r.Validate() != nil {
					continue
				}
				for _, k := range Kinds(m) {
					dec := item.Decision{Disposition: item.Disposition(d), Until: u, DecidedBy: "rule:r", DecidedAt: now}
					if err := dec.Validate(k); err != nil {
						t.Errorf("rule %v %s until %q is valid, but a %s's proposal is refused: %v", m, d, u, k, err)
					}
					checked++
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no valid rule was checked")
	}
}

// (a): conditions are numbered from 1, as Court and the agent count them.
func TestConditionsAreNumberedFromOne(t *testing.T) {
	bad := Condition{Field: "bot", Op: "is", Value: "yes"}
	err := rule("keep", "", kindIs("pr"), bad).Validate()
	if err == nil || !strings.HasPrefix(err.Error(), "condition 2: ") {
		t.Errorf("Validate: %v, want condition 2", err)
	}
	if err := ValidateConditions([]Condition{bad}); err == nil || !strings.HasPrefix(err.Error(), "condition 1: ") {
		t.Errorf("ValidateConditions: %v, want condition 1", err)
	}
	if _, err := compileRule(rule("keep", "", kindIs("pr"), Condition{Field: "title", Op: "matches", Value: "("})); err == nil ||
		!strings.HasPrefix(err.Error(), "condition 2: ") {
		t.Errorf("compileRule: %v, want condition 2", err)
	}
}
