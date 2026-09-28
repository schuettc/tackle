package propose

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/db"
)

var ctx = context.Background()

func newStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "casebook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	s := New(d)
	s.Now = func() time.Time { return now }
	_, _ = d.Exec("INSERT INTO sessions(id, first_seen, last_seen) VALUES ('s1', 1, 1)")
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
	_ = s.Settle(ctx, ps[0].ID, Accepted, "")
	_ = s.Settle(ctx, ps[1].ID, Changed, "decided keep")
	*now = now.Add(time.Minute)
	_ = s.Settle(ctx, ps[2].ID, Rejected, "still in use")
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
	_ = s.SupersedeKey(ctx, "pr:a/b#4", 0)
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
	_, _ = s.SetProgress(ctx, "s1", "checking CI on #671", 2, 4)
	*now = now.Add(3 * time.Minute)
	p, _ := s.SetProgress(ctx, "s1", "checking CI on #672", 3, 4)
	if p.N != 3 || p.UpdatedAt.Sub(p.StartedAt) != 3*time.Minute {
		t.Fatalf("progress %+v", p)
	}
	d, lines, err := s.ClearProgress(ctx, "s1")
	if err != nil {
		t.Fatalf("ClearProgress: %v", err)
	}
	if d != 3*time.Minute {
		t.Fatalf("worked for %v", d)
	}
	if len(lines) != 2 {
		t.Fatalf("progress lines: got %d, want 2", len(lines))
	}
	if lines[0].Text != "checking CI on #671" || lines[1].Text != "checking CI on #672" {
		t.Fatalf("progress lines: %+v", lines)
	}
	// Each line must carry a non-zero timestamp (item 6).
	for i, l := range lines {
		if l.At.IsZero() {
			t.Errorf("lines[%d].At is zero (want a real timestamp)", i)
		}
	}
	if _, ok, _ := s.Progress(ctx, "s1"); ok {
		t.Fatal("progress not cleared")
	}
}

// TestClearProgressDurationToSettlement verifies that the worked-for duration
// is measured from the turn's first progress update (started_at) to the moment
// the turn ends (s.Now()), not to the last progress update (updated_at).
//
// One progress call at t0; ClearProgress at t0+10m → expect 10m.
// The old code computed updated_at−started_at = 0 when there is only one
// progress call (updated_at == started_at).
func TestClearProgressDurationToSettlement(t *testing.T) {
	s, now := newStore(t)
	// Single progress call at t0: StartedAt = UpdatedAt = t0.
	if _, err := s.SetProgress(ctx, "s1", "start", 0, 0); err != nil {
		t.Fatal(err)
	}
	// Advance the clock by 10 minutes — this is when the turn ends (settled).
	*now = now.Add(10 * time.Minute)
	d, lines, err := s.ClearProgress(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if d != 10*time.Minute {
		t.Fatalf("worked-for duration = %v, want 10m (must measure first-progress→settlement, not first→last-update)", d)
	}
	if len(lines) != 1 {
		t.Fatalf("want 1 progress line, got %d", len(lines))
	}
	if lines[0].At.IsZero() {
		t.Error("lines[0].At is zero (must be a real timestamp)")
	}
}

// TestSetProgressReturnsLogError verifies that SetProgress surfaces the error
// from the progress_log INSERT rather than discarding it silently.  We simulate
// the failure by dropping the progress_log table before the call.
func TestSetProgressReturnsLogError(t *testing.T) {
	s, _ := newStore(t)
	// Record one progress line to make the progress row exist (ON CONFLICT UPDATE path).
	if _, err := s.SetProgress(ctx, "s1", "first", 0, 0); err != nil {
		t.Fatal(err)
	}
	// Drop progress_log to make the INSERT fail on the next call.
	if _, err := s.DB.ExecContext(ctx, "DROP TABLE progress_log"); err != nil {
		t.Fatalf("drop progress_log: %v", err)
	}
	_, err := s.SetProgress(ctx, "s1", "second", 0, 0)
	if err == nil {
		t.Fatal("SetProgress must return the progress_log insert error, got nil")
	}
}

// TestClearProgressAtomicOneCaller ensures that when multiple goroutines race
// on ClearProgress for the same session, exactly one of them gets the progress
// lines (the rest see nil).  The -race flag verifies that the implementation
// accesses the DB without data races.
func TestClearProgressAtomicOneCaller(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.SetProgress(ctx, "s1", "working", 1, 1); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	results := make([][]ProgressLine, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, lines, _ := s.ClearProgress(ctx, "s1")
			results[i] = lines
		}(i)
	}
	close(start)
	wg.Wait()

	got := 0
	for _, lines := range results {
		if len(lines) > 0 {
			got++
		}
	}
	if got != 1 {
		t.Errorf("exactly 1 goroutine must get the progress lines; got %d", got)
	}
}

// TestTrimProgressLog verifies that TrimProgressLog removes progress_log rows
// for sessions that no longer exist, and leaves rows for live sessions alone.
func TestTrimProgressLog(t *testing.T) {
	s, _ := newStore(t)
	// s1 is live; add a progress_log row via SetProgress.
	if _, err := s.SetProgress(ctx, "s1", "ok", 0, 0); err != nil {
		t.Fatal(err)
	}
	// Insert an orphaned progress_log row for a session that doesn't exist.
	// Temporarily disable FK enforcement so we can insert without a sessions row.
	_, _ = s.DB.Exec("PRAGMA foreign_keys = OFF")
	_, _ = s.DB.Exec("INSERT INTO progress_log(session_id, text, n, total, at) VALUES ('gone', 'old', 0, 0, 1)")
	_, _ = s.DB.Exec("PRAGMA foreign_keys = ON")

	// Trim must remove orphaned rows but leave s1's row intact.
	if err := s.TrimProgressLog(ctx); err != nil {
		t.Fatalf("TrimProgressLog: %v", err)
	}

	var goneCount, s1Count int
	_ = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM progress_log WHERE session_id = 'gone'").Scan(&goneCount)
	_ = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM progress_log WHERE session_id = 's1'").Scan(&s1Count)
	if goneCount != 0 {
		t.Errorf("progress_log rows for vanished session 'gone': got %d, want 0", goneCount)
	}
	if s1Count != 1 {
		t.Errorf("progress_log rows for live session 's1': got %d, want 1", s1Count)
	}
}
