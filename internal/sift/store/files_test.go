package store

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/row"
)

// audit records an audit round of three files: g (two findings, one
// certain), m (one) and q (one), and returns the round and the files by
// name.
func audit(t *testing.T, s *Store) (int64, map[string]rec.File) {
	t.Helper()
	g := rec.NewFile(row.Source{File: "/h/AGENTS.md"}, "global", 8000, "# G\n\n- Never do x.\n- See `gone.md`.\n")
	m := rec.NewFile(row.Source{File: "/w/m/AGENTS.md", Repo: "/w/m", Ref: "origin/main", Path: "AGENTS.md"}, "repo", 6000, "# M\n\n- Don't skip z.\n")
	q := rec.NewFile(row.Source{File: "/w/q/AGENTS.md", Repo: "/w/q", Ref: "HEAD", Path: "AGENTS.md"}, "repo", 6000, "# Q\n\n- No y.\n")
	rows := []row.Row{
		{ID: "neg", Check: "stale-status", Source: g.Source},
		{ID: "dead", Check: "dead-path", Source: g.Source, Certain: true},
		{ID: "neg2", Check: "stale-status", Source: m.Source},
		{ID: "neg3", Check: "stale-status", Source: q.Source},
	}
	g.Rows, m.Rows, q.Rows = []string{"neg", "dead"}, []string{"neg2"}, []string{"neg3"}
	id, err := s.RecordAudit(ctx, Round{Kind: "on-demand"}, rows, []rec.File{g, m, q})
	if err != nil {
		t.Fatal(err)
	}
	return id, map[string]rec.File{"g": g, "m": m, "q": q}
}

func recG(f map[string]rec.File, links ...string) rec.Rec {
	return rec.Rec{File: f["g"].Key, Base: f["g"].Base, Content: "# G\n\n- Do x safely.\n",
		Findings: []rec.Account{{Row: "neg", Did: "fixed", How: "guidance"}, {Row: "dead", Did: "fixed", How: "removed"}},
		Links:    links, Summary: "Guidance, and a dead path gone."}
}

func recM(f map[string]rec.File, links ...string) rec.Rec {
	return rec.Rec{File: f["m"].Key, Base: f["m"].Base, Content: "# M\n\n- Run z.\n- Do x safely here.\n",
		Findings: []rec.Account{{Row: "neg2", Did: "fixed", How: "guidance"}}, Links: links, Summary: "Guidance."}
}

func recQ(f map[string]rec.File) rec.Rec {
	return rec.Rec{File: f["q"].Key, Base: f["q"].Base, Content: "# Q\n\n- Use z instead of y.\n",
		Findings: []rec.Account{{Row: "neg3", Did: "fixed", How: "guidance"}}, Summary: "Guidance."}
}

func items(t *testing.T, s *Store, id int64) map[string]FileItem {
	t.Helper()
	fs, err := s.Files(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]FileItem{}
	for _, f := range fs {
		out[f.Key] = f
	}
	return out
}

// seenOf is what the page shows now of the files keys: each one's print
// and the id of its decision.
func seenOf(t *testing.T, s *Store, id int64, keys ...string) map[string]Seen {
	t.Helper()
	its := items(t, s, id)
	out := map[string]Seen{}
	for _, k := range keys {
		out[k] = Seen{Fingerprint: its[k].Fingerprint, Decision: fileID(its[k].Decision)}
	}
	return out
}

