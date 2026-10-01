package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/app"
	"github.com/schuettc/tackle/internal/casebook/config"
	"github.com/schuettc/tackle/internal/casebook/db"
	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/store"
)

// pushEvents returns the "push" events published after cursor, in order.
func pushEvents(t *testing.T, r *rig, cursor int64) []PushEvent {
	t.Helper()
	evs, _, err := r.s.Bus.Since(ctx, cursor, 10000)
	if err != nil {
		t.Fatal(err)
	}
	var out []PushEvent
	for _, e := range evs {
		if e.Type != "push" {
			continue
		}
		var pe PushEvent
		if err := json.Unmarshal(e.Data, &pe); err != nil {
			t.Fatal(err)
		}
		out = append(out, pe)
	}
	return out
}

// waitPushes waits until n "push" events were published after cursor.
func waitPushes(t *testing.T, r *rig, cursor int64, n int) []PushEvent {
	t.Helper()
	var got []PushEvent
	eventually(t, fmt.Sprintf("%d push events", n), func() bool {
		got = pushEvents(t, r, cursor)
		return len(got) >= n
	})
	return got
}

// offlineQueued reads GET /api/summary's offline_queued.
func (r *rig) offlineQueued(t *testing.T) int {
	t.Helper()
	var sum SummaryView
	if c := r.do(t, "GET", "/api/summary", nil, &sum); c != http.StatusOK {
		t.Fatalf("summary %d", c)
	}
	return sum.OfflineQueued
}

// summary reads GET /api/summary.
func (r *rig) summary(t *testing.T) SummaryView {
	t.Helper()
	var sum SummaryView
	if c := r.do(t, "GET", "/api/summary", nil, &sum); c != http.StatusOK {
		t.Fatalf("summary %d", c)
	}
	return sum
}

// remoteLog is the bare remote's main, subjects only.
func (r *rig) remoteLog(t *testing.T) string {
	t.Helper()
	out, err := git(t, r.Remote, "log", "--format=%s", "main")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// blockPush makes serve's push wait for release() (or serve's context)
// before it runs the real push. started receives once per push. The test's
// end releases it, so a failed check doesn't leave the pusher blocked.
func blockPush(t *testing.T, r *rig) (started chan struct{}, release func()) {
	started, gate := make(chan struct{}, 100), make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	r.s.runPush = func(ctx context.Context) error {
		started <- struct{}{}
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
		return r.s.App.Push(ctx)
	}
	return started, release
}

// within runs fn and fails the test when it hasn't returned within d.
func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return within %v", what, d)
	}
}

// TestDecideRepliesWhileThePushIsBlocked: decide, accept and change reply as
// soon as the decision is committed, its proposals retired and the index
// rebuilt; the push (blocked here) follows in the background. While it is in
// flight nothing is "offline": offline_queued stays 0.
func TestDecideRepliesWhileThePushIsBlocked(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	started, releaseAll := blockPush(t, r)
	before, _ := r.s.Bus.Head(ctx)
	builds := r.s.rebuilds.Load()

	var res DecideResult
	within(t, 5*time.Second, "POST /api/decide", func() {
		if c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"pr:schuettc/hail#3"}, "disposition": "keep"}, &res); c != 200 {
			t.Errorf("decide %d", c)
		}
	})
	if res.Decided != 1 {
		t.Fatalf("decide %+v", res)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the push never started")
	}
	// Committed locally and the index rebuilt (announced) before the reply.
	if d, _ := r.App.Repo.ReadDecision(mustKey(t, "pr:schuettc/hail#3")); d == nil || d.Disposition != "keep" {
		t.Fatalf("decision not committed: %+v", d)
	}
	if r.s.rebuilds.Load() <= builds {
		t.Fatal("the index was not rebuilt before the reply")
	}
	if !strings.Contains(r.remoteLog(t), "decide pr:schuettc/hail#3") {
		// expected: the push hasn't run
	} else {
		t.Fatal("the remote has the decision while the push is blocked")
	}
	if n := r.offlineQueued(t); n != 0 {
		t.Fatalf("offline_queued %d while a push is in flight, want 0 (not offline yet)", n)
	}

	// accept and change reply while the push is still blocked.
	var props struct {
		Proposals []struct{ ID int64 } `json:"proposals"`
	}
	r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": []string{"issue:schuettc/hail#4", "issue:schuettc/hail#5"}, "disposition": "close"}, &props)
	if len(props.Proposals) != 2 {
		t.Fatalf("propose %+v", props)
	}
	var acc AcceptResult
	within(t, 5*time.Second, "POST /api/proposals/accept", func() {
		r.do(t, "POST", "/api/proposals/accept", map[string]any{"ids": []int64{props.Proposals[0].ID}}, &acc)
	})
	if acc.Accepted != 1 {
		t.Fatalf("accept %+v", acc)
	}
	var ch DecideResult
	within(t, 5*time.Second, "POST /api/proposals/change", func() {
		r.do(t, "POST", "/api/proposals/change", map[string]any{"id": props.Proposals[1].ID, "disposition": "keep"}, &ch)
	})
	if ch.Decided != 1 {
		t.Fatalf("change %+v", ch)
	}
	if p, _ := r.s.Props.Get(ctx, props.Proposals[1].ID); p.State != "changed" {
		t.Fatalf("changed proposal state %q", p.State)
	}
	if got := pushEvents(t, r, before); len(got) != 0 {
		t.Fatalf("a push ended while blocked: %+v", got)
	}

	// Released: the push and its follow-up put all three on the remote.
	releaseAll()
	eventually(t, "all three decisions on the remote", func() bool {
		l := r.remoteLog(t)
		return strings.Contains(l, "pr:schuettc/hail#3") && strings.Contains(l, "issue:schuettc/hail#4") && strings.Contains(l, "issue:schuettc/hail#5")
	})
	waitIdle(t, r)
	if n := r.offlineQueued(t); n != 0 {
		t.Fatalf("offline_queued %d after the push, want 0", n)
	}
}

