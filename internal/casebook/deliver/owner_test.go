package deliver

import (
	"testing"
	"time"
)

// sessionByID reads one row of Sessions (the list the page gets).
func sessionByID(t *testing.T, q *Queue, id string) Session {
	t.Helper()
	ss, err := q.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range ss {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no session %s in %v", id, ss)
	return Session{}
}

// The name comes from the harness side (pi-casebook's session-info), the
// liveness from the channel's presence: a presence beat never clears the
// name, and a rename replaces it.
func TestSessionNameSurvivesPresenceAndFollowsARename(t *testing.T) {
	q, _ := newQueue(t)
	if err := q.SetInfo(ctx, SessionInfo{ID: "s1", Name: "tools-workspace/casebook", Harness: "pi", CWD: "/w", PID: 42}); err != nil {
		t.Fatal(err)
	}
	if got := sessionByID(t, q, "s1").Name; got != "tools-workspace/casebook" {
		t.Fatalf("name %q", got)
	}
	if err := q.Touch(ctx, Session{ID: "s1", Harness: "pi", Label: "pi · w", CWD: "/w", PID: 42}); err != nil {
		t.Fatal(err)
	}
	if got := sessionByID(t, q, "s1").Name; got != "tools-workspace/casebook" {
		t.Fatalf("a presence beat changed the name to %q", got)
	}
	if err := q.SetInfo(ctx, SessionInfo{ID: "s1", Name: "tools-workspace/owner", Harness: "pi", CWD: "/w", PID: 42}); err != nil {
		t.Fatal(err)
	}
	if got := sessionByID(t, q, "s1").Name; got != "tools-workspace/owner" {
		t.Fatalf("after a rename the name is %q", got)
	}
	one, err := q.Session(ctx, "s1")
	if err != nil || one.Name != "tools-workspace/owner" {
		t.Fatalf("Session(s1) = %+v, %v", one, err)
	}
	// session-info for a session the channel hasn't announced yet makes the
	// row (pi-casebook can speak first).
	if err := q.SetInfo(ctx, SessionInfo{ID: "s2", Name: "luminary-meridian/site", Harness: "pi", CWD: "/lm", PID: 7}); err != nil {
		t.Fatal(err)
	}
	if s := sessionByID(t, q, "s2"); s.Name != "luminary-meridian/site" || s.CWD != "/lm" || s.Harness != "pi" || s.Left {
		t.Fatalf("s2 = %+v", s)
	}
}

// A worker is a child session (pi-subagents marks it: a parentSession in its
// header, or no session file at all) that runs inside a process another live
// session is also in. A fork carries parentSession too, but runs in its own
// pi process, so it is not a worker.
func TestWorkerIsAChildSharingALiveSessionsProcess(t *testing.T) {
	q, c := newQueue(t) // s1: pid 42, no child mark
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(q.SetInfo(ctx, SessionInfo{ID: "s1", Name: "luminary-meridian/site", Harness: "pi", CWD: "/w", PID: 42}))
	must(q.SetInfo(ctx, SessionInfo{ID: "w1", Name: "worker#40c0f7e1", Harness: "pi", CWD: "/w", PID: 42, Child: true}))
	must(q.SetInfo(ctx, SessionInfo{ID: "fork", Name: "bettor-help-workspace/platform", Harness: "pi", CWD: "/b", PID: 77, Child: true}))
	must(q.Touch(ctx, Session{ID: "cc", Harness: "claude", CWD: "/c", PID: 42})) // a channel beat, no child mark
	for id, want := range map[string]bool{"s1": false, "w1": true, "fork": false, "cc": false} {
		if got := sessionByID(t, q, id).Worker; got != want {
			t.Errorf("%s worker = %v, want %v", id, got, want)
		}
	}
	// A channel beat (which knows nothing of children) doesn't clear the mark.
	must(q.Touch(ctx, Session{ID: "w1", Harness: "pi", CWD: "/w", PID: 42}))
	if !sessionByID(t, q, "w1").Worker {
		t.Error("a presence beat cleared the worker's child mark")
	}
	// Session(id) agrees with the list.
	if one, err := q.Session(ctx, "w1"); err != nil || !one.Worker {
		t.Errorf("Session(w1) = %+v, %v", one, err)
	}
	// pid 0 (unknown) never pairs two sessions.
	must(q.SetInfo(ctx, SessionInfo{ID: "z1", Harness: "pi", CWD: "/z"}))
	must(q.SetInfo(ctx, SessionInfo{ID: "z2", Harness: "pi", CWD: "/z", Child: true}))
	if sessionByID(t, q, "z2").Worker {
		t.Error("two sessions with no pid were paired")
	}
	// The process's other session leaving (only the child still beats) makes
	// the child stand alone: no live sibling, so not a worker.
	c.add(2 * DefaultLeftAfter)
	must(q.Touch(ctx, Session{ID: "w1", Harness: "pi", CWD: "/w", PID: 42}))
	if sessionByID(t, q, "w1").Worker {
		t.Error("a child whose process has no other live session is still a worker")
	}
}

// Eligible sessions are the ones the page may offer Court: live and not
// workers. Left and worker sessions stay in the list (their threads and the
// move-to path need them) but are not eligible.
func TestEligibleIsLiveAndNotAWorker(t *testing.T) {
	q, c := newQueue(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(q.SetInfo(ctx, SessionInfo{ID: "gone", Name: "old", Harness: "pi", CWD: "/g", PID: 9}))
	c.add(2 * DefaultLeftAfter)
	must(q.Touch(ctx, Session{ID: "s1", Harness: "pi", CWD: "/w", PID: 42}))
	must(q.SetInfo(ctx, SessionInfo{ID: "w1", Name: "worker#ab0b4c89", Harness: "pi", CWD: "/w", PID: 42, Child: true}))
	must(q.Touch(ctx, Session{ID: "cc", Harness: "claude", CWD: "/c", PID: 5}))
	want := map[string]bool{"s1": true, "cc": true, "w1": false, "gone": false}
	ss, err := q.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != len(want) {
		t.Fatalf("sessions %v", ss)
	}
	for _, s := range ss {
		if s.Eligible != want[s.ID] {
			t.Errorf("%s eligible = %v, want %v (left %v, worker %v)", s.ID, s.Eligible, want[s.ID], s.Left, s.Worker)
		}
	}
}

// Prune removes exited sessions that hold nothing: gone longer than the
// retention, no threads, no deliveries. Everything else stays.
func TestPruneDropsOnlyLongGoneEmptySessions(t *testing.T) {
	q, c := newQueue(t) // s1
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	const keep = 7 * 24 * time.Hour
	// old-empty: gone long ago, nothing in it, a stale progress line.
	must(q.Touch(ctx, Session{ID: "old-empty", Harness: "pi", CWD: "/o", PID: 1}))
	_, err := q.DB.ExecContext(ctx, `INSERT INTO progress(session_id, text, started_at, updated_at) VALUES ('old-empty', 'x', 1, 1)`)
	must(err)
	_, err = q.DB.ExecContext(ctx, `INSERT INTO progress_log(session_id, text, at) VALUES ('old-empty', 'x', 1)`)
	must(err)
	// old-thread: gone long ago, but it has a thread (readable via move-to).
	must(q.Touch(ctx, Session{ID: "old-thread", Harness: "pi", CWD: "/o", PID: 2}))
	thread(t, q, "old-thread")
	// old-queued: gone long ago with a queued message.
	must(q.Touch(ctx, Session{ID: "old-queued", Harness: "pi", CWD: "/o", PID: 3}))
	post(t, q, thread(t, q, "old-queued"), "still waiting", false)
	// old-moved: its thread moved away, but a finished delivery of its own
	// is still referenced by the moved messages.
	must(q.Touch(ctx, Session{ID: "old-moved", Harness: "pi", CWD: "/o", PID: 4}))
	th := thread(t, q, "old-moved")
	post(t, q, th, "do it", false)
	d, err := q.Next(ctx, "old-moved")
	must(err)
	if d == nil {
		t.Fatal("no delivery")
	}
	_, err = q.Settled(ctx, "old-moved", []int64{d.ID})
	must(err)
	_, _, err = q.MoveSession(ctx, "old-moved", "s1")
	must(err)

	c.add(keep + time.Hour)
	// recent-empty: gone, but within the retention.
	must(q.Touch(ctx, Session{ID: "recent-empty", Harness: "pi", CWD: "/r", PID: 5}))
	c.add(2 * DefaultLeftAfter)
	// live-empty: here now.
	must(q.Touch(ctx, Session{ID: "live-empty", Harness: "pi", CWD: "/l", PID: 6}))

	n, err := q.Prune(ctx, keep)
	must(err)
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}
	ss, err := q.Sessions(ctx)
	must(err)
	got := map[string]bool{}
	for _, s := range ss {
		got[s.ID] = true
	}
	for id, want := range map[string]bool{"old-empty": false, "old-thread": true, "old-queued": true, "old-moved": true, "recent-empty": true, "live-empty": true, "s1": true} {
		if got[id] != want {
			t.Errorf("%s present = %v, want %v", id, got[id], want)
		}
	}
	var rows int
	must(q.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM progress WHERE session_id = 'old-empty') + (SELECT count(*) FROM progress_log WHERE session_id = 'old-empty')`).Scan(&rows))
	if rows != 0 {
		t.Errorf("%d progress rows of the pruned session remain", rows)
	}
	// Idempotent.
	if n, err := q.Prune(ctx, keep); err != nil || n != 0 {
		t.Errorf("second prune: %d, %v", n, err)
	}
}
