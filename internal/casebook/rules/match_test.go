package rules

import (
	"context"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/db"
	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/propose"
)

// --- helpers ---

var testNow = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

// branchFields returns a Fields for a branch item with the given landed info.
// landedVia is the machine-readable code (e.g. "default-branch", "merged-pr").
func branchFields(landed string, landedVia ...string) engine.Fields {
	return engine.Fields{
		Kind:      "branch",
		Repo:      "schuettc/galley",
		Owner:     "schuettc",
		Landed:    landed,
		LandedHow: landedVia,
		Worktree:  "none",
	}
}

// repoFields returns a Fields for a repo item.
func repoFields(archived bool, openPRs, openIssues int) engine.Fields {
	return engine.Fields{
		Kind:       "repo",
		Repo:       "schuettc/galley",
		Owner:      "schuettc",
		Archived:   archived,
		OpenPRs:    openPRs,
		OpenIssues: openIssues,
	}
}

// prFields returns a Fields for a PR item.
func prFields(direction, title string, age time.Duration, labels []string) engine.Fields {
	return engine.Fields{
		Kind:      "pr",
		Repo:      "schuettc/galley",
		Owner:     "schuettc",
		Direction: direction,
		Relation:  direction,
		Title:     title,
		Age:       age,
		Labels:    labels,
	}
}

// --- operator tests ---

func TestEveryOperatorOnEveryFieldType(t *testing.T) {
	// is / is-not on enum field (kind)
	f := engine.Fields{Kind: "branch"}
	checkEval(t, "is matches", Condition{Field: "kind", Op: "is", Value: "branch"}, f, true)
	checkEval(t, "is no-match", Condition{Field: "kind", Op: "is", Value: "repo"}, f, false)
	checkEval(t, "is-not matches", Condition{Field: "kind", Op: "is-not", Value: "repo"}, f, true)
	checkEval(t, "is-not no-match", Condition{Field: "kind", Op: "is-not", Value: "branch"}, f, false)

	// in / not-in on enum field
	checkEval(t, "in", Condition{Field: "kind", Op: "in", Value: "branch, repo"}, f, true)
	checkEval(t, "in miss", Condition{Field: "kind", Op: "in", Value: "repo, worktree"}, f, false)
	checkEval(t, "not-in", Condition{Field: "kind", Op: "not-in", Value: "repo, worktree"}, f, true)
	checkEval(t, "not-in miss", Condition{Field: "kind", Op: "not-in", Value: "branch, repo"}, f, false)

	// matches on title
	ft := engine.Fields{Kind: "pr", Title: "feat/new-router"}
	checkEval(t, "matches hit", Condition{Field: "title", Op: "matches", Value: "feat/.*"}, ft, true)
	checkEval(t, "matches miss", Condition{Field: "title", Op: "matches", Value: "fix/.*"}, ft, false)

	// older-than / newer-than on duration field
	age7d := 7 * 24 * time.Hour
	fd := engine.Fields{Kind: "pr", Age: age7d}
	checkEval(t, "older-than hit", Condition{Field: "age", Op: "older-than", Value: "6d"}, fd, true)
	checkEval(t, "older-than miss", Condition{Field: "age", Op: "older-than", Value: "8d"}, fd, false)
	checkEval(t, "newer-than hit", Condition{Field: "age", Op: "newer-than", Value: "8d"}, fd, true)
	checkEval(t, "newer-than miss", Condition{Field: "age", Op: "newer-than", Value: "6d"}, fd, false)

	// is / is-not on bool field
	fb := engine.Fields{Kind: "repo", Archived: true}
	checkEval(t, "bool is true", Condition{Field: "archived", Op: "is", Value: "true"}, fb, true)
	checkEval(t, "bool is false", Condition{Field: "archived", Op: "is", Value: "false"}, fb, false)
	checkEval(t, "bool is-not true", Condition{Field: "archived", Op: "is-not", Value: "true"}, fb, false)
	checkEval(t, "bool is-not false", Condition{Field: "archived", Op: "is-not", Value: "false"}, fb, true)

	// gt / gte / lt / lte on count field
	fc := engine.Fields{Kind: "repo", OpenPRs: 5, OpenIssues: 3}
	checkEval(t, "gt hit", Condition{Field: "open-prs", Op: "gt", Value: "4"}, fc, true)
	checkEval(t, "gt miss", Condition{Field: "open-prs", Op: "gt", Value: "5"}, fc, false)
	checkEval(t, "gte hit eq", Condition{Field: "open-prs", Op: "gte", Value: "5"}, fc, true)
	checkEval(t, "gte hit above", Condition{Field: "open-prs", Op: "gte", Value: "4"}, fc, true)
	checkEval(t, "gte miss", Condition{Field: "open-prs", Op: "gte", Value: "6"}, fc, false)
	checkEval(t, "lt hit", Condition{Field: "open-issues", Op: "lt", Value: "4"}, fc, true)
	checkEval(t, "lt miss", Condition{Field: "open-issues", Op: "lt", Value: "3"}, fc, false)
	checkEval(t, "lte hit eq", Condition{Field: "open-issues", Op: "lte", Value: "3"}, fc, true)
	checkEval(t, "lte miss", Condition{Field: "open-issues", Op: "lte", Value: "2"}, fc, false)

	// is / is-not on count
	checkEval(t, "count is hit", Condition{Field: "open-prs", Op: "is", Value: "5"}, fc, true)
	checkEval(t, "count is miss", Condition{Field: "open-prs", Op: "is", Value: "4"}, fc, false)
	checkEval(t, "count is-not hit", Condition{Field: "open-prs", Op: "is-not", Value: "4"}, fc, true)
	checkEval(t, "count is-not miss", Condition{Field: "open-prs", Op: "is-not", Value: "5"}, fc, false)

	// label multi-value
	fl := engine.Fields{Kind: "pr", Labels: []string{"bug", "help wanted"}}
	checkEval(t, "label is hit", Condition{Field: "label", Op: "is", Value: "bug"}, fl, true)
	checkEval(t, "label is miss", Condition{Field: "label", Op: "is", Value: "enhancement"}, fl, false)
	checkEval(t, "label is-not hit", Condition{Field: "label", Op: "is-not", Value: "enhancement"}, fl, true)
	checkEval(t, "label is-not miss", Condition{Field: "label", Op: "is-not", Value: "bug"}, fl, false)
	checkEval(t, "label in hit", Condition{Field: "label", Op: "in", Value: "bug, enhancement"}, fl, true)
	checkEval(t, "label in miss", Condition{Field: "label", Op: "in", Value: "enhancement, question"}, fl, false)
	checkEval(t, "label not-in hit", Condition{Field: "label", Op: "not-in", Value: "enhancement, question"}, fl, true)
	checkEval(t, "label not-in miss", Condition{Field: "label", Op: "not-in", Value: "bug, enhancement"}, fl, false)

	// policy-hit multi-value
	fp := engine.Fields{Kind: "repo", PolicyHits: []string{"dormant"}}
	checkEval(t, "policy-hit is hit", Condition{Field: "policy-hit", Op: "is", Value: "dormant"}, fp, true)
	checkEval(t, "policy-hit is miss", Condition{Field: "policy-hit", Op: "is", Value: "unpushed"}, fp, false)
}

