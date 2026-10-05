package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/row"
)

// round records a round of three rows: a judgment, a second judgment and a
// certain one.
func round(t *testing.T, s *Store) (int64, []row.Row) {
	t.Helper()
	rows := []row.Row{
		finding("/w/a/CLAUDE.md", "negative-rule", "- Never push."),
		finding("/w/a/CLAUDE.md", "size", "whole"),
		finding("/w/b/AGENTS.md", "dead-path", "see `gone.md`"),
	}
	rows[2].Certain = true
	id, err := s.RecordRound(ctx, Round{Kind: "on-demand"}, rows)
	if err != nil {
		t.Fatal(err)
	}
	return id, rows
}

func TestAddRowsMergesProposals(t *testing.T) {
	s, _ := open(t)
	id, rows := round(t, s)
	_, rev0, _ := s.Latest(ctx)
	in := []row.Row{{ID: rows[0].ID, Check: "ignored", Passage: "ignored", Verdict: "rewrite", Text: "- Push to a branch.", Reason: "guidance"}}
	res, err := s.AddRows(ctx, id, in)
	if err != nil || res.Updated != 1 || res.Added != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	r, got, _ := s.Round(ctx, id)
	if got[0].Verdict != "rewrite" || got[0].Text != "- Push to a branch." || got[0].Reason != "guidance" {
		t.Errorf("proposal not merged: %+v", got[0])
	}
	if got[0].Check != "negative-rule" || got[0].Passage != "- Never push." {
		t.Errorf("a check-owned field changed: %+v", got[0])
	}
	if r.Rev <= rev0 {
		t.Errorf("rev %d not past %d", r.Rev, rev0)
	}
}

func TestAddRowsRejects(t *testing.T) {
	s, _ := open(t)
	id, rows := round(t, s)
	for name, c := range map[string]struct {
		in   row.Row
		want string
	}{
		"unknown verdict":   {row.Row{ID: rows[0].ID, Verdict: "cut"}, "not a verdict"},
		"not in round":      {row.Row{ID: "0000000000000000", Verdict: "keep"}, "not in round"},
		"no id":             {row.Row{Verdict: "keep"}, "no id"},
		"a decision":        {row.Row{ID: rows[0].ID, Decision: &row.Decision{Action: "accept"}}, "decision"},
		"intake, no source": {row.Row{ID: "m1", Check: "intake", Summary: "s"}, "source"},
	} {
		if _, err := s.AddRows(ctx, id, []row.Row{{ID: rows[1].ID, Verdict: "keep"}, c.in}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want %q", name, err, c.want)
		}
	}
	// All or nothing: the good row in each batch was not written either.
	if _, got, _ := s.Round(ctx, id); got[1].Verdict != "" {
		t.Errorf("a rejected batch was partly written: %+v", got[1])
	}
}

// Intake rows are new to the round: added after the audit's rows, never
// certain; sending one again updates it.
func TestAddRowsAddsIntakeRows(t *testing.T) {
	s, _ := open(t)
	id, _ := round(t, s)
	in := row.Row{ID: "mem-7", Check: "intake", Summary: "a memory entry", Source: row.Source{Entry: "MEMORY.md#7"},
		Passage: "use the shared runner", Verdict: "move", Destination: "CLAUDE.md#CI", Certain: true}
	res, err := s.AddRows(ctx, id, []row.Row{in})
	if err != nil || res.Added != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	in.Verdict = "drop:obsolete"
	if res, err = s.AddRows(ctx, id, []row.Row{in}); err != nil || res.Updated != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	_, got, _ := s.Round(ctx, id)
	last := got[len(got)-1]
	if len(got) != 4 || last.ID != "mem-7" || last.Certain || last.Verdict != "drop:obsolete" {
		t.Fatalf("rows %+v", got)
	}
}