// The round is recommending until every file with findings has a
// recommendation; next hands out the files without one, in order.
func TestNextAndProposeMakeTheRoundReady(t *testing.T) {
	s, _ := open(t)
	id, f := audit(t, s)
	st, err := s.State(ctx, id)
	if err != nil || st.State != Recommending || st.Files != 3 || st.Recommended != 0 {
		t.Fatalf("%+v %v", st, err)
	}
	next, err := s.Next(ctx, id)
	// The store keeps the base's hash and size, never the content (a file
	// may hold a secret): the caller reads it back at its source.
	if err != nil || next == nil || next.Key != f["g"].Key || next.Base != f["g"].Base || next.Size != len(f["g"].Content) || next.Content != "" {
		t.Fatalf("next %+v %v", next, err)
	}
	// Linked files go in together, by path or key.
	g, m := recG(f, f["m"].Source.File), recM(f, f["g"].Key)
	g.File = f["g"].Source.File
	res, err := s.Propose(ctx, id, []rec.Rec{g, m})
	if err != nil || res.Stored != 2 || res.Left != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	its := items(t, s, id)
	if r := its[f["g"].Key].Rec; r == nil || r.File != f["g"].Key || r.Links[0] != f["m"].Key {
		t.Fatalf("stored %+v", r)
	}
	if got := its[f["g"].Key].Group; strings.Join(got, ",") != strings.Join(rec.Group(map[string]rec.Rec{f["g"].Key: recG(f, f["m"].Key), f["m"].Key: recM(f, f["g"].Key)}, f["g"].Key), ",") {
		t.Errorf("group %v", got)
	}
	if next, _ := s.Next(ctx, id); next == nil || next.Key != f["q"].Key {
		t.Fatalf("next after two: %+v", next)
	}
	if _, err := s.Propose(ctx, id, []rec.Rec{recQ(f)}); err != nil {
		t.Fatal(err)
	}
	if next, _ := s.Next(ctx, id); next != nil {
		t.Fatalf("next when done: %+v", next)
	}
	if st, _ := s.State(ctx, id); st.State != Ready || st.Recommended != 3 {
		t.Fatalf("%+v", st)
	}
}

// A batch with one bad recommendation stores nothing.
func TestProposeIsAllOrNothing(t *testing.T) {
	s, _ := open(t)
	id, f := audit(t, s)
	bad := recQ(f)
	bad.Findings = nil
	if _, err := s.Propose(ctx, id, []rec.Rec{recG(f), bad}); err == nil || !strings.Contains(err.Error(), "neg3") {
		t.Fatalf("got %v", err)
	}
	for _, it := range items(t, s, id) {
		if it.Rec != nil {
			t.Fatalf("stored %s", it.Key)
		}
	}
	// A certain finding kept is refused too.
	g := recG(f)
	g.Findings[1] = rec.Account{Row: "dead", Did: "kept", How: "fine"}
	if _, err := s.Propose(ctx, id, []rec.Rec{g}); err == nil || !strings.Contains(err.Error(), "certain") {
		t.Fatalf("got %v", err)
	}
}

func ready(t *testing.T, s *Store) (int64, map[string]rec.File) {
	t.Helper()
	id, f := audit(t, s)
	if _, err := s.Propose(ctx, id, []rec.Rec{recG(f, f["m"].Key), recM(f, f["g"].Key), recQ(f)}); err != nil {
		t.Fatal(err)
	}
	return id, f
}

func decisions(t *testing.T, s *Store, id int64) map[string]string {
	t.Helper()
	out := map[string]string{}
	for k, it := range items(t, s, id) {
		if it.Decision != nil {
			out[k] = it.Decision.Action
		}
	}
	return out
}

