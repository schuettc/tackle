package store

import (
	"errors"
	"slices"
	"testing"

	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/row"
)

// shownNow is what a page that loaded the round now shows unsent, as its
// Send names it: each decision's id and its item's print.
func shownNow(t *testing.T, s *Store, id int64) *Shown {
	t.Helper()
	sh := &Shown{Files: map[string]Seen{}, Rows: map[string]Seen{}}
	_, rows, err := s.Round(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Decision != nil && !r.Decision.Sent {
			sh.Rows[r.ID] = Seen{Fingerprint: r.Fingerprint, Decision: r.Decision.ID}
		}
	}
	for k, it := range items(t, s, id) {
		if it.Decision != nil && !it.Decision.Sent {
			sh.Files[k] = Seen{Fingerprint: it.Fingerprint, Decision: it.Decision.ID}
		}
	}
	return sh
}

// answer decides rows against what the page shows now.
func answer(t *testing.T, s *Store, id int64, rowID string, d row.Decision, target string) {
	t.Helper()
	_, rows, err := s.Round(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	a := Answer{Row: rowID, Decision: d}
	for _, r := range rows {
		if r.ID == rowID {
			a.Fingerprint = r.Fingerprint
			a.DecisionID = decisionID(r.Decision)
		}
		if r.ID == target {
			a.TargetFingerprint = r.Fingerprint
		}
	}
	if _, err := s.Answer(ctx, id, []Answer{a}); err != nil {
		t.Fatal(err)
	}
}

func decisionID(d *row.Decision) string {
	if d == nil {
		return ""
	}
	return d.ID
}

// A Send binds to the decision the page showed by its id, not by its
// fields: each case presses Send, changes the round underneath before the
// Send is stored, and expects the row it changed to stay unsent.
func TestASendBindsToTheDecisionItShowed(t *testing.T) {
	for name, under := range map[string]func(t *testing.T, s *Store, id int64, a, c string){
		// The agent re-proposes a (its decision is dropped), and a new
		// accept with the same note follows: the same fields, another
		// decision on another proposal.
		"re-proposed, then accepted again": func(t *testing.T, s *Store, id int64, a, _ string) {
			if _, err := s.AddRows(ctx, id, []row.Row{{ID: a, Verdict: "keep", Text: "kept, reworded"}}); err != nil {
				t.Fatal(err)
			}
			answer(t, s, id, a, row.Decision{Action: "accept", Note: "fine"}, "")
		},
		// a is an edit to merge:c, and c's proposal changes: a's decision
		// stays, but what it merges into is not what the page showed.
		"an edited merge whose target changed": func(t *testing.T, s *Store, id int64, _, c string) {
			if _, err := s.AddRows(ctx, id, []row.Row{{ID: c, Verdict: "keep", Text: "c, reworded"}}); err != nil {
				t.Fatal(err)
			}
		},
		// The same decision, made again after Send was pressed: cleared,
		// then accepted with the same note.
		"an identical re-decision": func(t *testing.T, s *Store, id int64, a, _ string) {
			_, rows, _ := s.Round(ctx, id)
			if _, err := s.Undecide(ctx, id, a, rows[0].Fingerprint, decisionID(rows[0].Decision)); err != nil {
				t.Fatal(err)
			}
			answer(t, s, id, a, row.Decision{Action: "accept", Note: "fine"}, "")
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := open(t)
			id, rows := round(t, s)
			a, b, c := rows[0].ID, rows[1].ID, rows[2].ID
			if name == "an edited merge whose target changed" {
				answer(t, s, id, a, row.Decision{Action: "edit", Verdict: "merge:" + c, Text: "both", Note: "fine"}, c)
			} else {
				answer(t, s, id, a, row.Decision{Action: "accept", Note: "fine"}, "")
			}
			answer(t, s, id, b, row.Decision{Action: "reject"}, "")
			// The page shows a and b unsent, and Send is pressed; the Send
			// is stored after the change.
			shown := shownNow(t, s, id)
			under(t, s, id, a, c)
			sd, err := s.Send(ctx, id, "", shown)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(sd.Rows, []string{b}) {
				t.Fatalf("sent %v, want [%s] (a is not what the page showed)", sd.Rows, b)
			}
			_, now, _ := s.Round(ctx, id)
			if now[0].Decision == nil || now[0].Decision.Sent {
				t.Fatalf("a: %+v, want an unsent decision", now[0].Decision)
			}
		})
	}
}

