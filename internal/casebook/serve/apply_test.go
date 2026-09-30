package serve

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/rules"
)

// activeRepoRule returns a minimal active rule that matches all repo items.
func activeRepoRule(id string, now time.Time) rules.Rule {
	return rules.Rule{
		ID:        id,
		Name:      "Test: " + id,
		Status:    rules.StatusActive,
		CreatedBy: "court",
		CreatedAt: now,
		EditedAt:  now.Add(-time.Hour),
		Match: []rules.Condition{
			{Field: "kind", Op: "is", Value: "repo"},
		},
		Propose: rules.RuleAction{Disposition: "archive"},
	}
}

// TestRebuildRunsActiveRulesAndProposesMatches verifies that serve.rebuild
// calls EvaluateActive: an active rule written to the repo produces pending
// proposals after rebuild runs.
func TestRebuildRunsActiveRulesAndProposesMatches(t *testing.T) {
	r := newRig(t)

	ru := activeRepoRule("archive-repos", r.Now)
	if err := r.App.Repo.WriteRule(ctx, ru, "rule archive-repos active by court"); err != nil {
		t.Fatalf("WriteRule: %v", err)
	}

	// rebuild was already called by New; call it again now that the rule exists.
	if err := r.s.rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	// The apptest rig always has repo:schuettc/hail.
	pending, err := r.s.Props.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if _, ok := pending["repo:schuettc/hail"]; !ok {
		t.Error("expected pending proposal for repo:schuettc/hail after rebuild with active rule")
	}
	if p := pending["repo:schuettc/hail"]; p.Source != "rule:archive-repos" {
		t.Errorf("proposal source = %q, want rule:archive-repos", p.Source)
	}
}

// TestRebuildWithActiveRuleDoesNotLoop verifies that a rebuild that creates
// proposals does not trigger another rebuild. Proposals are written to SQLite
// only; they never move the casebook-data HEAD that the watch loop monitors.
// We count rebuilds: New triggers one, our explicit call triggers one more —
// total two. If proposing caused a loop, the count would exceed two.
func TestRebuildWithActiveRuleDoesNotLoop(t *testing.T) {
	r := newRig(t)

	ru := activeRepoRule("no-loop-rule", r.Now)
	if err := r.App.Repo.WriteRule(ctx, ru, "rule no-loop-rule active"); err != nil {
		t.Fatalf("WriteRule: %v", err)
	}

	countBefore := r.s.rebuilds.Load()
	if err := r.s.rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	countAfter := r.s.rebuilds.Load()

	// Exactly one rebuild was triggered by our explicit call.
	if got := countAfter - countBefore; got != 1 {
		t.Errorf("rebuild count delta = %d, want 1 (no loop from proposals)", got)
	}
}

// TestRebuildSkipsInvalidRuleAndKeepsPending verifies that a rule that fails
// validation is skipped and its error recorded in index notices, while valid
// active rules continue to produce proposals.
func TestRebuildSkipsInvalidRuleAndKeepsPending(t *testing.T) {
	ar := newRig(t)

	// Write a valid active rule.
	valid := activeRepoRule("valid-rule", ar.Now)
	if err := ar.App.Repo.WriteRule(ctx, valid, "rule valid-rule active"); err != nil {
		t.Fatalf("WriteRule valid: %v", err)
	}

	// Inject an invalid rule file (unknown disposition) using low-level
	// WriteFile + Commit, bypassing WriteRule which calls Validate.
	invalidTOML := []byte("id = \"bad-rule\"\n" +
		"name = \"Bad rule\"\n" +
		"status = \"active\"\n" +
		"created_by = \"court\"\n" +
		"created_at = 2026-09-27T12:00:00Z\n" +
		"edited_at = 2026-09-27T12:00:00Z\n" +
		"[[match]]\n" +
		"field = \"kind\"\n" +
		"op = \"is\"\n" +
		"value = \"repo\"\n" +
		"[propose]\n" +
		"disposition = \"not-a-real-disposition\"\n")
	if _, err := ar.App.Repo.WriteFile("rules/bad-rule.toml", invalidTOML); err != nil {
		t.Fatalf("WriteFile bad rule: %v", err)
	}
	if _, err := ar.App.Repo.Commit(ctx, "add bad-rule (test)"); err != nil {
		t.Fatalf("Commit bad rule: %v", err)
	}

	if err := ar.s.rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	// valid-rule must have produced proposals.
	pending, _ := ar.s.Props.Pending(ctx)
	if _, ok := pending["repo:schuettc/hail"]; !ok {
		t.Error("expected proposal from valid-rule after rebuild")
	}

	// Index notices must mention the bad rule.
	notices := ar.s.Index.Notices()
	found := false
	for _, n := range notices {
		if strings.Contains(n, "bad-rule") || strings.HasPrefix(n, "rule") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected rule error notice; got %v", notices)
	}
}

