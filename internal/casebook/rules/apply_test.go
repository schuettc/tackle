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

// applyTestNow is the fixed reference time used across apply tests.
var applyTestNow = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

// applyDB opens a fresh in-memory-style database and registers cleanup.
func applyDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "casebook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// applyStore returns a propose.Store whose clock is pinned to applyTestNow.
func applyStore(d *db.DB) *propose.Store {
	s := propose.New(d)
	s.Now = func() time.Time { return applyTestNow }
	return s
}

// activeRule returns a minimal active rule that matches all items of kind repo.
func activeRepoRule(id string) Rule {
	return Rule{
		ID:       id,
		Name:     "Test: " + id,
		Status:   StatusActive,
		EditedAt: applyTestNow.Add(-24 * time.Hour),
		Match: []Condition{
			{Field: "kind", Op: "is", Value: "repo"},
		},
		Propose: RuleAction{Disposition: "archive"},
	}
}

// repoItem builds a minimal undecided repo item for the result.
func repoItem(key string) engine.Item {
	return engine.Item{
		ID:   key,
		Kind: item.KindRepo,
		Repo: "schuettc/hail",
	}
}

// buildResult returns a Result with items sorted by ID (Find requires sorted items).
func buildResult(items ...engine.Item) engine.Result {
	sorted := make([]engine.Item, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	return engine.Result{Items: sorted}
}

// TestActiveRuleProposesMatchesOnRebuild checks that EvaluateActive (the
// "rebuild" step) creates pending proposals sourced "rule:<id>" for every item
// an active rule matches.
func TestActiveRuleProposesMatchesOnRebuild(t *testing.T) {
	ctx := context.Background()
	d := applyDB(t)
	s := applyStore(d)

	r := activeRepoRule("archive-repos")
	res := buildResult(
		repoItem("repo:schuettc/hail"),
		repoItem("repo:schuettc/old"),
	)

	created, err := EvaluateActive(ctx, []Rule{r}, res, applyTestNow, s)
	if err != nil {
		t.Fatalf("EvaluateActive: %v", err)
	}
	if created != 2 {
		t.Fatalf("created = %d, want 2", created)
	}

	pending, err := s.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	for _, key := range []string{"repo:schuettc/hail", "repo:schuettc/old"} {
		p, ok := pending[key]
		if !ok {
			t.Errorf("no pending proposal for %s", key)
			continue
		}
		if p.Source != "rule:archive-repos" {
			t.Errorf("proposal for %s source = %q, want rule:archive-repos", key, p.Source)
		}
		if p.Disposition != "archive" {
			t.Errorf("proposal for %s disposition = %q, want archive", key, p.Disposition)
		}
	}
}

// TestActiveRuleReProposesNewMatchAfterSync verifies that a second call to
// EvaluateActive (simulating a new HEAD after a sync) proposes newly matched
// items without re-proposing items that already have a pending proposal.
func TestActiveRuleReProposesNewMatchAfterSync(t *testing.T) {
	ctx := context.Background()
	d := applyDB(t)
	s := applyStore(d)

	r := activeRepoRule("archive-repos")

	// First rebuild: one item.
	res1 := buildResult(repoItem("repo:schuettc/hail"))
	created1, err := EvaluateActive(ctx, []Rule{r}, res1, applyTestNow, s)
	if err != nil || created1 != 1 {
		t.Fatalf("first EvaluateActive: created=%d err=%v", created1, err)
	}

	// Second rebuild (after sync): original item plus a newly appeared one.
	res2 := buildResult(
		repoItem("repo:schuettc/hail"),
		repoItem("repo:schuettc/old"),
	)
	created2, err := EvaluateActive(ctx, []Rule{r}, res2, applyTestNow, s)
	if err != nil {
		t.Fatalf("second EvaluateActive: %v", err)
	}
	if created2 != 1 {
		t.Fatalf("second created = %d, want 1 (only the new item)", created2)
	}

	// Both items must have a pending proposal.
	pending, _ := s.Pending(ctx)
	for _, key := range []string{"repo:schuettc/hail", "repo:schuettc/old"} {
		if _, ok := pending[key]; !ok {
			t.Errorf("no pending proposal for %s after two rebuilds", key)
		}
	}
}

// TestDraftRuleProposesNothingUntilProposeOnce confirms that a draft rule is
// ignored by EvaluateActive (no proposals), while ProposeOnce creates them.
func TestDraftRuleProposesNothingUntilProposeOnce(t *testing.T) {
	ctx := context.Background()
	d := applyDB(t)
	s := applyStore(d)

	draft := Rule{
		ID:       "draft-rule",
		Name:     "Draft rule",
		Status:   StatusDraft,
		EditedAt: applyTestNow.Add(-24 * time.Hour),
		Match: []Condition{
			{Field: "kind", Op: "is", Value: "repo"},
		},
		Propose: RuleAction{Disposition: "archive"},
	}
	res := buildResult(repoItem("repo:schuettc/hail"))

	// EvaluateActive ignores draft rules (caller pre-filters; passing draft
	// directly verifies the defensive skip inside EvaluateActive).
	created, err := EvaluateActive(ctx, []Rule{draft}, res, applyTestNow, s)
	if err != nil {
		t.Fatalf("EvaluateActive: %v", err)
	}
	if created != 0 {
		t.Errorf("EvaluateActive with draft rule: created = %d, want 0", created)
	}

	pending, _ := s.Pending(ctx)
	if len(pending) != 0 {
		t.Errorf("pending after EvaluateActive with draft: %d proposals, want 0", len(pending))
	}

	// ProposeOnce works on a draft rule.
	proposals, n, propErr := ProposeOnce(ctx, draft, res, applyTestNow, s)
	if propErr != nil {
		t.Fatalf("ProposeOnce: %v", propErr)
	}
	if n != 1 {
		t.Fatalf("ProposeOnce: created = %d, want 1", n)
	}
	if len(proposals) != 1 {
		t.Fatalf("ProposeOnce: returned %d proposals, want 1", len(proposals))
	}

	pending, _ = s.Pending(ctx)
	if _, ok := pending["repo:schuettc/hail"]; !ok {
		t.Error("no pending proposal after ProposeOnce")
	}
}

// TestRuleNeverReProposesAfterAccept verifies that once an item has a decision
// (the user accepted the proposal and the item is now decided in the result),
// EvaluateActive does not generate a new proposal for it.
func TestRuleNeverReProposesAfterAccept(t *testing.T) {
	ctx := context.Background()
	d := applyDB(t)
	s := applyStore(d)

	r := activeRepoRule("archive-repos")

	// First rebuild: item is undecided.
	res1 := buildResult(repoItem("repo:schuettc/hail"))
	if _, err := EvaluateActive(ctx, []Rule{r}, res1, applyTestNow, s); err != nil {
		t.Fatalf("first EvaluateActive: %v", err)
	}

	// Court accepts the proposal → the item now has a Decision in the result.
	dec := item.Decision{Disposition: item.Archive, DecidedBy: "court", DecidedAt: applyTestNow}
	decided := engine.Item{
		ID:       "repo:schuettc/hail",
		Kind:     item.KindRepo,
		Repo:     "schuettc/hail",
		Decision: &dec,
	}
	res2 := buildResult(decided)

	// Second rebuild: item is decided, so Proposable should skip it.
	created, err := EvaluateActive(ctx, []Rule{r}, res2, applyTestNow, s)
	if err != nil {
		t.Fatalf("second EvaluateActive: %v", err)
	}
	if created != 0 {
		t.Errorf("second EvaluateActive: created = %d, want 0 (item is decided)", created)
	}
}

// TestEvaluateActivePerItemNote verifies that the note is rendered per item,
// using each item's own fields (e.g. {repo} expands to that item's repo).
func TestEvaluateActivePerItemNote(t *testing.T) {
	ctx := context.Background()
	d := applyDB(t)
	s := applyStore(d)

	r := Rule{
		ID:       "repo-note",
		Name:     "Per-item note test",
		Status:   StatusActive,
		EditedAt: applyTestNow.Add(-24 * time.Hour),
		Match: []Condition{
			{Field: "kind", Op: "is", Value: "repo"},
		},
		Propose: RuleAction{Disposition: "archive", Note: "archiving {repo}"},
	}
	items := []engine.Item{
		{ID: "repo:schuettc/alpha", Kind: item.KindRepo, Repo: "schuettc/alpha"},
		{ID: "repo:schuettc/beta", Kind: item.KindRepo, Repo: "schuettc/beta"},
	}
	res := buildResult(items...)

	if _, err := EvaluateActive(ctx, []Rule{r}, res, applyTestNow, s); err != nil {
		t.Fatalf("EvaluateActive: %v", err)
	}

	pending, _ := s.Pending(ctx)
	wantNotes := map[string]string{
		"repo:schuettc/alpha": "archiving schuettc/alpha",
		"repo:schuettc/beta":  "archiving schuettc/beta",
	}
	for key, want := range wantNotes {
		p, ok := pending[key]
		if !ok {
			t.Errorf("no pending proposal for %s", key)
			continue
		}
		if p.Note != want {
			t.Errorf("proposal for %s note = %q, want %q", key, p.Note, want)
		}
	}
}

// TestEvaluateActiveReturnsErrorOnProposeFailure verifies that when
// props.Propose returns errors (e.g. invalid disposition that passes rule
// status check but fails Propose's own item.Decision.Validate), EvaluateActive
// surfaces those errors rather than silently discarding them.
// Before fix: EvaluateActive discards the []error from Propose ("ps, _") →
// returns (0, nil); test FAILS. After fix: returns a non-nil error → PASS.
func TestEvaluateActiveReturnsErrorOnProposeFailure(t *testing.T) {
	ctx := context.Background()
	d := applyDB(t)
	s := applyStore(d)

	// Build a rule whose disposition is intentionally invalid. We do NOT call
	// r.Validate() here (EvaluateActive doesn't validate either), so the rule
	// passes the StatusActive check and reaches props.Propose, which then
	// fails its own item.Decision.Validate → returns []error.
	r := Rule{
		ID:       "bad-disp-rule",
		Name:     "Bad Disposition",
		Status:   StatusActive,
		EditedAt: applyTestNow.Add(-24 * time.Hour),
		Match:    []Condition{{Field: "kind", Op: "is", Value: "repo"}},
		Propose:  RuleAction{Disposition: "not-a-real-disposition"},
	}
	res := buildResult(repoItem("repo:schuettc/hail"))

	_, err := EvaluateActive(ctx, []Rule{r}, res, applyTestNow, s)
	if err == nil {
		t.Fatal("EvaluateActive: expected non-nil error when Propose fails (invalid disposition), got nil — Propose errors are being silently discarded")
	}
}

// TestProposeOnceReturnsErrorOnProposeFailure is the ProposeOnce equivalent:
// when the disposition is invalid, ProposeOnce must return the Propose error.
func TestProposeOnceReturnsErrorOnProposeFailure(t *testing.T) {
	ctx := context.Background()
	d := applyDB(t)
	s := applyStore(d)

	r := Rule{
		ID:       "bad-disp-once",
		Name:     "Bad Disposition Once",
		Status:   StatusDraft,
		EditedAt: applyTestNow.Add(-24 * time.Hour),
		Match:    []Condition{{Field: "kind", Op: "is", Value: "repo"}},
		Propose:  RuleAction{Disposition: "not-a-real-disposition"},
	}
	res := buildResult(repoItem("repo:schuettc/hail"))

	_, _, err := ProposeOnce(ctx, r, res, applyTestNow, s)
	if err == nil {
		t.Fatal("ProposeOnce: expected non-nil error when Propose fails (invalid disposition), got nil — Propose errors are being silently discarded")
	}
}