// waitIdle waits until serve's pusher has nothing in flight or queued.
func waitIdle(t *testing.T, r *rig) {
	t.Helper()
	eventually(t, "the pusher idle", func() bool {
		r.s.pushMu.Lock()
		defer r.s.pushMu.Unlock()
		return !r.s.pushRunning
	})
}

// TestConcurrentDecidesCoalescePushes: N decides that land while a push is in
// flight make one follow-up push, not N; and none is lost: the follow-up
// pushes the decisions the in-flight push (which already sent its commits)
// didn't have.
func TestConcurrentDecidesCoalescePushes(t *testing.T) {
	r := newRig(t)
	var pushes atomic.Int32
	inFlight := make(chan struct{}, 10)
	release := make(chan struct{})
	r.s.runPush = func(ctx context.Context) error {
		n := pushes.Add(1)
		err := r.s.App.Push(ctx) // the real push first: what it sends is fixed
		if n == 1 {
			inFlight <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return err
	}
	if c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"pr:schuettc/hail#3"}, "disposition": "keep"}, nil); c != 200 {
		t.Fatalf("decide %d", c)
	}
	select {
	case <-inFlight:
	case <-time.After(5 * time.Second):
		t.Fatal("the first push never ran")
	}
	const N = 8
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var res DecideResult
			if c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{fmt.Sprintf("issue:schuettc/hail#%d", 4+i)}, "disposition": "keep"}, &res); c != 200 || res.Decided != 1 {
				t.Errorf("decide %d: %d %+v", i, c, res)
			}
		}(i)
	}
	within(t, 10*time.Second, "the concurrent decides", wg.Wait)
	if got := pushes.Load(); got != 1 {
		t.Fatalf("%d pushes while the first was in flight, want 1", got)
	}
	close(release)
	eventually(t, "every decision on the remote", func() bool {
		l := r.remoteLog(t)
		for i := 0; i < N; i++ {
			if !strings.Contains(l, fmt.Sprintf("decide issue:schuettc/hail#%d ", 4+i)) {
				return false
			}
		}
		return strings.Contains(l, "decide pr:schuettc/hail#3 ")
	})
	waitIdle(t, r)
	if got := pushes.Load(); got > 2 {
		t.Fatalf("%d pushes for %d decides, want at most 2", got, N+1)
	}
}