// Linked files are decided together: accepting or rejecting one sets the
// other, and an edit to either keeps the link (the other is accepted, and
// a later accept keeps the edit).
func TestLinkedFilesAreDecidedTogether(t *testing.T) {
	s, _ := open(t)
	id, f := ready(t, s)
	g, m := f["g"].Key, f["m"].Key
	both := func() map[string]Seen { return seenOf(t, s, id, g, m) }

	if _, err := s.DecideFile(ctx, id, g, rec.Decision{Action: "accept", Note: "good"}, both()); err != nil {
		t.Fatal(err)
	}
	if d := decisions(t, s, id); d[g] != "accept" || d[m] != "accept" || len(d) != 2 {
		t.Fatalf("accept: %v", d)
	}
	if n := items(t, s, id)[m].Decision.Note; n != "" {
		t.Errorf("the note went to the other file too: %q", n)
	}
	if _, err := s.DecideFile(ctx, id, m, rec.Decision{Action: "reject"}, both()); err != nil {
		t.Fatal(err)
	}
	if d := decisions(t, s, id); d[g] != "reject" || d[m] != "reject" {
		t.Fatalf("reject: %v", d)
	}
	if _, err := s.DecideFile(ctx, id, m, rec.Decision{Action: "edit", Content: "# M\n\n- Mine.\n- Do x safely here.\n"}, both()); err != nil {
		t.Fatal(err)
	}
	if d := decisions(t, s, id); d[g] != "accept" || d[m] != "edit" {
		t.Fatalf("edit: %v", d)
	}
	if _, err := s.DecideFile(ctx, id, g, rec.Decision{Action: "accept"}, both()); err != nil {
		t.Fatal(err)
	}
	its := items(t, s, id)
	if its[m].Decision.Action != "edit" || !strings.Contains(its[m].Decision.Content, "Mine.") {
		t.Fatalf("an accept of the other side dropped the edit: %+v", its[m].Decision)
	}
	if _, err := s.UndecideFile(ctx, id, m, both()); err != nil {
		t.Fatal(err)
	}
	if d := decisions(t, s, id); len(d) != 0 {
		t.Fatalf("clear: %v", d)
	}
	// A file with no links is decided alone.
	q := f["q"].Key
	if _, err := s.DecideFile(ctx, id, q, rec.Decision{Action: "reject"}, seenOf(t, s, id, q)); err != nil {
		t.Fatal(err)
	}
	if d := decisions(t, s, id); len(d) != 1 || d[q] != "reject" {
		t.Fatalf("alone: %v", d)
	}
}

