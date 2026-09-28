package propose

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/db"
)

var ctx = context.Background()

func newStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "casebook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	s := New(d)
	s.Now = func() time.Time { return now }
	d.Exec("INSERT INTO sessions(id, first_seen, last_seen) VALUES ('s1', 1, 1)")
	return s, &now
}

func TestProposeValidatesAndSupersedes(t *testing.T) {
	s, _ := newStore(t)
	ps, errs := s.Propose(ctx, "pi:s1", []string{"pr:A/B#1", "repo:a/b", "nonsense"}, "close", "", "stale")
	if len(ps) != 1 || ps[0].Key != "pr:a/b#1" || len(errs) != 2 {
		t.Fatalf("proposals %+v errs %v (repo can't be closed; bad key)", ps, errs)
	}
	again, _ := s.Propose(ctx, "pi:s1", []string{"pr:a/b#1"}, "keep", "", "")
	if old, _ := s.Get(ctx, ps[0].ID); old.State != Superseded {
		t.Fatalf("old %s", old.State)
	}
	pending, _ := s.Pending(ctx)
	if len(pending) != 1 || pending["pr:a/b#1"].ID != again[0].ID || pending["pr:a/b#1"].Disposition != "keep" {
		t.Fatalf("pending %+v", pending)
	}
	if _, errs := s.Propose(ctx, "pi:s1", []string{"pr:a/b#2"}, "wait", "", ""); len(errs) != 1 {
		t.Fatal("wait without until accepted")
	}
}

func TestSettleAndTally(t *testing.T) {
	s, now := newStore(t)
	ps, _ := s.Propose(ctx, "pi:s1", []string{"pr:a/b#1", "pr:a/b#2", "pr:a/b#3", "pr:a/b#4"}, "close", "", "")
	since := *now
	*now = now.Add(time.Minute)
	s.Settle(ctx, ps[0].ID, Accepted, "")
	s.Settle(ctx, ps[1].ID, Changed, "decided keep")
	*now = now.Add(time.Minute)
	s.Settle(ctx, ps[2].ID, Rejected, "still in use")
	if err := s.Settle(ctx, ps[0].ID, Rejected, ""); err != ErrNotFound {
		t.Fatalf("settling twice: %v", err)
	}
	tal, _ := s.Tally(ctx, "pi:s1", since)
	if tal != (Tally{Accepted: 1, Changed: 1, Rejected: 1, Pending: 1}) {
		t.Fatalf("tally %+v", tal)
	}
	if got, _ := s.Get(ctx, ps[2].ID); got.Reason != "still in use" {
		t.Fatalf("reason %q", got.Reason)
	}
	over, total, err := s.Overruled(ctx, "pi:s1", since, 1)
	if err != nil || total != 2 || len(over) != 1 || over[0].ID != ps[2].ID {
		t.Fatalf("overruled %+v %d %v (want the newest of 2)", over, total, err)
	}
	if over, total, _ := s.Overruled(ctx, "pi:s1", *now, 10); total != 1 || over[0].Reason != "still in use" {
		t.Fatalf("overruled since the rejection %+v %d", over, total)
	}
	s.SupersedeKey(ctx, "pr:a/b#4", 0)
	if p, _ := s.Pending(ctx); len(p) != 0 {
		t.Fatalf("pending after supersede %+v", p)
	}
}

func TestEvidenceAndProgress(t *testing.T) {
	s, now := newStore(t)
	if _, err := s.AddEvidence(ctx, "issue:Schuettc/Muster#112", "race in nudge.go:88", "pi:s1"); err != nil {
		t.Fatal(err)
	}
	ev, _ := s.Evidence(ctx, "issue:schuettc/muster#112")
	if len(ev) != 1 || ev[0].Author != "pi:s1" {
		t.Fatalf("evidence %+v", ev)
	}
	s.SetProgress(ctx, "s1", "checking CI on #671", 2, 4)
	*now = now.Add(3 * time.Minute)
	p, _ := s.SetProgress(ctx, "s1", "checking CI on #672", 3, 4)
	if p.N != 3 || p.UpdatedAt.Sub(p.StartedAt) != 3*time.Minute {
		t.Fatalf("progress %+v", p)
	}
	if d, _ := s.ClearProgress(ctx, "s1"); d != 3*time.Minute {
		t.Fatalf("worked for %v", d)
	}
	if _, ok, _ := s.Progress(ctx, "s1"); ok {
		t.Fatal("progress not cleared")
	}
}