// TestFailedPushLeavesOfflineQueued: a push that can't reach the remote
// leaves its decisions counted in offline_queued (announced, so the page
// asks again), still counted while the next push is in flight, and a later
// push that succeeds clears it.
func TestFailedPushLeavesOfflineQueued(t *testing.T) {
	r := newRig(t)
	gone := r.Remote + ".away"
	if err := os.Rename(r.Remote, gone); err != nil {
		t.Fatal(err)
	}
	before, _ := r.s.Bus.Head(ctx)
	for i, key := range []string{"issue:schuettc/hail#4", "issue:schuettc/hail#5"} {
		if c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{key}, "disposition": "keep"}, nil); c != 200 {
			t.Fatalf("decide %d", c)
		}
		evs := waitPushes(t, r, before, i+1)
		waitIdle(t, r)
		if last := evs[len(evs)-1]; last.State != PushOffline {
			t.Fatalf("push event %+v, want %s", last, PushOffline)
		}
	}
	if sum := r.summary(t); sum.OfflineQueued != 2 || sum.PushError != "" {
		t.Fatalf("after two unpushed decisions: offline_queued %d, push_error %q; want 2 and none (offline isn't a push failure)", sum.OfflineQueued, sum.PushError)
	}
	if err := os.Rename(gone, r.Remote); err != nil {
		t.Fatal(err)
	}
	mid, _ := r.s.Bus.Head(ctx)
	started, release := blockPush(t, r)
	if c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"issue:schuettc/hail#6"}, "disposition": "keep"}, nil); c != 200 {
		t.Fatalf("decide %d", c)
	}
	<-started
	// The push before failed: while this one is in flight the page still
	// says offline, with the new decision counted.
	if n := r.offlineQueued(t); n != 3 {
		t.Fatalf("offline_queued %d during a push after a failed one, want 3", n)
	}
	release()
	evs := waitPushes(t, r, mid, 1)
	waitIdle(t, r)
	if evs[0].State != PushDone {
		t.Fatalf("push event %+v, want %s", evs[0], PushDone)
	}
	if n := r.offlineQueued(t); n != 0 {
		t.Fatalf("offline_queued %d after a push that succeeded, want 0", n)
	}
	l := r.remoteLog(t)
	for _, n := range []int{4, 5, 6} {
		if !strings.Contains(l, fmt.Sprintf("issue:schuettc/hail#%d", n)) {
			t.Fatalf("remote lacks issue #%d:\n%s", n, l)
		}
	}
}

// TestPushAndSyncNeverOverlap: serve's background push and POST /api/sync's
// sync never run git on casebook-data at the same time, in either order.
func TestPushAndSyncNeverOverlap(t *testing.T) {
	r := newRig(t)
	var running, most atomic.Int32
	enter := func() {
		n := running.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
	}
	pushIn, pushGo := make(chan struct{}, 10), make(chan struct{}, 10)
	syncIn, syncGo := make(chan struct{}, 10), make(chan struct{}, 10)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) }) // a failed check doesn't leave them blocked
	hold := func(in, goOn chan struct{}) {
		enter()
		defer running.Add(-1)
		in <- struct{}{}
		select {
		case <-goOn:
		case <-stop:
		}
	}
	r.s.runPush = func(context.Context) error { hold(pushIn, pushGo); return nil }
	r.s.runSync = func(context.Context) error { hold(syncIn, syncGo); return nil }
	quiet := func(ch chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
			t.Fatalf("%s ran while the other held casebook-data", what)
		case <-time.After(300 * time.Millisecond):
		}
	}
	wait := func(ch chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s never ran", what)
		}
	}

	// A push in flight: a sync waits for it.
	r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"issue:schuettc/hail#4"}, "disposition": "keep"}, nil)
	wait(pushIn, "the push")
	before, _ := r.s.Bus.Head(ctx)
	r.do(t, "POST", "/api/sync", map[string]any{}, nil)
	quiet(syncIn, "the sync")
	pushGo <- struct{}{}
	wait(syncIn, "the sync")

	// A sync in flight: a push waits for it.
	r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"issue:schuettc/hail#5"}, "disposition": "keep"}, nil)
	quiet(pushIn, "the push")
	syncGo <- struct{}{}
	wait(pushIn, "the push")
	pushGo <- struct{}{}
	waitSyncEnd(t, r, before)
	waitIdle(t, r)
	if m := most.Load(); m != 1 {
		t.Fatalf("%d git operations on casebook-data at once, want 1", m)
	}
}

