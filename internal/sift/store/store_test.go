package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/sift/row"
)

var ctx = context.Background()

func open(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sift.db")
	s, err := Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, p
}

func finding(file, check, passage string) row.Row {
	return row.Row{ID: row.ID(file, check, passage), Check: check, Source: row.Source{File: file, Start: 1, End: 1}, Passage: passage}
}

func TestOpenMigratesAndIsPrivate(t *testing.T) {
	s, p := open(t)
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	if v, err := s.db.Version(ctx); err != nil || v != len(Migrations) {
		t.Errorf("version %d %v", v, err)
	}
	// Reopening an existing database is a no-op migration.
	_ = s.Close()
	s2, err := Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	_ = s2.Close()
}

func TestRoundRoundTrip(t *testing.T) {
	s, _ := open(t)
	if _, _, err := s.LatestRound(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("want ErrNoRows, got %v", err)
	}
	rows := []row.Row{finding("/a", "size", "x"), finding("/b", "secret", "token = abc")}
	rows[1].Certain = true
	at := time.UnixMilli(1_790_000_000_000)
	id, err := s.RecordRound(ctx, Round{Kind: "on-demand", At: at, Summary: map[string]int{"size": 1, "secret": 1}}, rows)
	if err != nil {
		t.Fatal(err)
	}
	r, got, err := s.LatestRound(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != id || r.Kind != "on-demand" || !r.At.Equal(at) || r.Summary["size"] != 1 {
		t.Errorf("round %+v", r)
	}
	if len(got) != 2 || got[0].ID != rows[0].ID || !got[1].Certain {
		t.Errorf("rows %+v", got)
	}
}

func TestDecideNeedsTheRowInTheRound(t *testing.T) {
	s, _ := open(t)
	r := finding("/a", "size", "x")
	id, _ := s.RecordRound(ctx, Round{Kind: "on-demand"}, []row.Row{r})
	if err := s.Decide(ctx, id, "nope", row.Decision{Action: "accept"}); !errors.Is(err, ErrStale) {
		t.Fatalf("err %v", err)
	}
	if err := s.Decide(ctx, id, r.ID, row.Decision{Action: "edit", Verdict: "bogus"}); err == nil {
		t.Fatal("an invalid verdict was stored")
	}
	if err := s.Decide(ctx, id, r.ID, row.Decision{Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	// A later answer replaces the earlier one.
	want := row.Decision{Action: "edit", Verdict: "close:tracked:o/r#4", Title: "t", Text: "x", Note: "gone"}
	if err := s.Decide(ctx, id, r.ID, want); err != nil {
		t.Fatal(err)
	}
	_, got, _ := s.LatestRound(ctx)
	if got[0].Decision == nil || *got[0].Decision != want {
		t.Fatalf("decision %+v", got[0].Decision)
	}
}

// "keep, stop flagging" mutes a row by id: it holds in later rounds, and
// lapses when the passage changes, because the id hashes the passage.
func TestMuteSurvivesARoundAndLapsesWhenThePassageChanges(t *testing.T) {
	s, _ := open(t)
	r := finding("/a", "negative-rule", "- Never push to main.")
	if _, err := s.RecordRound(ctx, Round{Kind: "on-demand"}, []row.Row{r}); err != nil {
		t.Fatal(err)
	}
	if err := s.Mute(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordRound(ctx, Round{Kind: "weekly"}, []row.Row{r}); err != nil {
		t.Fatal(err)
	}
	kept, muted, err := s.Unmuted(ctx, []row.Row{r})
	if err != nil || len(kept) != 0 || muted != 1 {
		t.Fatalf("same passage: kept %d muted %d %v", len(kept), muted, err)
	}
	changed := finding("/a", "negative-rule", "- Never push to main without a review.")
	kept, muted, err = s.Unmuted(ctx, []row.Row{changed})
	if err != nil || len(kept) != 1 || muted != 0 {
		t.Fatalf("changed passage: kept %d muted %d %v", len(kept), muted, err)
	}
	// Whitespace alone is not a change.
	reflowed := finding("/a", "negative-rule", "-  Never push\nto main.")
	if kept, _, _ := s.Unmuted(ctx, []row.Row{reflowed}); len(kept) != 0 {
		t.Fatal("a reflowed passage lost its mute")
	}
}

func TestOldRoundsArePruned(t *testing.T) {
	s, _ := open(t)
	var first int64
	for i := 0; i < keepRounds+2; i++ {
		id, err := s.RecordRound(ctx, Round{Kind: "on-demand"}, []row.Row{finding("/a", "size", "x")})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = id
		}
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM rounds`).Scan(&n); err != nil || n != keepRounds {
		t.Fatalf("rounds %d %v", n, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM rows WHERE round_id = ?`, first).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows of a pruned round %d %v", n, err)
	}
}

// A round that cannot be recorded is not recorded at all, and its id is 0.
func TestRecordRoundRollsBack(t *testing.T) {
	s, _ := open(t)
	r := finding("/a", "size", "x")
	id, err := s.RecordRound(ctx, Round{Kind: "on-demand"}, []row.Row{r, r})
	if err == nil || id != 0 {
		t.Fatalf("id %d err %v", id, err)
	}
	if _, _, err := s.LatestRound(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("a rolled-back round is stored: %v", err)
	}
}
