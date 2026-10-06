package deliver

import (
	"fmt"
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

// A worker is a session whose parent session (pi-casebook reports it: the
// session named by its header's parentSession) is live in the same pi
// process. pi-subagents runs its workers inside the parent's pi process;
// a fork's parent is elsewhere, or gone.
func TestWorkerIsASessionWhoseParentIsLiveInItsProcess(t *testing.T) {
	q, c := newQueue(t) // s1: pid 42
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	worker := func(id string) bool { t.Helper(); return sessionByID(t, q, id).Worker }

	// A pi-subagents child: its parent is live in its own pi process.
	must(q.SetInfo(ctx, SessionInfo{ID: "s1", Name: "luminary-meridian/site", Harness: "pi", CWD: "/w", PID: 42}))
	must(q.SetInfo(ctx, SessionInfo{ID: "w1", Name: "worker#40c0f7e1", Harness: "pi", CWD: "/w", PID: 42, Parent: "s1"}))
	if !worker("w1") {
		t.Error("a pi-subagents child of a live parent in its process is not a worker")
	}
	if worker("s1") {
		t.Error("the parent is a worker")
	}
	// A channel beat (which knows nothing of parents) doesn't clear it.
	must(q.Touch(ctx, Session{ID: "w1", Harness: "pi", CWD: "/w", PID: 42}))
	if !worker("w1") {
		t.Error("a presence beat cleared the worker's parent")
	}
	if one, err := q.Session(ctx, "w1"); err != nil || !one.Worker {
		t.Errorf("Session(w1) = %+v, %v (Session(id) must agree with the list)", one, err)
	}

	// A fork that runs subagents of its own: its parent (s1) is live, but in
	// another pi process; its workers share ITS process. The fork is not a
	// worker; its workers are.
	must(q.SetInfo(ctx, SessionInfo{ID: "fork", Name: "bettor-help-workspace/platform", Harness: "pi", CWD: "/b", PID: 77, Parent: "s1"}))
	must(q.SetInfo(ctx, SessionInfo{ID: "fw1", Name: "worker#2d08dfa1", Harness: "pi", CWD: "/b", PID: 77, Parent: "fork"}))
	must(q.SetInfo(ctx, SessionInfo{ID: "fw2", Name: "worker#cd4c04f2", Harness: "pi", CWD: "/b", PID: 77, Parent: "fork"}))
	if worker("fork") {
		t.Error("a fork running its own subagents is a worker")
	}
	if !worker("fw1") || !worker("fw2") {
		t.Error("the fork's subagents are not workers")
	}
	// A fork whose parent has exited, in a process of its own.
	must(q.SetInfo(ctx, SessionInfo{ID: "gone-parent", Harness: "pi", CWD: "/g", PID: 90}))
	c.add(2 * DefaultLeftAfter)
	for _, id := range []string{"s1", "w1", "fork", "fw1", "fw2"} {
		must(q.Touch(ctx, Session{ID: id, Harness: "pi", PID: map[bool]int{true: 42, false: 77}[id == "s1" || id == "w1"]}))
	}
	must(q.SetInfo(ctx, SessionInfo{ID: "orphan-fork", Harness: "pi", CWD: "/g", PID: 91, Parent: "gone-parent"}))
	if worker("orphan-fork") {
		t.Error("a fork whose parent exited is a worker")
	}

	// An in-process /fork: pi replaced session "old" with "new" in the same
	// process (pid 50). pi-casebook reports "old" ended (its
	// session_shutdown); "new" names "old" as its parent. "old" was seen a
	// moment ago, so by last_seen alone it is still here for LeftAfter: its
	// last heartbeat window. Ended, it is not live, and "new" is no worker.
	must(q.SetInfo(ctx, SessionInfo{ID: "old", Name: "tools-workspace/owner", Harness: "pi", CWD: "/w", PID: 50}))
	must(q.Touch(ctx, Session{ID: "old", Harness: "pi", CWD: "/w", PID: 50}))
	must(q.SetInfo(ctx, SessionInfo{ID: "old", Ended: true}))
	must(q.SetInfo(ctx, SessionInfo{ID: "new", Name: "tools-workspace/owner", Harness: "pi", CWD: "/w", PID: 50, Parent: "old"}))
	if worker("new") {
		t.Error("an in-process /fork is a worker in its old session's last heartbeat window")
	}
	if o := sessionByID(t, q, "old"); !o.Left || o.Eligible {
		t.Errorf("the ended session is still here: %+v", o)
	}
	// A late presence beat from old's channel (sent before pi replaced it)
	// lands inside the window: old stays ended.
	c.add(DefaultLeftAfter / 2)
	must(q.Touch(ctx, Session{ID: "old", Harness: "pi", CWD: "/w", PID: 50}))
	if worker("new") || !sessionByID(t, q, "old").Left {
		t.Error("a late beat inside the window brought the ended session back")
	}
	// The old session's ended report naming no name doesn't clear it.
	if o := sessionByID(t, q, "old"); o.Name != "tools-workspace/owner" {
		t.Errorf("ending cleared the name: %q", o.Name)
	}
	// Resumed later (its channel beats again past the window, or pi-casebook
	// reports it running): it is here again.
	c.add(DefaultLeftAfter)
	must(q.Touch(ctx, Session{ID: "old", Harness: "pi", CWD: "/w", PID: 60}))
	if sessionByID(t, q, "old").Left {
		t.Error("a session that came back after the window is still left")
	}
	must(q.SetInfo(ctx, SessionInfo{ID: "old", Ended: true}))
	must(q.SetInfo(ctx, SessionInfo{ID: "old", Name: "tools-workspace/owner", Harness: "pi", CWD: "/w", PID: 60}))
	if sessionByID(t, q, "old").Left {
		t.Error("a session pi-casebook reports running is still left")
	}

	// A Claude session sharing a pid with a live session, with no parent.
	must(q.Touch(ctx, Session{ID: "cc", Harness: "claude", CWD: "/c", PID: 60}))
	if worker("cc") {
		t.Error("a session with no parent is a worker")
	}
	// pid 0 (unknown) and 1 (an orphaned channel's parent) never pair.
	for _, pid := range []int{0, 1} {
		p, ch := fmt.Sprintf("p%d", pid), fmt.Sprintf("ch%d", pid)
		must(q.SetInfo(ctx, SessionInfo{ID: p, Harness: "pi", CWD: "/z", PID: pid}))
		must(q.SetInfo(ctx, SessionInfo{ID: ch, Harness: "pi", CWD: "/z", PID: pid, Parent: p}))
		if worker(ch) {
			t.Errorf("two sessions with pid %d were paired", pid)
		}
	}
	// The parent leaving (only the child still beats) makes the child stand
	// alone: not a worker.
	c.add(2 * DefaultLeftAfter)
	must(q.Touch(ctx, Session{ID: "fw1", Harness: "pi", CWD: "/b", PID: 77}))
	if worker("fw1") {
		t.Error("a child whose parent left is still a worker")
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
	must(q.SetInfo(ctx, SessionInfo{ID: "w1", Name: "worker#ab0b4c89", Harness: "pi", CWD: "/w", PID: 42, Parent: "s1"}))
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
	// old-empty: gone long ago, nothing refers to it.
	must(q.Touch(ctx, Session{ID: "old-empty", Harness: "pi", CWD: "/o", PID: 1}))
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
	// Idempotent.
	if n, err := q.Prune(ctx, keep); err != nil || n != 0 {
		t.Errorf("second prune: %d, %v", n, err)
	}
}
