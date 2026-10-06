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

// ids is the id of each row's decision in force ("" for none), as the page
// shows it.
func ids(t *testing.T, s *Store, id int64) map[string]string {
	t.Helper()
	_, rows, err := s.Round(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, r := range rows {
		m[r.ID] = idOf(r.Decision)
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
		if _, err := s.Answer(ctx, id, []Answer{ans}); !errors.Is(err, ErrChanged) {
			t.Errorf("%s: %v, want ErrChanged", name, err)
		}
	}
	if n := decided(t, s, id); n != 0 {
		t.Fatalf("%d rows decided", n)
	}
	// A multi-row answer where one row's target changed stores nothing.
	_, err := s.Answer(ctx, id, []Answer{
		{Row: c, Fingerprint: now[c], Decision: row.Decision{Action: "accept"}},
		{Row: a, Fingerprint: now[a], TargetFingerprint: old[c], Decision: edit},
	})
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("%v, want ErrChanged", err)
	}
	if _, got, _ := s.Round(ctx, id); got[1].Decision != nil {
		t.Fatalf("stored %+v", got[1].Decision)
	}
	if _, err := s.Answer(ctx, id, []Answer{{Row: a, Fingerprint: now[a], TargetFingerprint: now[c], Decision: edit}}); err != nil {
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
	if _, err := s.Answer(ctx, id, []Answer{{Row: a, Fingerprint: prints(t, s, id)[a], Decision: row.Decision{Action: "accept"}}}); !errors.Is(err, ErrChanged) {
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

// A row's clear answers the proposal the page showed, as a decision does:
// a page still showing an old proposal is refused, and gets no row to show
// in its place. Answer and Undecide return the rows as they are after.
func TestARowsClearAnswersWhatThePageShowed(t *testing.T) {
	s, _ := open(t)
	id, rows := round(t, s)
	a, c := rows[0].ID, rows[1].ID
	old := prints(t, s, id)
	got, err := s.Answer(ctx, id, []Answer{{Row: c, Fingerprint: old[c], Decision: row.Decision{Action: "reject", Note: "no"}}})
	if err != nil || len(got) != 1 || got[0].ID != c || got[0].Decision == nil || got[0].Decision.Action != "reject" || got[0].Fingerprint != old[c] {
		t.Fatalf("the answered rows: %+v %v", got, err)
	}
	if _, err := s.AddRows(ctx, id, []row.Row{{ID: c, Verdict: "delete"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Answer(ctx, id, []Answer{{Row: c, Fingerprint: prints(t, s, id)[c], Decision: row.Decision{Action: "reject"}}}); err != nil {
		t.Fatal(err)
	}
	after, err := s.Undecide(ctx, id, c, old[c], ids(t, s, id)[c])
	if !errors.Is(err, ErrChanged) || after.ID != "" {
		t.Fatalf("a stale clear: %v, returned %+v", err, after)
	}
	if n := decided(t, s, id); n != 1 {
		t.Fatalf("%d rows decided, want the reject kept", n)
	}
	after, err = s.Undecide(ctx, id, c, prints(t, s, id)[c], ids(t, s, id)[c])
	if err != nil || after.ID != c || after.Decision != nil || after.Fingerprint != prints(t, s, id)[c] {
		t.Fatalf("a current clear: %+v %v", after, err)
	}
	// A note changes the note alone, and needs a decision to sit on.
	if err := s.Note(ctx, id, a, "why"); !errors.Is(err, ErrChanged) {
		t.Fatalf("a note on an undecided row: %v", err)
	}
	if _, err := s.Answer(ctx, id, []Answer{{Row: a, Fingerprint: old[a], Decision: row.Decision{Action: "accept"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Note(ctx, id, a, "why"); err != nil {
		t.Fatal(err)
	}
	if _, got, _ := s.Round(ctx, id); got[0].Decision.Action != "accept" || got[0].Decision.Note != "why" {
		t.Fatalf("after the note: %+v", got[0].Decision)
	}
}

// A row follows the file model: its print covers what the page shows, the
// edit in force over the proposal, so an edit changes it and a clear
// changes it back. Accepting an edited row, alone or in a group, keeps the
// edit (with the accept's note); the proposal comes back only through a
// clear.
func TestAcceptingAnEditedRowKeepsTheEdit(t *testing.T) {
	s, _ := open(t)
	id, rows := round(t, s)
	a, b := rows[0].ID, rows[1].ID
	old := prints(t, s, id)
	snap, err := s.Answer(ctx, id, []Answer{{Row: a, Fingerprint: old[a], Decision: row.Decision{Action: "edit", Text: "mine"}}})
	if err != nil {
		t.Fatal(err)
	}
	if snap[0].Fingerprint == old[a] {
		t.Fatal("the edit left the print as it was: the print does not cover the edit the page shows")
	}
	if snap[0].Fingerprint != prints(t, s, id)[a] {
		t.Fatal("the snapshot's print is not the round's")
	}
	snap, err = s.Answer(ctx, id, []Answer{{Row: a, Fingerprint: snap[0].Fingerprint, DecisionID: snap[0].Decision.ID, Decision: row.Decision{Action: "accept", Note: "fine"}}})
	if err != nil {
		t.Fatal(err)
	}
	if d := snap[0].Decision; d == nil || d.Action != "edit" || d.Text != "mine" || d.Note != "fine" {
		t.Fatalf("a single accept of an edited row: %+v", d)
	}
	// A group accept: a (edited) and b (edited too).
	now := prints(t, s, id)
	if _, err := s.Answer(ctx, id, []Answer{{Row: b, Fingerprint: now[b], Decision: row.Decision{Action: "edit", Text: "theirs"}}}); err != nil {
		t.Fatal(err)
	}
	now, in := prints(t, s, id), ids(t, s, id)
	snap, err = s.Answer(ctx, id, []Answer{
		{Row: a, Fingerprint: now[a], DecisionID: in[a], Decision: row.Decision{Action: "accept"}},
		{Row: b, Fingerprint: now[b], DecisionID: in[b], Decision: row.Decision{Action: "accept"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) != 2 || snap[0].Decision.Action != "edit" || snap[0].Decision.Text != "mine" ||
		snap[1].Decision.Action != "edit" || snap[1].Decision.Text != "theirs" {
		t.Fatalf("a group accept of edited rows: %+v %+v", snap[0].Decision, snap[1].Decision)
	}
	if c, ok := snap[0].Effective(); !ok || c.Text != "mine" {
		t.Fatalf("what apply would do: %+v %v", c, ok)
	}
	// The clear is the way back: the page then shows the proposal again.
	after, err := s.Undecide(ctx, id, a, snap[0].Fingerprint, snap[0].Decision.ID)
	if err != nil || after.Decision != nil || after.Fingerprint != old[a] {
		t.Fatalf("the clear: %+v %v", after, err)
	}
}

// An accept from a page still showing an edit another client has cleared
// since is refused: it would approve the proposal the page never showed.
func TestAStaleAcceptOfAClearedEditIsRefused(t *testing.T) {
	s, _ := open(t)
	id, rows := round(t, s)
	a := rows[0].ID
	old := prints(t, s, id)
	snap, err := s.Answer(ctx, id, []Answer{{Row: a, Fingerprint: old[a], Decision: row.Decision{Action: "edit", Text: "mine"}}})
	if err != nil {
		t.Fatal(err)
	}
	shown, edit := snap[0].Fingerprint, snap[0].Decision.ID
	// Another page clears the edit.
	if _, err := s.Undecide(ctx, id, a, shown, edit); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Answer(ctx, id, []Answer{{Row: a, Fingerprint: shown, DecisionID: edit, Decision: row.Decision{Action: "accept"}}}); !errors.Is(err, ErrChanged) {
		t.Fatalf("a stale accept: %v, want ErrChanged", err)
	}
	if n := decided(t, s, id); n != 0 {
		t.Fatalf("%d rows decided", n)
	}
	if _, err := s.Answer(ctx, id, []Answer{{Row: a, Fingerprint: prints(t, s, id)[a], Decision: row.Decision{Action: "accept"}}}); err != nil {
		t.Fatalf("after a reload: %v", err)
	}
}

// An edited merge:C covers C as the page shows it: when C's shown content
// changes (here C's own edit), the merge's print changes, an accept given
// against the old one is refused, and one given against the new keeps the
// edit.
func TestAnEditedMergeCoversItsTarget(t *testing.T) {
	s, _ := open(t)
	id, rows := round(t, s)
	a, c := rows[0].ID, rows[1].ID
	old := prints(t, s, id)
	snap, err := s.Answer(ctx, id, []Answer{{Row: a, Fingerprint: old[a], TargetFingerprint: old[c],
		Decision: row.Decision{Action: "edit", Verdict: "merge:" + c, Text: "both"}}})
	if err != nil {
		t.Fatal(err)
	}
	merged, edit := snap[0].Fingerprint, snap[0].Decision.ID
	if _, err := s.Answer(ctx, id, []Answer{{Row: c, Fingerprint: old[c], Decision: row.Decision{Action: "edit", Text: "c's own"}}}); err != nil {
		t.Fatal(err)
	}
	now := prints(t, s, id)
	if now[a] == merged {
		t.Fatal("the merge's print did not change with its target's")
	}
	if _, err := s.Answer(ctx, id, []Answer{{Row: a, Fingerprint: merged, DecisionID: edit, Decision: row.Decision{Action: "accept"}}}); !errors.Is(err, ErrChanged) {
		t.Fatalf("an accept against the old target: %v, want ErrChanged", err)
	}
	snap, err = s.Answer(ctx, id, []Answer{{Row: a, Fingerprint: now[a], DecisionID: edit, Decision: row.Decision{Action: "accept"}}})
	if err != nil {
		t.Fatal(err)
	}
	if d := snap[0].Decision; d.Action != "edit" || d.Verdict != "merge:"+c || d.Text != "both" {
		t.Fatalf("the accept: %+v", d)
	}
}