// The file side: the agent re-proposes m (the group's decisions drop), and
// the same accept is given again; a Send pressed before sends only q.
func TestASendBindsToTheFileDecisionItShowed(t *testing.T) {
	s, _ := open(t)
	id, f := ready(t, s)
	g, m, q := f["g"].Key, f["m"].Key, f["q"].Key
	if _, err := s.DecideFile(ctx, id, g, rec.Decision{Action: "accept", Note: "fine"}, seenOf(t, s, id, g, m)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideFile(ctx, id, q, rec.Decision{Action: "reject"}, seenOf(t, s, id, q)); err != nil {
		t.Fatal(err)
	}
	shown := shownNow(t, s, id)
	rm := recM(f, g)
	rm.Summary = "Guidance, reworded."
	if _, err := s.Propose(ctx, id, []rec.Rec{rm}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideFile(ctx, id, g, rec.Decision{Action: "accept", Note: "fine"}, seenOf(t, s, id, g, m)); err != nil {
		t.Fatal(err)
	}
	sd, err := s.Send(ctx, id, "", shown)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sd.Files, []string{q}) {
		t.Fatalf("sent %v, want [%s]", sd.Files, q)
	}
}

// A row's print can't tell an accept from a reject; its decision's id can.
// A decision or clear given against a decision another page replaced since
// (same print, another id) is refused, and nothing changes.
func TestADecisionBindsToTheDecisionItShowed(t *testing.T) {
	s, _ := open(t)
	id, rows := round(t, s)
	a := rows[0].ID
	answer(t, s, id, a, row.Decision{Action: "reject"}, "")
	_, was, _ := s.Round(ctx, id)
	// Another page makes it an accept: the print stays, the id does not.
	answer(t, s, id, a, row.Decision{Action: "accept"}, "")
	_, now, _ := s.Round(ctx, id)
	if now[0].Fingerprint != was[0].Fingerprint || now[0].Decision.ID == was[0].Decision.ID {
		t.Fatalf("print %s → %s, id %s → %s", was[0].Fingerprint, now[0].Fingerprint, was[0].Decision.ID, now[0].Decision.ID)
	}
	if _, err := s.Undecide(ctx, id, a, was[0].Fingerprint, was[0].Decision.ID); !errors.Is(err, ErrChanged) {
		t.Fatalf("a clear of the replaced reject: %v, want ErrChanged", err)
	}
	if _, err := s.Answer(ctx, id, []Answer{{Row: a, Fingerprint: was[0].Fingerprint, DecisionID: was[0].Decision.ID,
		Decision: row.Decision{Action: "edit", Text: "mine"}}}); !errors.Is(err, ErrChanged) {
		t.Fatalf("an edit over the replaced reject: %v, want ErrChanged", err)
	}
	if _, after, _ := s.Round(ctx, id); after[0].Decision.ID != now[0].Decision.ID || after[0].Decision.Action != "accept" {
		t.Fatalf("after: %+v", after[0].Decision)
	}

	// The file side.
	s, _ = open(t)
	fid, f := ready(t, s)
	q := f["q"].Key
	before := seenOf(t, s, fid, q)
	if _, err := s.DecideFile(ctx, fid, q, rec.Decision{Action: "reject"}, before); err != nil {
		t.Fatal(err)
	}
	rejected := seenOf(t, s, fid, q)
	if _, err := s.DecideFile(ctx, fid, q, rec.Decision{Action: "accept"}, rejected); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UndecideFile(ctx, fid, q, rejected); !errors.Is(err, ErrChanged) {
		t.Fatalf("a file clear of the replaced reject: %v, want ErrChanged", err)
	}
	if _, err := s.DecideFile(ctx, fid, q, rec.Decision{Action: "edit", Content: "# Q, mine\n"}, before); !errors.Is(err, ErrChanged) {
		t.Fatalf("an edit against no decision: %v, want ErrChanged", err)
	}
	if d := items(t, s, fid)[q].Decision; d == nil || d.Action != "accept" {
		t.Fatalf("after: %+v", d)
	}
}