// Once a file is edited the page shows the edit, so accepting it approves
// the edit: the file keeps its edit (and its other side stays accepted).
// Going back to the recommendation is clearing the decision first.
func TestAcceptKeepsTheFilesOwnEdit(t *testing.T) {
	s, _ := open(t)
	id, f := ready(t, s)
	g, m := f["g"].Key, f["m"].Key
	both := func() map[string]Seen { return seenOf(t, s, id, g, m) }
	mine := "# M\n\n- Mine.\n- Do x safely here.\n"
	if _, err := s.DecideFile(ctx, id, m, rec.Decision{Action: "edit", Content: mine}, both()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideFile(ctx, id, m, rec.Decision{Action: "accept", Note: "looks right"}, both()); err != nil {
		t.Fatal(err)
	}
	its := items(t, s, id)
	if d := its[m].Decision; d.Action != "edit" || d.Content != mine || d.Note != "looks right" {
		t.Fatalf("accept after an edit: %+v", d)
	}
	if d := its[g].Decision; d.Action != "accept" {
		t.Fatalf("the other side: %+v", d)
	}
	if _, err := s.UndecideFile(ctx, id, m, both()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideFile(ctx, id, m, rec.Decision{Action: "accept"}, both()); err != nil {
		t.Fatal(err)
	}
	if d := items(t, s, id)[m].Decision; d.Action != "accept" || d.Content != "" {
		t.Fatalf("accept after a revert: %+v", d)
	}
}

// A file's print covers what the page shows of it: the edit in force, if
// there is one, else the recommendation. A page that still shows an edit
// another client has cleared cannot accept it as the recommendation; once
// it shows the recommendation, it can.
func TestAcceptAnswersTheContentThePageShowed(t *testing.T) {
	s, _ := open(t)
	id, f := ready(t, s)
	g, m := f["g"].Key, f["m"].Key
	both := func() map[string]Seen { return seenOf(t, s, id, g, m) }
	if _, err := s.DecideFile(ctx, id, m, rec.Decision{Action: "edit", Content: "# M\n\n- Mine.\n- Do x safely here.\n"}, both()); err != nil {
		t.Fatal(err)
	}
	shown := both()
	if _, err := s.UndecideFile(ctx, id, m, both()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideFile(ctx, id, m, rec.Decision{Action: "accept"}, shown); !errors.Is(err, ErrChanged) {
		t.Fatalf("an accept of an edit no longer in force: %v", err)
	}
	if _, err := s.DecideFile(ctx, id, g, rec.Decision{Action: "accept"}, shown); !errors.Is(err, ErrChanged) {
		t.Fatalf("an accept of the other side, which keeps the edit: %v", err)
	}
	if d := decisions(t, s, id); len(d) != 0 {
		t.Fatalf("approved %v", d)
	}
	if _, err := s.DecideFile(ctx, id, m, rec.Decision{Action: "accept"}, both()); err != nil {
		t.Fatal(err)
	}
	if d := items(t, s, id)[m].Decision; d.Action != "accept" || d.Content != "" {
		t.Fatalf("the recommendation shown: %+v", d)
	}
}

// A decision carries the print of every file in its group as the page
// showed it; a missing or old one is refused and nothing is stored.
func TestDecideNeedsEveryPrint(t *testing.T) {
	s, _ := open(t)
	id, f := ready(t, s)
	g, m := f["g"].Key, f["m"].Key
	ps := seenOf(t, s, id, g, m)
	only := map[string]Seen{g: ps[g]}
	if _, err := s.DecideFile(ctx, id, g, rec.Decision{Action: "accept"}, only); !errors.Is(err, ErrChanged) {
		t.Fatalf("a missing print: %v", err)
	}
	old := map[string]Seen{g: ps[g], m: {Fingerprint: "0000000000000000"}}
	if _, err := s.DecideFile(ctx, id, g, rec.Decision{Action: "accept"}, old); !errors.Is(err, ErrChanged) {
		t.Fatalf("an old print: %v", err)
	}
	if d := decisions(t, s, id); len(d) != 0 {
		t.Fatalf("stored %v", d)
	}
	if _, err := s.DecideFile(ctx, id, "nope", rec.Decision{Action: "accept"}, ps); !errors.Is(err, ErrStale) {
		t.Fatalf("no such file: %v", err)
	}
}

// A new recommendation for a file drops the decisions on its group, sent
// or not, and changes the prints the page holds.
func TestReproposingDropsTheGroupsDecisions(t *testing.T) {
	s, _ := open(t)
	id, f := ready(t, s)
	g, m := f["g"].Key, f["m"].Key
	old := seenOf(t, s, id, g, m)
	if _, err := s.DecideFile(ctx, id, g, rec.Decision{Action: "accept"}, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, id, "", nil); err != nil {
		t.Fatal(err)
	}
	m2 := recM(f, g)
	m2.Content += "- More.\n"
	res, err := s.Propose(ctx, id, []rec.Rec{m2})
	if err != nil || res.Cleared != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if d := decisions(t, s, id); len(d) != 0 {
		t.Fatalf("kept %v", d)
	}
	if _, err := s.DecideFile(ctx, id, g, rec.Decision{Action: "accept"}, old); !errors.Is(err, ErrChanged) {
		t.Fatalf("an old page: %v", err)
	}
}

// A file apply has written, and every file linked to one, keeps its
// recommendation and decision: a new recommendation is refused. Others in
// the round can still be re-proposed.
func TestProposeRefusesAnAppliedFileOrGroup(t *testing.T) {
	s, _ := open(t)
	id, f := ready(t, s)
	g, m := f["g"].Key, f["m"].Key
	if _, err := s.DecideFile(ctx, id, g, rec.Decision{Action: "accept"}, seenOf(t, s, id, g, m)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, id, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordApply(ctx, Apply{Round: id, Repo: "/w/m", State: "branch", Branch: "sift/round-1", Rows: []string{m}}); err != nil {
		t.Fatal(err)
	}
	m2 := recM(f, g)
	m2.Content += "- More.\n"
	if _, err := s.Propose(ctx, id, []rec.Rec{m2}); err == nil || !strings.Contains(err.Error(), "applied") {
		t.Fatalf("the applied file: %v", err)
	}
	g2 := recG(f, m)
	g2.Content += "- More.\n"
	if _, err := s.Propose(ctx, id, []rec.Rec{g2}); err == nil || !strings.Contains(err.Error(), "applied") {
		t.Fatalf("a file linked to it: %v", err)
	}
	if d := decisions(t, s, id); d[g] != "accept" || d[m] != "accept" {
		t.Fatalf("decisions %v", d)
	}
	q2 := recQ(f)
	q2.Content += "- More.\n"
	if _, err := s.Propose(ctx, id, []rec.Rec{q2}); err != nil {
		t.Fatalf("a file apply did not write: %v", err)
	}
}

// Nothing is decided while the agent is still recommending.
func TestNoDecisionBeforeReady(t *testing.T) {
	s, _ := open(t)
	id, f := audit(t, s)
	if _, err := s.Propose(ctx, id, []rec.Rec{recQ(f)}); err != nil {
		t.Fatal(err)
	}
	q := f["q"].Key
	if _, err := s.DecideFile(ctx, id, q, rec.Decision{Action: "accept"}, seenOf(t, s, id, q)); !errors.Is(err, ErrNotReady) {
		t.Fatalf("got %v", err)
	}
}

// A backlog round is decided per item: no file recommendations.
func TestABacklogRoundTakesNoRecommendations(t *testing.T) {
	s, _ := open(t)
	id, err := s.RecordRound(ctx, Round{Kind: "backlog"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Propose(ctx, id, []rec.Rec{{File: "x"}}); err == nil || !strings.Contains(err.Error(), "per item") {
		t.Fatalf("got %v", err)
	}
	if _, err := s.AddRows(ctx, id, []row.Row{{ID: "i1", Check: "intake", Verdict: "issue", Source: row.Source{Entry: "1"}}}); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.State(ctx, id); st.State != Ready || st.Files != 1 || st.Recommended != 1 {
		t.Fatalf("%+v", st)
	}
}

// A backlog round is recommending until every item has the agent's verdict
// (ask counts: it is the agent's answer that the user must say), and no
// item is decided before then.
func TestABacklogRoundWaitsForEveryItem(t *testing.T) {
	s, _ := open(t)
	id, err := s.RecordRound(ctx, Round{Kind: "backlog"}, []row.Row{
		{ID: "i1", Check: "intake", Verdict: "issue", Title: "t", Text: "b", Destination: "acme/app", Source: row.Source{Entry: "1"}},
		{ID: "i2", Check: "intake", Source: row.Source{Entry: "2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := s.State(ctx, id); st.State != Recommending || st.Files != 2 || st.Recommended != 1 {
		t.Fatalf("%+v", st)
	}
	_, rows, err := s.Round(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Answer(ctx, id, []Answer{{Row: "i1", Fingerprint: rows[0].Fingerprint, Decision: row.Decision{Action: "accept"}}}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("answer while recommending: %v", err)
	}
	if err := s.Decide(ctx, id, "i1", row.Decision{Action: "accept"}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("decide while recommending: %v", err)
	}
	if _, err := s.AddRows(ctx, id, []row.Row{{ID: "i2", Verdict: "ask", Text: "which repo?"}}); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.State(ctx, id); st.State != Ready || st.Recommended != 2 {
		t.Fatalf("%+v", st)
	}
	if err := s.Decide(ctx, id, "i1", row.Decision{Action: "accept"}); err != nil {
		t.Fatal(err)
	}
}

// Send carries the file decisions, with their notes; the round is then
// sent, and applied once a repo's branch is written.
func TestSendCarriesFileDecisions(t *testing.T) {
	s, _ := open(t)
	id, f := ready(t, s)
	g, m, q := f["g"].Key, f["m"].Key, f["q"].Key
	if _, err := s.DecideFile(ctx, id, g, rec.Decision{Action: "accept", Note: "nice"}, seenOf(t, s, id, g, m)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideFile(ctx, id, q, rec.Decision{Action: "reject"}, seenOf(t, s, id, q)); err != nil {
		t.Fatal(err)
	}
	sd, err := s.Send(ctx, id, "", nil)
	if err != nil || sd.Counts.Accept != 2 || sd.Counts.Reject != 1 || len(sd.Notes) != 1 || !strings.Contains(sd.Notes[0].Row, "/h/AGENTS.md") {
		t.Fatalf("%+v %v", sd, err)
	}
	for _, it := range items(t, s, id) {
		if !it.Decision.Sent {
			t.Errorf("%s not sent", it.Key)
		}
	}
	if st, _ := s.State(ctx, id); st.State != Sent {
		t.Fatalf("%+v", st)
	}
	if err := s.RecordApply(ctx, Apply{Round: id, Repo: "/w/m", State: "branch", Branch: "sift/round-1"}); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.State(ctx, id); st.State != Applied {
		t.Fatalf("%+v", st)
	}
}

// A clear answers the content the page showed, as a decision does. A page
// still showing an edit on the old recommendation, after another client
// replaced it, is refused, and gets no snapshot to show in its place; once
// it shows the new recommendation, accepting it approves that.
func TestAStaleClearIsRefused(t *testing.T) {
	s, _ := open(t)
	id, f := ready(t, s)
	g, m := f["g"].Key, f["m"].Key
	both := func() map[string]Seen { return seenOf(t, s, id, g, m) }
	if _, err := s.DecideFile(ctx, id, m, rec.Decision{Action: "edit", Content: "# M\n\n- Mine.\n- Do x safely here.\n"}, both()); err != nil {
		t.Fatal(err)
	}
	shown := both()
	m2 := recM(f, g)
	m2.Content += "- More.\n"
	if _, err := s.Propose(ctx, id, []rec.Rec{m2}); err != nil {
		t.Fatal(err)
	}
	after, err := s.UndecideFile(ctx, id, m, shown)
	if !errors.Is(err, ErrChanged) || after != nil {
		t.Fatalf("a stale clear: %v, returned %+v", err, after)
	}
	its := items(t, s, id)
	if _, err := s.DecideFile(ctx, id, m, rec.Decision{Action: "accept"}, shown); !errors.Is(err, ErrChanged) {
		t.Fatalf("an accept with the old prints: %v", err)
	}
	// The page reloads and shows the new recommendation.
	got, err := s.DecideFile(ctx, id, m, rec.Decision{Action: "accept"}, map[string]Seen{g: {Fingerprint: its[g].Fingerprint}, m: {Fingerprint: its[m].Fingerprint}})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range got {
		if it.Key == m && (it.Decision == nil || it.Decision.Action != "accept" || it.Rec.Content != m2.Content) {
			t.Fatalf("the snapshot after the accept: %+v %+v", it.Decision, it.Rec)
		}
	}
	if len(got) != 2 {
		t.Fatalf("the snapshot holds the group: %d files", len(got))
	}
	if d := items(t, s, id)[m].Decision; d.Action != "accept" || d.Content != "" {
		t.Fatalf("approved %+v", d)
	}
}

// A file's note changes the note alone: the decision, its content and the
// prints stay as they are, and it needs a decision to sit on.
func TestAFileNoteChangesOnlyTheNote(t *testing.T) {
	s, _ := open(t)
	id, f := ready(t, s)
	g, m := f["g"].Key, f["m"].Key
	if err := s.NoteFile(ctx, id, m, "why"); !errors.Is(err, ErrChanged) {
		t.Fatalf("a note on an undecided file: %v", err)
	}
	mine := "# M\n\n- Mine.\n- Do x safely here.\n"
	if _, err := s.DecideFile(ctx, id, m, rec.Decision{Action: "edit", Content: mine}, seenOf(t, s, id, g, m)); err != nil {
		t.Fatal(err)
	}
	before := seenOf(t, s, id, g, m)
	if err := s.NoteFile(ctx, id, m, "why"); err != nil {
		t.Fatal(err)
	}
	if d := items(t, s, id)[m].Decision; d.Action != "edit" || d.Content != mine || d.Note != "why" {
		t.Fatalf("after the note: %+v", d)
	}
	if after := seenOf(t, s, id, g, m); after[g] != before[g] || after[m] != before[m] {
		t.Fatalf("a note changed the prints: %v -> %v", before, after)
	}
}

// A Send covers the decisions the page showed when Send was pressed, and
// names the ones it sent. A decision made since, by another page and
// stored before the Send, is not sent: on a file, or on a row. Nor is one
// whose print changed under it: g's accept stays, but g's print covers
// m's edit, so g is not as the page showed it either.
func TestASendSendsOnlyWhatThePageShowed(t *testing.T) {
	s, _ := open(t)
	id, f := ready(t, s)
	g, m, q := f["g"].Key, f["m"].Key, f["q"].Key
	if _, err := s.DecideFile(ctx, id, g, rec.Decision{Action: "accept"}, seenOf(t, s, id, g, m)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideFile(ctx, id, q, rec.Decision{Action: "reject"}, seenOf(t, s, id, q)); err != nil {
		t.Fatal(err)
	}
	// The page shows g, m accepted and q rejected, and presses Send.
	shown := shownNow(t, s, id)
	// Another page edits m before the Send is stored.
	if _, err := s.DecideFile(ctx, id, m, rec.Decision{Action: "edit", Content: "# mine\n"}, seenOf(t, s, id, g, m)); err != nil {
		t.Fatal(err)
	}
	sd, err := s.Send(ctx, id, "", shown)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{q}; !slices.Equal(sd.Files, want) || sd.Counts != (Counts{Reject: 1}) {
		t.Fatalf("sent %v %+v, want %v", sd.Files, sd.Counts, want)
	}
	for k, it := range items(t, s, id) {
		if it.Decision.Sent != (k == q) {
			t.Errorf("%s: sent %v", k, it.Decision.Sent)
		}
	}
	// Nothing the page showed is left: a second Send of the same shows
	// sends nothing, and records nothing.
	if again, err := s.Send(ctx, id, "", shown); err != nil || again.ID != 0 || len(again.Files) != 0 {
		t.Fatalf("again: %+v %v", again, err)
	}
}

// The row side of the same.
func TestASendSendsOnlyTheRowsThePageShowed(t *testing.T) {
	s, _ := open(t)
	id, rows := round(t, s)
	a, b := rows[0].ID, rows[1].ID
	p := prints(t, s, id)
	if _, err := s.Answer(ctx, id, []Answer{
		{Row: a, Fingerprint: p[a], Decision: row.Decision{Action: "accept"}},
		{Row: b, Fingerprint: p[b], Decision: row.Decision{Action: "reject", Note: "no"}},
	}); err != nil {
		t.Fatal(err)
	}
	shown := shownNow(t, s, id)
	// Another page edits a before the Send is stored.
	if _, err := s.Answer(ctx, id, []Answer{{Row: a, Fingerprint: prints(t, s, id)[a], DecisionID: ids(t, s, id)[a], Decision: row.Decision{Action: "edit", Text: "mine"}}}); err != nil {
		t.Fatal(err)
	}
	sd, err := s.Send(ctx, id, "", shown)
	if err != nil || !slices.Equal(sd.Rows, []string{b}) || sd.Counts != (Counts{Reject: 1}) || len(sd.Notes) != 1 {
		t.Fatalf("%+v %v", sd, err)
	}
	_, rs, _ := s.Round(ctx, id)
	if rs[0].Decision.Sent || !rs[1].Decision.Sent {
		t.Fatalf("sent flags: %+v %+v", rs[0].Decision, rs[1].Decision)
	}
}
