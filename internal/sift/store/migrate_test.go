package store

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tools-common/sqlitedb"
)

// A populated v1 database (rounds, rows, decisions, mutes, as PR 1 left
// them) upgrades in place: its data is kept and the later columns take
// their defaults.
func TestMigrateAPopulatedV1Database(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sift.db")
	v1, err := sqlitedb.Open(ctx, p, sqlitedb.Options{Migrations: Migrations[:1]})
	if err != nil {
		t.Fatal(err)
	}
	certain := finding("/w/b/AGENTS.md", "dead-path", "see `gone.md`")
	certain.Certain = true
	judged := finding("/w/a/CLAUDE.md", "stale-status", "- Never push.")
	for _, stmt := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO rounds(id, kind, at, summary) VALUES (1, 'on-demand', 1790000000000, '{"stale-status":1,"dead-path":1}')`, nil},
		{`INSERT INTO rows(round_id, seq, row_id, check_, body) VALUES (1, 0, ?, ?, ?)`, []any{judged.ID, judged.Check, mustJSON(t, judged)}},
		{`INSERT INTO rows(round_id, seq, row_id, check_, body) VALUES (1, 1, ?, ?, ?)`, []any{certain.ID, certain.Check, mustJSON(t, certain)}},
		{`INSERT INTO decisions(round_id, row_id, action, verdict, title, text, note, decided_at) VALUES (1, ?, 'edit', 'rewrite', '', '- Push to a branch.', 'shorter', 1790000000001)`, []any{judged.ID}},
		{`INSERT INTO decisions(round_id, row_id, action, note, decided_at) VALUES (1, ?, 'reject', 'keep the hint', 1790000000002)`, []any{certain.ID}},
		{`INSERT INTO mutes(row_id, muted_at) VALUES ('muted-row', 1790000000003)`, nil},
	} {
		if _, err := v1.ExecContext(ctx, stmt.q, stmt.args...); err != nil {
			t.Fatalf("%s: %v", stmt.q, err)
		}
	}
	if v, _ := v1.Version(ctx); v != 1 {
		t.Fatalf("fixture version %d", v)
	}
	_ = v1.Close()

	s, err := Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if v, err := s.db.Version(ctx); err != nil || v != len(Migrations) {
		t.Fatalf("version %d %v", v, err)
	}
	r, rows, err := s.LatestRound(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != 1 || r.Kind != "on-demand" || r.Summary["dead-path"] != 1 || r.Rev != 0 || r.OwnerSession != "" || r.OwnerLabel != "" {
		t.Errorf("round %+v", r)
	}
	if len(rows) != 2 || rows[0].ID != judged.ID || rows[0].Passage != "- Never push." || !rows[1].Certain {
		t.Fatalf("rows %+v", rows)
	}
	if d := rows[0].Decision; d == nil || d.Action != "edit" || d.Verdict != "rewrite" || d.Text != "- Push to a branch." || d.Note != "shorter" || d.Sent || len(d.Cleared) != 0 {
		t.Errorf("decision %+v", d)
	}
	if d := rows[1].Decision; d == nil || d.Action != "reject" || d.Note != "keep the hint" || d.Sent {
		t.Errorf("undo %+v", d)
	}
	// Each stored decision got an id of its own.
	if a, b := rows[0].Decision.ID, rows[1].Decision.ID; len(a) != 16 || len(b) != 16 || a == b {
		t.Errorf("decision ids %q %q", a, b)
	}
	if kept, n, err := s.Unmuted(ctx, []row.Row{{ID: "muted-row"}}); err != nil || len(kept) != 0 || n != 1 {
		t.Errorf("mute lost: %d %d %v", len(kept), n, err)
	}
	if n, err := s.Sends(ctx, 1); err != nil || n != 0 {
		t.Errorf("sends %d %v", n, err)
	}
	if as, err := s.Applies(ctx, 1); err != nil || len(as) != 0 {
		t.Errorf("applies %+v %v", as, err)
	}
	// The upgraded database works: the old decisions go with the first Send.
	sd, err := s.Send(ctx, 1, "", nil)
	if err != nil || sd.Counts != (Counts{Edit: 1, Reject: 1}) {
		t.Fatalf("send %+v %v", sd, err)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The branches a v5 database records as applied are what sift created, so
// v6 records them for sift clean: pushed when a pull request was opened or
// the detail says the branch was pushed; held and failed applies made
// nothing.
func TestMigrateRecordsTheAppliedBranchesAsCreated(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sift.db")
	v5, err := sqlitedb.Open(ctx, p, sqlitedb.Options{Migrations: Migrations[:5]})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO applies(round_id, repo, base, branch, pr, state, detail, at) VALUES (1, '/r/a', 'origin/main', 'sift/round-1', 'https://github.com/o/a/pull/1', 'pr', '', 1)`,
		`INSERT INTO applies(round_id, repo, base, branch, pr, state, detail, at) VALUES (1, '/r/b', 'origin/main', 'sift/round-1', '', 'branch', 'no gh: the branch is committed and left local', 2)`,
		`INSERT INTO applies(round_id, repo, base, branch, pr, state, detail, at) VALUES (1, '/r/c', 'origin/main', 'sift/round-1', '', 'branch', 'the branch is pushed but gh pr create failed: x', 3)`,
		`INSERT INTO applies(round_id, repo, base, branch, pr, state, detail, at) VALUES (1, '/r/d', 'origin/main', '', '', 'held', 'uncommitted changes', 4)`,
	} {
		if _, err := v5.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	_ = v5.Close()
	s, err := Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	made, err := s.Created(ctx)
	if err != nil || len(made) != 3 {
		t.Fatalf("%+v %v", made, err)
	}
	for i, want := range []struct {
		repo   string
		pushed bool
		pr     string
	}{{"/r/a", true, "https://github.com/o/a/pull/1"}, {"/r/b", false, ""}, {"/r/c", true, ""}} {
		if c := made[i]; c.Kind != "branch" || c.Repo != want.repo || c.Name != "sift/round-1" || c.Pushed != want.pushed || c.PR != want.pr {
			t.Errorf("%d: %+v", i, c)
		}
	}
}
