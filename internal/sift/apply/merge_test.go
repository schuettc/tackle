package apply

import (
	"database/sql"
	"testing"

	"github.com/schuettc/tackle/internal/sift/row"
)

// A sent merge into row B, then the agent changes B's path or passage: the
// approval was for the old target, so apply skips the merge until it is
// approved again and sent.
func TestApplySkipsAMergeWhoseTargetChanged(t *testing.T) {
	for name, change := range map[string]func(b *row.Row){
		"passage": func(b *row.Row) { b.Passage, b.Source.Start, b.Source.End = "- Never force-push.", 6, 6 },
		"path": func(b *row.Row) {
			b.Source.Path, b.Source.File, b.Passage, b.Source.Start, b.Source.End = "docs/other.md", otherFile(b), "- one", 5, 5
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := newRig(t)
			g.record(g.at("dup2", "duplicate", 16, "- Keep the changelog up to date."))
			b := g.at("mem", "intake", 15, "- Keep the changelog current.")
			if _, err := g.s.AddRows(ctx, g.round, []row.Row{b, {ID: "dup2", Verdict: "merge:mem", Text: "- Keep the changelog current with every change."}}); err != nil {
				t.Fatal(err)
			}
			g.decide("dup2", row.Decision{Action: "accept"})
			if _, err := g.s.Send(ctx, g.round, ""); err != nil {
				t.Fatal(err)
			}
			change(&b)
			if _, err := g.s.AddRows(ctx, g.round, []row.Row{b}); err != nil {
				t.Fatal(err)
			}
			if res := g.runUnsent(Options{DryRun: true}); len(res.Repos) != 0 {
				t.Fatalf("applied a merge into a changed target: %+v", res.Repos)
			}
			g.decide("dup2", row.Decision{Action: "accept"})
			if res := g.runUnsent(Options{DryRun: true}); len(res.Repos) != 0 || res.Unsent != 1 {
				t.Fatalf("applied before the send: %+v", res)
			}
			if r := g.run(Options{DryRun: true}).Repos[0]; len(r.Applied) != 1 || r.Applied[0].Row != "dup2" {
				t.Fatalf("not applied once sent: %+v", r)
			}
		})
	}
}

// otherFile is b's file moved to docs/other.md in the same repo.
func otherFile(b *row.Row) string { return b.Source.Repo + "/docs/other.md" }

// A merge into a row the round lacks can't be approved; and an approval
// that predates its target (seeded in the store, sent) is dropped when the
// target is added, so apply skips the merge.
func TestApplySkipsAMergeApprovedBeforeItsTargetWasAdded(t *testing.T) {
	g := newRig(t)
	g.record(g.at("dup2", "duplicate", 16, "- Keep the changelog up to date."))
	if _, err := g.s.AddRows(ctx, g.round, []row.Row{{ID: "dup2", Verdict: "merge:mem", Text: "- Keep the changelog current with every change."}}); err != nil {
		t.Fatal(err)
	}
	if err := g.s.Decide(ctx, g.round, "dup2", row.Decision{Action: "accept"}); err == nil {
		t.Fatal("accepted a merge into a row the round lacks")
	}
	db, err := sql.Open("sqlite", g.db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `INSERT INTO decisions(round_id, row_id, action, decided_at, sent_at) VALUES (?, 'dup2', 'accept', 1, 1)`, g.round); err != nil {
		t.Fatal(err)
	}
	if _, err := g.s.AddRows(ctx, g.round, []row.Row{g.at("mem", "intake", 15, "- Keep the changelog current.")}); err != nil {
		t.Fatal(err)
	}
	if res := g.runUnsent(Options{DryRun: true}); len(res.Repos) != 0 {
		t.Fatalf("applied a merge approved before its target was added: %+v", res.Repos)
	}
}