func checkEval(t *testing.T, name string, c Condition, f engine.Fields, want bool) {
	t.Helper()
	got, err := c.Eval(f)
	if err != nil {
		t.Errorf("%s: Eval error: %v", name, err)
		return
	}
	if got != want {
		t.Errorf("%s: got %v, want %v", name, got, want)
	}
}

// --- MatchAll tests ---

func TestLandedBranchesRuleMatchesByReason(t *testing.T) {
	// landedBranchRule matches kind=branch, landed=all-machines, worktree is-not dirty
	r := Rule{
		ID:     "landed-branches",
		Status: StatusActive,
		Match: []Condition{
			{Field: "kind", Op: "is", Value: "branch"},
			{Field: "landed", Op: "is", Value: "all-machines"},
			{Field: "worktree", Op: "is-not", Value: "dirty"},
		},
		Propose: Action{Disposition: "delete"},
	}

	// Two branch items with different how types (codes in LandedVia).
	inMainItem := engine.Item{
		ID:        "branch:schuettc/galley@feat/foo",
		Kind:      item.KindBranch,
		Repo:      "schuettc/galley",
		Status:    item.StatusNew,
		Landed:    "all-machines",
		LandedHow: "in main", // human text
		LandedVia: []string{"default-branch"},
	}
	viaPRItem := engine.Item{
		ID:        "branch:schuettc/galley@feat/bar",
		Kind:      item.KindBranch,
		Repo:      "schuettc/galley",
		Status:    item.StatusNew,
		Landed:    "all-machines",
		LandedHow: "via merged PR", // human text
		LandedVia: []string{"merged-pr"},
	}
	result := engine.Result{Items: []engine.Item{inMainItem, viaPRItem}}

	matches, err := r.MatchAll(result, testNow)
	if err != nil {
		t.Fatalf("MatchAll: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("got %d matches, want 2", len(matches))
	}

	// Check that reasons reflect how
	reasons := map[string]string{}
	for _, m := range matches {
		reasons[m.Key] = m.Reason
	}
	if r := reasons["branch:schuettc/galley@feat/foo"]; r != "in main" {
		t.Errorf("foo reason = %q, want %q", r, "in main")
	}
	if r := reasons["branch:schuettc/galley@feat/bar"]; r != "via merged PR" {
		t.Errorf("bar reason = %q, want %q", r, "via merged PR")
	}
}

func TestExclusionRemovesAMatch(t *testing.T) {
	r := Rule{
		ID:     "landed-branches",
		Status: StatusActive,
		Match: []Condition{
			{Field: "kind", Op: "is", Value: "branch"},
		},
		Propose: Action{Disposition: "delete"},
		Exclude: []Exclusion{
			{Key: "branch:schuettc/galley@feat/keep", By: "court", At: testNow},
		},
	}

	items := []engine.Item{
		{ID: "branch:schuettc/galley@feat/keep", Kind: item.KindBranch, Repo: "schuettc/galley"},
		{ID: "branch:schuettc/galley@feat/delete", Kind: item.KindBranch, Repo: "schuettc/galley"},
	}
	result := engine.Result{Items: items}

	matches, err := r.MatchAll(result, testNow)
	if err != nil {
		t.Fatalf("MatchAll: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want 1", len(matches))
	}
	if matches[0].Key != "branch:schuettc/galley@feat/delete" {
		t.Errorf("got key %q, want delete branch", matches[0].Key)
	}
}

func TestRuleSkipsDecidedPendingAndRejected(t *testing.T) {
	editedAt := testNow.Add(-24 * time.Hour) // rule edited 1d ago

	r := Rule{
		ID:       "dormant-repos",
		Status:   StatusActive,
		EditedAt: editedAt,
		Match: []Condition{
			{Field: "kind", Op: "is", Value: "repo"},
		},
		Propose: Action{Disposition: "archive"},
	}

	dec := item.Decision{Disposition: item.Archive, DecidedBy: "court", DecidedAt: testNow}
	items := []engine.Item{
		// decided → excluded
		{ID: "repo:schuettc/decided", Kind: item.KindRepo, Repo: "schuettc/decided",
			Decision: &dec},
		// pending from this rule → excluded
		{ID: "repo:schuettc/pending", Kind: item.KindRepo, Repo: "schuettc/pending"},
		// rejected since edit → excluded
		{ID: "repo:schuettc/rejected-new", Kind: item.KindRepo, Repo: "schuettc/rejected-new"},
		// rejected BEFORE edit → should be included (the rejection predates the edit)
		{ID: "repo:schuettc/rejected-old", Kind: item.KindRepo, Repo: "schuettc/rejected-old"},
		// plain undecided → included
		{ID: "repo:schuettc/open", Kind: item.KindRepo, Repo: "schuettc/open"},
	}
	// engine.Result.Find requires items sorted by ID.
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	result := engine.Result{Items: items}

	// Build pending map: "pending" has a pending proposal from this rule.
	pending := map[string]propose.Proposal{
		"repo:schuettc/pending": {
			Key:    "repo:schuettc/pending",
			Source: "rule:dormant-repos",
			State:  propose.Pending,
		},
	}

	// rejected: "rejected-new" was rejected AFTER the rule edit (blocks re-propose).
	// "rejected-old" was rejected BEFORE the rule edit (does NOT block re-propose).
	rejected := map[string]bool{
		"repo:schuettc/rejected-new": true,
		// "rejected-old" not in this map because its settled_at < editedAt
	}

	proposable := r.Proposable(result, testNow, pending, rejected)

	// Only "open" and "rejected-old" should be proposable.
	got := map[string]bool{}
	for _, m := range proposable {
		got[m.Key] = true
	}
	if len(got) != 2 {
		t.Fatalf("got %d proposable, want 2: %v", len(got), got)
	}
	if !got["repo:schuettc/open"] {
		t.Errorf("expected open to be proposable")
	}
	if !got["repo:schuettc/rejected-old"] {
		t.Errorf("expected rejected-old to be proposable (rejection predates rule edit)")
	}
}

func TestNeverOverridesADecision(t *testing.T) {
	r := Rule{
		ID:     "all-repos",
		Status: StatusActive,
		Match: []Condition{
			{Field: "kind", Op: "is", Value: "repo"},
		},
		Propose: Action{Disposition: "archive"},
	}

	dec := item.Decision{Disposition: item.Archive, DecidedBy: "court", DecidedAt: testNow}
	result := engine.Result{Items: []engine.Item{
		{ID: "repo:schuettc/decided", Kind: item.KindRepo, Decision: &dec},
	}}

	proposable := r.Proposable(result, testNow, map[string]propose.Proposal{}, map[string]bool{})
	if len(proposable) != 0 {
		t.Errorf("Proposable returned %d items for decided item, want 0", len(proposable))
	}
}

// TestRuleRecordCounts verifies that RuleRecord counts accepted/rejected/pending
// proposals from a real propose.Store: accepted+changed → Accepted, rejected →
// Rejected, pending → Pending; proposals from other sources are ignored.
func TestRuleRecordCounts(t *testing.T) {
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "casebook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	s := propose.New(d)
	s.Now = func() time.Time { return now }
	// Need a session row for the source to be valid (proposals don't require sessions,
	// but the db schema needs to be set up; Open already runs migrations).

	ctx := context.Background()
	source := "rule:my-rule"
	other := "pi:s1"

	// Insert proposals via direct SQL (Propose validates item keys; we bypass
	// that by inserting rows directly to test RuleRecord in isolation).
	for _, row := range []struct{ source, state string }{
		{source, "accepted"},
		{source, "changed"},
		{source, "rejected"},
		{source, "pending"},
		{other, "accepted"}, // different source → must not count
	} {
		_, err := d.ExecContext(ctx, "INSERT INTO proposals(key, disposition, source, state, created_at) VALUES (?, 'delete', ?, ?, ?)",
			"branch:a/b@feat", row.source, row.state, now.UnixMilli())
		if err != nil {
			t.Fatalf("insert %v: %v", row, err)
		}
	}

	rec, err := RuleRecord(ctx, s, "my-rule")
	if err != nil {
		t.Fatalf("RuleRecord: %v", err)
	}
	if rec.Accepted != 2 {
		t.Errorf("Accepted = %d, want 2 (accepted+changed)", rec.Accepted)
	}
	if rec.Rejected != 1 {
		t.Errorf("Rejected = %d, want 1", rec.Rejected)
	}
	if rec.Pending != 1 {
		t.Errorf("Pending = %d, want 1", rec.Pending)
	}
}

// TestCompiledRegexUsesCorrectField verifies that the compiled "matches" op is
// evaluated against the field it names, NOT hard-coded against Title.
// Fix D: compiledRule must remember its field; evalAll uses it.
func TestCompiledRegexUsesCorrectField(t *testing.T) {
	// Rule matches on "repo" field, not "title".
	r := Rule{
		ID:     "repo-match",
		Status: StatusActive,
		Match: []Condition{
			{Field: "repo", Op: "matches", Value: `schuettc/.*`},
		},
		Propose: Action{Disposition: "archive"},
	}

	// Item with matching Repo but non-matching Title.
	it := engine.Item{
		ID:    "repo:schuettc/galley",
		Kind:  item.KindRepo,
		Repo:  "schuettc/galley",
		Title: "some-unrelated-title", // does NOT match schuettc/.*
	}
	result := engine.Result{Items: []engine.Item{it}}

	matches, err := r.MatchAll(result, testNow)
	if err != nil {
		t.Fatalf("MatchAll: %v", err)
	}
	// Should match because Repo matches, not Title.
	if len(matches) != 1 {
		t.Errorf("got %d matches, want 1 (regex must match Repo, not Title)", len(matches))
	}
}

// TestDeterministicTip verifies that Fields.Tip is always the tip of the
// lexically first machine in LandedTips, not an arbitrary map iteration.
// Fix E: fields.go must sort LandedTips keys before picking the tip.
func TestDeterministicTip(t *testing.T) {
	it := engine.Item{
		ID:   "branch:schuettc/hail@feat",
		Kind: item.KindBranch,
		Repo: "schuettc/hail",
		// Two machines: "mbp" > "air" lexically, so "air" is first.
		LandedTips: map[string]string{
			"mbp": "tip-mbp",
			"air": "tip-air",
		},
	}
	f := it.Fields(testNow)
	if f.Tip != "tip-air" {
		t.Errorf("Tip = %q, want %q (lexically first machine is \"air\")", f.Tip, "tip-air")
	}
}