// TestPushWaitsForAnotherProcessSync: a sync in another process holds the
// machine's sync lock; serve's push doesn't run beside it but tries again
// (on serve's clock) until the lock is free, then pushes.
func TestPushWaitsForAnotherProcessSync(t *testing.T) {
	r := newRig(t)
	ticks := make(chan time.Time)
	var waits atomic.Int32
	r.s.after = func(time.Duration) <-chan time.Time { waits.Add(1); return ticks }

	if err := os.MkdirAll(config.StateDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	lf, err := os.OpenFile(filepath.Join(config.StateDir(), "sync.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lf.Close() }()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"issue:schuettc/hail#4"}, "disposition": "keep"}, nil); c != 200 {
		t.Fatalf("decide %d", c)
	}
	eventually(t, "the push waiting for the lock", func() bool { return waits.Load() >= 1 })
	ticks <- time.Time{} // another try: still held
	eventually(t, "a second wait", func() bool { return waits.Load() >= 2 })
	if strings.Contains(r.remoteLog(t), "issue:schuettc/hail#4") {
		t.Fatal("pushed while another sync held the machine lock")
	}
	_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	ticks <- time.Time{}
	eventually(t, "the decision on the remote", func() bool {
		return strings.Contains(r.remoteLog(t), "issue:schuettc/hail#4")
	})
	waitIdle(t, r)
}

// TestShutdownKeepsQueuedDecisions: serve stopping mid-push loses nothing:
// the decision is a local commit, still queued, and the next sync pushes it.
func TestShutdownKeepsQueuedDecisions(t *testing.T) {
	r := newRig(t)
	life, stop := context.WithCancel(context.Background())
	d, err := db.Open(life, filepath.Join(t.TempDir(), "second.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s, err := New(life, r.App, d)
	if err != nil {
		t.Fatal(err)
	}
	r2 := &rig{Rig: r.Rig, s: s, url: serveURL(t, s)}
	started, _ := blockPush(t, r2)
	if c := r2.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"issue:schuettc/hail#4"}, "disposition": "keep"}, nil); c != 200 {
		t.Fatalf("decide %d", c)
	}
	<-started
	stop()
	within(t, 5*time.Second, "the pusher stopping", s.pushWG.Wait)
	if strings.Contains(r.remoteLog(t), "issue:schuettc/hail#4") {
		t.Fatal("pushed although serve stopped mid-push")
	}
	if out, _ := git(t, r.App.Repo.Dir, "rev-list", "--count", "origin/main..HEAD"); out == "0" {
		t.Fatal("the decision is no longer queued locally")
	}
	if _, err := r.App.Sync(context.Background(), app.SyncOptions{NoGitHub: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.remoteLog(t), "issue:schuettc/hail#4") {
		t.Fatal("the next sync didn't push the queued decision")
	}
}

// TestPushErrorIsAnnounced: a push that fails for a reason other than the
// network is announced as failed, with serve's reason, and logged.
func TestPushErrorIsAnnounced(t *testing.T) {
	r := newRig(t)
	var log strings.Builder
	var mu sync.Mutex
	r.s.Log = writerFunc(func(b []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return log.Write(b) })
	r.s.runPush = func(context.Context) error { return errors.New("rebase onto the casebook remote failed") }
	before, _ := r.s.Bus.Head(ctx)
	r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"issue:schuettc/hail#4"}, "disposition": "keep"}, nil)
	evs := waitPushes(t, r, before, 1)
	if evs[0].State != PushFailed || !strings.Contains(evs[0].Error, "rebase") {
		t.Fatalf("push event %+v", evs[0])
	}
	waitIdle(t, r)
	if sum := r.summary(t); sum.OfflineQueued != 1 || sum.PushError != "rebase onto the casebook remote failed" {
		t.Fatalf("after a failed push: offline_queued %d, push_error %q; want 1 and serve's reason", sum.OfflineQueued, sum.PushError)
	}
	mu.Lock()
	if !strings.Contains(log.String(), "push: rebase") {
		t.Fatalf("not logged: %q", log.String())
	}
	mu.Unlock()

	// A push that can't reach the remote after it keeps the reason (the
	// last push failure that wasn't the network) ...
	mid, _ := r.s.Bus.Head(ctx)
	r.s.runPush = func(context.Context) error { return fmt.Errorf("%w: no route", store.ErrOffline) }
	r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"issue:schuettc/hail#5"}, "disposition": "keep"}, nil)
	waitPushes(t, r, mid, 1)
	waitIdle(t, r)
	if sum := r.summary(t); sum.OfflineQueued != 2 || sum.PushError != "rebase onto the casebook remote failed" {
		t.Fatalf("after an offline push: offline_queued %d, push_error %q", sum.OfflineQueued, sum.PushError)
	}
	// ... and the next push that succeeds clears it.
	mid, _ = r.s.Bus.Head(ctx)
	r.s.runPush = nil
	r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"issue:schuettc/hail#6"}, "disposition": "keep"}, nil)
	if evs := waitPushes(t, r, mid, 1); evs[0].State != PushDone {
		t.Fatalf("push event %+v, want done", evs[0])
	}
	waitIdle(t, r)
	if sum := r.summary(t); sum.OfflineQueued != 0 || sum.PushError != "" {
		t.Fatalf("after a push that succeeded: offline_queued %d, push_error %q; want 0 and none", sum.OfflineQueued, sum.PushError)
	}
}