// TestRebuildSerializedNoRace verifies that concurrent calls to rebuild are
// serialized by a mutex: when N goroutines call rebuild at once with an active
// rule that matches 1 item, the result is exactly 1 pending proposal (not N),
// and -race reports no data race.
//
// Before fix (no mutex): rebuilds run in parallel; each call evaluates rules
// and calls Propose, which may create duplicate entries or race on shared
// Server state. The -race detector flags the race on rebuilds/Index/etc.
// After fix: exactly 1 proposal, no race.
func TestRebuildSerializedNoRace(t *testing.T) {
	ar := newRig(t)

	// Write an active rule that matches the rig's single repo item.
	ru := activeRepoRule("serialize-rule", ar.Now)
	if err := ar.App.Repo.WriteRule(ctx, ru, "rule serialize-rule active"); err != nil {
		t.Fatalf("WriteRule: %v", err)
	}

	// First rebuild to confirm baseline.
	if err := ar.s.rebuild(ctx); err != nil {
		t.Fatalf("baseline rebuild: %v", err)
	}

	// Now run rebuild from several goroutines simultaneously. Without the
	// mutex, they race on shared state (Index fields, rebuilds counter, etc.).
	// -race will flag this before fix.
	const N = 8
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ar.s.rebuild(ctx)
		}()
	}
	wg.Wait()

	// The rig has exactly one repo item. The rule should have proposed it once.
	// With serialization, every rebuild after the first sees the existing
	// pending proposal (Proposable skips already-pending items) and creates 0
	// new ones. Total pending should be exactly 1.
	pending, err := ar.s.Props.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if got := len(pending); got != 1 {
		t.Errorf("pending proposals = %d, want 1 (got duplicates from concurrent rebuilds)", got)
	}
}

// TestRebuildNoticesAtomic verifies that rule-error notices are published in
// the same index update as the rebuilt result, not appended under a second
// lock. A concurrent reader must never observe a new Head without its notices.
//
// Strategy: run a goroutine that spins reading Head + Notices while rebuild
// runs, and asserts that once the head has changed, the notice is immediately
// visible. Before fix (appendNotices under a second lock), the reader can
// observe a window where head is updated but notices are empty.
func TestRebuildNoticesAtomic(t *testing.T) {
	ar := newRig(t)

	// Write an invalid rule that produces a notice during rebuild.
	invalidTOML := []byte("id = \"atomic-bad\"\n" +
		"name = \"Atomic bad\"\n" +
		"status = \"active\"\n" +
		"created_by = \"court\"\n" +
		"created_at = 2026-09-27T12:00:00Z\n" +
		"edited_at = 2026-09-27T12:00:00Z\n" +
		"[[match]]\n" +
		"field = \"kind\"\n" +
		"op = \"is\"\n" +
		"value = \"repo\"\n" +
		"[propose]\n" +
		"disposition = \"not-a-real-disposition\"\n")
	if _, err := ar.App.Repo.WriteFile("rules/atomic-bad.toml", invalidTOML); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := ar.App.Repo.Commit(ctx, "add atomic-bad rule (test)"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	origHead := ar.s.Index.Head()

	// Spin reading index state. If we ever see a new head without the notice,
	// that's the race window exposed by the two-lock approach.
	var sawWindowRace atomic.Bool
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			head := ar.s.Index.Head()
			notices := ar.s.Index.Notices()
			if head != origHead {
				// New head visible: notices must be present immediately.
				hasNotice := false
				for _, n := range notices {
					if strings.Contains(n, "atomic-bad") || strings.Contains(n, "not-a-real-disposition") {
						hasNotice = true
						break
					}
				}
				if !hasNotice {
					sawWindowRace.Store(true)
				}
				return
			}
		}
	}()

	if err := ar.s.rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	<-readDone

	if sawWindowRace.Load() {
		t.Error("reader saw new head without rule notices — notices are not updated atomically with the result")
	}

	// Regardless of the race window, after rebuild the notice must be present.
	notices := ar.s.Index.Notices()
	hasNotice := false
	for _, n := range notices {
		if strings.Contains(n, "atomic-bad") || strings.Contains(n, "not-a-real-disposition") || strings.HasPrefix(n, "rule") {
			hasNotice = true
			break
		}
	}
	if !hasNotice {
		t.Errorf("expected rule error notice after rebuild; got %v", notices)
	}
}
