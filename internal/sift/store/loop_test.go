package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/row"
)

// round records a round decided per item, of three rows: a judgment, a
// second judgment and a certain one, each with the agent's verdict (keep),
// so the round is ready.
func round(t *testing.T, s *Store) (int64, []row.Row) {
	t.Helper()
	rows := []row.Row{
		finding("/w/a/CLAUDE.md", "stale-status", "- Never push."),
		finding("/w/a/CLAUDE.md", "size", "whole"),
		finding("/w/b/AGENTS.md", "dead-path", "see `gone.md`"),
	}
	rows[2].Certain = true
	for i := range rows {
		rows[i].Verdict = "keep"
	}
	id, err := s.RecordRound(ctx, Round{Kind: "backlog"}, rows)
	if err != nil {
		t.Fatal(err)
	}
	return id, rows
}

// An audit round's findings are answered by a file's recommendation, not
// by a proposal per row: rows add refuses them and says so.
func TestAddRowsRefusesAnAuditRoundsFindings(t *testing.T) {
	s, _ := open(t)
	r := finding("/w/a/CLAUDE.md", "stale-status", "- Never push.")
	id, err := s.RecordRound(ctx, Round{Kind: "on-demand"}, []row.Row{r})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AddRows(ctx, id, []row.Row{{ID: r.ID, Verdict: "rewrite", Text: "- Push to a branch."}})
	if err == nil || !strings.Contains(err.Error(), "sift propose") {
		t.Fatalf("got %v", err)
	}
	if _, rows, _ := s.Round(ctx, id); rows[0].Verdict != "" {
		t.Fatalf("stored %+v", rows[0])
	}
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
	if got[0].Check != "stale-status" || got[0].Passage != "- Never push." {
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
		if _, err := s.AddRows(ctx, id, []row.Row{{ID: rows[1].ID, Verdict: "delete"}, c.in}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want %q", name, err, c.want)
		}
	}
	// All or nothing: the good row in each batch was not written either.
	if _, got, _ := s.Round(ctx, id); got[1].Verdict != "keep" {
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
	sd, err := s.Send(ctx, id, "sess-1", nil)
	if err != nil || sd.ID == 0 {
		t.Fatalf("%+v %v", sd, err)
	}
	if sd.Counts != (Counts{Edit: 1, Reject: 1}) || sd.Round != id || sd.Owner != "sess-1" {
		t.Errorf("counts %+v", sd)
	}
	if len(sd.Notes) != 1 || sd.Notes[0].Note != "shorter" || !strings.Contains(sd.Notes[0].Row, "CLAUDE.md:1") {
		t.Errorf("notes %+v", sd.Notes)
	}
	// Nothing new: no second send.
	if again, err := s.Send(ctx, id, "", nil); err != nil || again.ID != 0 {
		t.Fatalf("an empty send was recorded: %+v %v", again, err)
	}
	// A decision on the certain row is a new answer.
	if err := s.Decide(ctx, id, rows[2].ID, row.Decision{Action: "reject"}); err != nil {
		t.Fatal(err)
	}
	two, _ := s.Send(ctx, id, "", nil)
	if two.Counts != (Counts{Reject: 1}) {
		t.Errorf("second send %+v", two.Counts)
	}
	if und, _ := s.Undelivered(ctx); len(und) != 2 {
		t.Fatalf("undelivered %d", len(und))
	}
	// sess-1 owns the first send and is present: sess-2 can't take it.
	if got, ok, err := s.ClaimSend(ctx, "sess-2", []string{"sess-1", "sess-2"}); err != nil || !ok || got.ID == sd.ID {
		t.Fatalf("claim of a present owner's send %+v %v %v", got, ok, err)
	}
	got, ok, err := s.ClaimSend(ctx, "sess-2", nil)
	if err != nil || !ok || got.ID != sd.ID || got.DeliveredTo != "sess-2" {
		t.Fatalf("claim %+v %v %v", got, ok, err)
	}
	if und, _ := s.Undelivered(ctx); len(und) != 0 {
		t.Fatalf("undelivered after two claims %d", len(und))
	}
	if _, ok, _ := s.ClaimSend(ctx, "sess-2", nil); ok {
		t.Fatal("claimed a delivered send")
	}
	// Sent decisions say so.
	_, rs, _ := s.Round(ctx, id)
	if !rs[0].Decision.Sent || !rs[2].Decision.Sent {
		t.Errorf("sent flags %+v %+v", rs[0].Decision, rs[2].Decision)
	}
}

func TestUndecideAndOwner(t *testing.T) {
	s, _ := open(t)
	id, rows := round(t, s)
	_ = s.Decide(ctx, id, rows[2].ID, row.Decision{Action: "reject"})
	if _, err := s.Undecide(ctx, id, rows[2].ID, prints(t, s, id)[rows[2].ID]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Undecide(ctx, id, "nope", "x"); !errors.Is(err, ErrStale) {
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
	// A success is permanent: the held attempt is kept apart.
	got, err := s.Applies(ctx, id)
	if err != nil || len(got) != 1 || got[0].State != "pr" || got[0].PR != "https://x/pr/1" || got[0].Rows[0] != rows[0].ID ||
		got[0].At.IsZero() || got[0].Last.State != "held" || got[0].Last.Detail != "uncommitted changes" {
		t.Fatalf("%+v %v", got, err)
	}
	// A held record is replaced by the next attempt.
	b := Apply{Round: id, Repo: "/w/b", State: "held", Detail: "dirty"}
	_ = s.RecordApply(ctx, b)
	b.State, b.Detail, b.Branch = "branch", "", "sift/round-1"
	_ = s.RecordApply(ctx, b)
	got, _ = s.Applies(ctx, id)
	if got[1].State != "branch" || got[1].Last.State != "" {
		t.Fatalf("%+v", got[1])
	}
}

// An intake row is the agent's own, so any change to what it would do (its
// source, lines or passage as much as its proposal) drops a decision, sent
// or not: the user approved the old row, not this one.
func TestAddRowsClearsADecisionOnAChangedIntakeRow(t *testing.T) {
	base := row.Row{ID: "mem-1", Check: "intake", Summary: "a memory entry", Passage: "use the shared runner",
		Source: row.Source{File: "/w/a/CLAUDE.md", Repo: "/w/a", Path: "CLAUDE.md", Start: 3, End: 3}, Verdict: "delete"}
	for name, change := range map[string]func(*row.Row){
		"repo":    func(r *row.Row) { r.Source.Repo = "/w/b" },
		"path":    func(r *row.Row) { r.Source.Path = "AGENTS.md" },
		"file":    func(r *row.Row) { r.Source.File = "/w/a/AGENTS.md" },
		"lines":   func(r *row.Row) { r.Source.Start, r.Source.End = 9, 9 },
		"entry":   func(r *row.Row) { r.Source.Entry = "MEMORY.md#2" },
		"passage": func(r *row.Row) { r.Passage = "use the other runner" },
		"title":   func(r *row.Row) { r.Title = "t" },
	} {
		s, _ := open(t)
		id, _ := round(t, s)
		if _, err := s.AddRows(ctx, id, []row.Row{base}); err != nil {
			t.Fatal(err)
		}
		if err := s.Decide(ctx, id, base.ID, row.Decision{Action: "accept"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Send(ctx, id, "", nil); err != nil {
			t.Fatal(err)
		}
		next := base
		change(&next)
		res, err := s.AddRows(ctx, id, []row.Row{next})
		if err != nil || res.Cleared != 1 {
			t.Errorf("%s: %+v %v", name, res, err)
		}
		_, got, _ := s.Round(ctx, id)
		if d := got[len(got)-1].Decision; d != nil {
			t.Errorf("%s: the decision survived: %+v", name, d)
		}
	}
}
