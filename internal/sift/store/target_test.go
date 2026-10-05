package store

import (
	"errors"
	"testing"

	"github.com/schuettc/tackle/internal/sift/row"
)

// prints is each row's fingerprint as the page shows it.
func prints(t *testing.T, s *Store, id int64) map[string]string {
	t.Helper()
	_, rows, err := s.Round(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, r := range rows {
		m[r.ID] = r.Fingerprint
	}
	return m
}

// decided is how many of the round's rows hold a decision.
func decided(t *testing.T, s *Store, id int64) int {
	t.Helper()
	_, rows, err := s.Round(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range rows {
		if r.Decision != nil {
			n++
		}
	}
	return n
}

// An edit to merge:C binds to C as the page showed it: C changed since, or
// a target the round lacks, or no target fingerprint, is refused with
// ErrChanged and nothing is stored.
func TestAnEditsMergeTargetIsFingerprinted(t *testing.T) {
	s, _ := open(t)
	id, rows := round(t, s)
	a, c := rows[0].ID, rows[1].ID
	old := prints(t, s, id)
	if _, err := s.AddRows(ctx, id, []row.Row{{ID: c, Verdict: "delete"}}); err != nil {
		t.Fatal(err)
	}
	now := prints(t, s, id)
	edit := row.Decision{Action: "edit", Verdict: "merge:" + c, Text: "both"}
	for name, ans := range map[string]Answer{
		"target changed":  {Row: a, Fingerprint: now[a], TargetFingerprint: old[c], Decision: edit},
		"no target print": {Row: a, Fingerprint: now[a], Decision: edit},
		"unknown target": {Row: a, Fingerprint: now[a], TargetFingerprint: old[c],
			Decision: row.Decision{Action: "edit", Verdict: "merge:nope", Text: "both"}},
	} {
		if err := s.Answer(ctx, id, []Answer{ans}); !errors.Is(err, ErrChanged) {
			t.Errorf("%s: %v, want ErrChanged", name, err)
		}
	}
	if n := decided(t, s, id); n != 0 {
		t.Fatalf("%d rows decided", n)
	}
	// A multi-row answer where one row's target changed stores nothing.
	err := s.Answer(ctx, id, []Answer{
		{Row: c, Fingerprint: now[c], Decision: row.Decision{Action: "accept"}},
		{Row: a, Fingerprint: now[a], TargetFingerprint: old[c], Decision: edit},
	})
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("%v, want ErrChanged", err)
	}
	if _, got, _ := s.Round(ctx, id); got[1].Decision != nil {
		t.Fatalf("stored %+v", got[1].Decision)
	}
	if err := s.Answer(ctx, id, []Answer{{Row: a, Fingerprint: now[a], TargetFingerprint: now[c], Decision: edit}}); err != nil {
		t.Fatalf("with the current target print: %v", err)
	}
}

// Accepting or editing a merge into a row the round lacks is refused.
func TestAMergeIntoAMissingRowIsRefused(t *testing.T) {
	s, _ := open(t)
	id, rows := round(t, s)
	a := rows[0].ID
	if _, err := s.AddRows(ctx, id, []row.Row{{ID: a, Verdict: "merge:mem-1", Text: "both"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(ctx, id, a, row.Decision{Action: "accept"}); err == nil {
		t.Error("accepted a merge into a missing row")
	}
	if err := s.Decide(ctx, id, a, row.Decision{Action: "edit", Verdict: "merge:mem-2", Text: "x"}); err == nil {
		t.Error("edited to a merge into a missing row")
	}
	if err := s.Answer(ctx, id, []Answer{{Row: a, Fingerprint: prints(t, s, id)[a], Decision: row.Decision{Action: "accept"}}}); !errors.Is(err, ErrChanged) {
		t.Errorf("answer: %v, want ErrChanged", err)
	}
	if err := s.Decide(ctx, id, a, row.Decision{Action: "reject"}); err != nil {
		t.Errorf("reject: %v", err)
	}
}

// A decision approving merge:B, made while B was not in the round, is
// dropped when B is added: it never saw B.
func TestAddingAMergeTargetDropsDecisionsOnIt(t *testing.T) {
	s, _ := open(t)
	id, rows := round(t, s)
	a := rows[0].ID
	if _, err := s.AddRows(ctx, id, []row.Row{{ID: a, Verdict: "merge:mem-1", Text: "both"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO decisions(round_id, row_id, action, decided_at, sent_at) VALUES (?,?,'accept',1,1)`, id, a); err != nil {
		t.Fatal(err)
	}
	res, err := s.AddRows(ctx, id, []row.Row{{ID: "mem-1", Check: "intake", Source: row.Source{Entry: "MEMORY.md#1"}, Passage: "x"}})
	if err != nil || res.Added != 1 || res.Cleared != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, got, _ := s.Round(ctx, id); got[0].Decision != nil {
		t.Fatalf("kept %+v", got[0].Decision)
	}
}