// A decision answers one proposal: a changed proposal clears it.
func TestAddRowsClearsADecisionOnAChangedProposal(t *testing.T) {
	s, _ := open(t)
	id, rows := round(t, s)
	if _, err := s.AddRows(ctx, id, []row.Row{{ID: rows[0].ID, Verdict: "delete"}, {ID: rows[1].ID, Verdict: "keep"}}); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows[:2] {
		if err := s.Decide(ctx, id, r.ID, row.Decision{Action: "accept"}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.AddRows(ctx, id, []row.Row{{ID: rows[0].ID, Verdict: "rewrite", Text: "x"}, {ID: rows[1].ID, Verdict: "keep"}})
	if err != nil || res.Cleared != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	_, got, _ := s.Round(ctx, id)
	if got[0].Decision != nil || got[1].Decision == nil {
		t.Fatalf("decisions %+v %+v", got[0].Decision, got[1].Decision)
	}
}

func TestSendCountsAndClaims(t *testing.T) {
	s, _ := open(t)
	id, rows := round(t, s)
	_ = s.Decide(ctx, id, rows[0].ID, row.Decision{Action: "edit", Text: "mine", Note: "shorter"})
	_ = s.Decide(ctx, id, rows[1].ID, row.Decision{Action: "reject"})
	sd, err := s.Send(ctx, id, "sess-1")
	if err != nil || sd.ID == 0 {
		t.Fatalf("%+v %v", sd, err)
	}
	if sd.Counts != (Counts{Edit: 1, Reject: 1, Applied: 1}) || sd.Round != id || sd.Owner != "sess-1" {
		t.Errorf("counts %+v", sd)
	}
	if len(sd.Notes) != 1 || sd.Notes[0].Note != "shorter" || !strings.Contains(sd.Notes[0].Row, "CLAUDE.md:1") {
		t.Errorf("notes %+v", sd.Notes)
	}
	// Nothing new: no second send.
	if again, err := s.Send(ctx, id, ""); err != nil || again.ID != 0 {
		t.Fatalf("an empty send was recorded: %+v %v", again, err)
	}
	// Undoing the certain row is a new answer.
	if err := s.Decide(ctx, id, rows[2].ID, row.Decision{Action: "reject"}); err != nil {
		t.Fatal(err)
	}
	two, _ := s.Send(ctx, id, "")
	if two.Counts != (Counts{Undone: 1}) {
		t.Errorf("second send %+v", two.Counts)
	}
	if und, _ := s.Undelivered(ctx); len(und) != 2 {
		t.Fatalf("undelivered %d", len(und))
	}
	got, ok, err := s.ClaimSend(ctx, "sess-2")
	if err != nil || !ok || got.ID != sd.ID || got.DeliveredTo != "sess-2" {
		t.Fatalf("claim %+v %v %v", got, ok, err)
	}
	if und, _ := s.Undelivered(ctx); len(und) != 1 {
		t.Fatalf("undelivered after a claim %d", len(und))
	}
	_, _, _ = s.ClaimSend(ctx, "sess-2")
	if _, ok, _ := s.ClaimSend(ctx, "sess-2"); ok {
		t.Fatal("claimed a delivered send")
	}
	// Sent decisions say so.
	_, rs, _ := s.Round(ctx, id)
	if !rs[0].Decision.Sent || !rs[2].Decision.Sent {
		t.Errorf("sent flags %+v %+v", rs[0].Decision, rs[2].Decision)
	}
}

// A round whose certain fixes nobody touched is still worth one send.
func TestSendCarriesTheAppliedRowsOnce(t *testing.T) {
	s, _ := open(t)
	id, _ := round(t, s)
	sd, err := s.Send(ctx, id, "")
	if err != nil || sd.ID == 0 || sd.Counts != (Counts{Applied: 1}) {
		t.Fatalf("%+v %v", sd, err)
	}
	if again, _ := s.Send(ctx, id, ""); again.ID != 0 {
		t.Fatal("the applied rows were sent twice")
	}
	if n, err := s.Sends(ctx, id); n != 1 || err != nil {
		t.Fatalf("sends %d %v", n, err)
	}
}

func TestUndecideAndOwner(t *testing.T) {
	s, _ := open(t)
	id, rows := round(t, s)
	_ = s.Decide(ctx, id, rows[2].ID, row.Decision{Action: "reject"})
	if err := s.Undecide(ctx, id, rows[2].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Undecide(ctx, id, "nope"); !errors.Is(err, ErrStale) {
		t.Fatalf("err %v", err)
	}
	if err := s.SetOwner(ctx, id, "sess", "pi · w"); err != nil {
		t.Fatal(err)
	}
	r, got, _ := s.Round(ctx, id)
	if got[2].Decision != nil || r.OwnerSession != "sess" || r.OwnerLabel != "pi · w" {
		t.Fatalf("%+v %+v", r, got[2].Decision)
	}
}

func TestApplies(t *testing.T) {
	s, _ := open(t)
	id, rows := round(t, s)
	a := Apply{Round: id, Repo: "/w/a", Base: "origin/main", Branch: "sift/round-1", PR: "https://x/pr/1", State: "pr", Rows: []string{rows[0].ID}}
	if err := s.RecordApply(ctx, a); err != nil {
		t.Fatal(err)
	}
	a.State, a.Detail = "held", "uncommitted changes"
	if err := s.RecordApply(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := s.Applies(ctx, id)
	if err != nil || len(got) != 1 || got[0].State != "held" || got[0].Rows[0] != rows[0].ID || got[0].At.IsZero() {
		t.Fatalf("%+v %v", got, err)
	}
}