// TestPushFailureClearsWhenNothingQueued: once nothing is queued (a sync,
// serve's or another process's, pushed it all), the summary forgets the
// last push failure: the next decide's push in flight isn't "offline", and
// no push error stays shown.
func TestPushFailureClearsWhenNothingQueued(t *testing.T) {
	r := newRig(t)
	before, _ := r.s.Bus.Head(ctx)
	r.s.runPush = func(context.Context) error { return errors.New("sync conflict in notes.txt") }
	r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"issue:schuettc/hail#4"}, "disposition": "keep"}, nil)
	waitPushes(t, r, before, 1)
	waitIdle(t, r)
	if sum := r.summary(t); sum.OfflineQueued != 1 || sum.PushError == "" {
		t.Fatalf("after a failed push: %+v", sum)
	}
	// Another sync pushes everything.
	if err := r.App.Push(ctx); err != nil {
		t.Fatal(err)
	}
	if sum := r.summary(t); sum.OfflineQueued != 0 || sum.PushError != "" {
		t.Fatalf("nothing queued: offline_queued %d, push_error %q; want 0 and none", sum.OfflineQueued, sum.PushError)
	}
	r.s.runPush = nil
	started, release := blockPush(t, r)
	r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"issue:schuettc/hail#5"}, "disposition": "keep"}, nil)
	<-started
	if n := r.offlineQueued(t); n != 0 {
		t.Fatalf("offline_queued %d while a push is in flight after the failure was cleared, want 0", n)
	}
	release()
	waitIdle(t, r)
}

// TestNoPushStartsOnceServeIsStopping: a decide that lands while serve is
// stopping leaves its commit queued and starts no push (no pusher beside
// the database closing, no WaitGroup.Add racing Run's Wait).
func TestNoPushStartsOnceServeIsStopping(t *testing.T) {
	r := newRig(t)
	life, stop := context.WithCancel(context.Background())
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "second.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s, err := New(life, r.App, d)
	if err != nil {
		t.Fatal(err)
	}
	var pushes atomic.Int32
	s.runPush = func(context.Context) error { pushes.Add(1); return nil }
	r2 := &rig{Rig: r.Rig, s: s, url: serveURL(t, s)}
	stop()
	s.stopPushes()
	if c := r2.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"issue:schuettc/hail#4"}, "disposition": "keep"}, nil); c != 200 {
		t.Fatalf("decide %d", c)
	}
	within(t, time.Second, "the pusher", s.pushWG.Wait)
	time.Sleep(100 * time.Millisecond)
	s.pushMu.Lock()
	running := s.pushRunning
	s.pushMu.Unlock()
	if n := pushes.Load(); n != 0 || running {
		t.Fatalf("a push started while serve was stopping (%d pushes, running %v)", n, running)
	}
	if out, _ := git(t, r.App.Repo.Dir, "rev-list", "--count", "origin/main..HEAD"); out != "1" {
		t.Fatalf("queued %q, want the decision queued", out)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }

func mustKey(t *testing.T, s string) item.Key {
	t.Helper()
	k, err := item.ParseKey(s)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// serveURL serves s's API on a test server for the rest of the test.
func serveURL(t *testing.T, s *Server) string {
	t.Helper()
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	return hs.URL
}
