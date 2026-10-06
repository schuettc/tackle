package store

import (
	"errors"
	"testing"

	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/row"
)

// recQSame keeps q's one finding and leaves the file as it is: nothing to
// change.
func recQSame(f map[string]rec.File) rec.Rec {
	return rec.Rec{File: f["q"].Key, Base: f["q"].Base, Content: f["q"].Content,
		Findings: []rec.Account{{Row: "neg3", Did: "kept", How: "the rule still holds"}}, Summary: "Nothing to change."}
}

// readyWithSame is the audit round with g and m linked and changed, and q
// recommended with no change.
func readyWithSame(t *testing.T, s *Store) (int64, map[string]rec.File) {
	t.Helper()
	id, f := audit(t, s)
	if _, err := s.Propose(ctx, id, []rec.Rec{recG(f, f["m"].Key), recM(f, f["g"].Key), recQSame(f)}); err != nil {
		t.Fatal(err)
	}
	return id, f
}

func unmuted(t *testing.T, s *Store, ids ...string) []string {
	t.Helper()
	var rows []row.Row
	for _, id := range ids {
		rows = append(rows, row.Row{ID: id})
	}
	kept, _, err := s.Unmuted(ctx, rows)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range kept {
		out = append(out, r.ID)
	}
	return out
}

// A file whose recommendation leaves it as it is has nothing to change; a
// file the recommendation changes, or one linked to it, does not.
func TestAFileWithNoChangeIsMarked(t *testing.T) {
	s, _ := open(t)
	id, f := readyWithSame(t, s)
	its := items(t, s, id)
	if !its[f["q"].Key].Unchanged {
		t.Error("q: its recommendation leaves it as it is")
	}
	for _, k := range []string{"g", "m"} {
		if its[f[k].Key].Unchanged {
			t.Errorf("%s: its recommendation changes it", k)
		}
	}
}

// Agreeing with a no-change file accepts it and mutes its findings until
// their passage changes (a changed passage is another row id); clearing
// the agreement unmutes them. Accepting a file that changes mutes nothing.
func TestAgreeingMutesTheFindings(t *testing.T) {
	s, _ := open(t)
	id, f := readyWithSame(t, s)
	q := f["q"].Key
	if _, err := s.DecideFile(ctx, id, q, rec.Decision{Action: "accept"}, seenOf(t, s, id, q)); err != nil {
		t.Fatal(err)
	}
	if got := unmuted(t, s, "neg3", "neg3-changed"); len(got) != 1 || got[0] != "neg3-changed" {
		t.Fatalf("after agreeing, unmuted %v; want only the changed passage", got)
	}
	if it := items(t, s, id)[q]; it.Decision == nil || it.Decision.Action != "accept" || !it.Muted {
		t.Fatalf("q after agreeing: %+v muted %v", it.Decision, it.Muted)
	}
	if _, err := s.UndecideFile(ctx, id, q, seenOf(t, s, id, q)); err != nil {
		t.Fatal(err)
	}
	if got := unmuted(t, s, "neg3"); len(got) != 1 {
		t.Fatal("clearing the agreement left the finding muted")
	}
	g := f["g"].Key
	if _, err := s.DecideFile(ctx, id, g, rec.Decision{Action: "accept"}, seenOf(t, s, id, g, f["m"].Key)); err != nil {
		t.Fatal(err)
	}
	if got := unmuted(t, s, "neg", "dead", "neg2"); len(got) != 3 {
		t.Fatalf("accepting a change muted %v", got)
	}
}

// Disagreeing with a no-change file is a reject with a note, which is
// required, and stays required; Send carries it back to the agent as a
// file to recommend again. The agent's next recommendation for it, a real
// change, takes it out of nothing-to-change and drops the reject. An
// agreement replaced by a disagreement unmutes.
func TestDisagreeingTakesANoteAndGoesBack(t *testing.T) {
	s, _ := open(t)
	id, f := readyWithSame(t, s)
	q := f["q"].Key
	if _, err := s.DecideFile(ctx, id, q, rec.Decision{Action: "reject", Note: "  "}, seenOf(t, s, id, q)); !errors.Is(err, ErrNoteNeeded) {
		t.Fatalf("a disagreement with no note: %v", err)
	}
	if _, err := s.DecideFile(ctx, id, q, rec.Decision{Action: "accept"}, seenOf(t, s, id, q)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideFile(ctx, id, q, rec.Decision{Action: "reject", Note: "y is gone: say z"}, seenOf(t, s, id, q)); err != nil {
		t.Fatal(err)
	}
	if got := unmuted(t, s, "neg3"); len(got) != 1 {
		t.Fatal("a disagreement left the agreement's mute")
	}
	if err := s.NoteFile(ctx, id, q, ""); !errors.Is(err, ErrNoteNeeded) {
		t.Fatalf("emptying a disagreement's note: %v", err)
	}
	sd, err := s.Send(ctx, id, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var again []Note
	for _, n := range sd.Notes {
		if n.Again {
			again = append(again, n)
		}
	}
	if len(again) != 1 || again[0].Row != f["q"].Source.File || again[0].Note != "y is gone: say z" {
		t.Fatalf("the send's notes %+v", sd.Notes)
	}
	r := recQ(f)
	if _, err := s.Propose(ctx, id, []rec.Rec{r}); err != nil {
		t.Fatal(err)
	}
	it := items(t, s, id)[q]
	if it.Unchanged || it.Decision != nil {
		t.Fatalf("after the agent's rewrite: unchanged %v decision %+v", it.Unchanged, it.Decision)
	}
	// A plain reject of a file that changes needs no note.
	if _, err := s.DecideFile(ctx, id, q, rec.Decision{Action: "reject"}, seenOf(t, s, id, q)); err != nil {
		t.Fatal(err)
	}
}

// The agent's re-recommendation of an agreed file drops the agreement and
// its mutes with it.
func TestReproposingAnAgreedFileUnmutes(t *testing.T) {
	s, _ := open(t)
	id, f := readyWithSame(t, s)
	q := f["q"].Key
	if _, err := s.DecideFile(ctx, id, q, rec.Decision{Action: "accept"}, seenOf(t, s, id, q)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Propose(ctx, id, []rec.Rec{recQ(f)}); err != nil {
		t.Fatal(err)
	}
	if got := unmuted(t, s, "neg3"); len(got) != 1 {
		t.Fatal("the dropped agreement left its mute")
	}
}
