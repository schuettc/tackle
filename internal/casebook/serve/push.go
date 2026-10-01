package serve

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/schuettc/tackle/internal/casebook/app"
	"github.com/schuettc/tackle/internal/casebook/gitx"
	"github.com/schuettc/tackle/internal/casebook/store"
)

// The background push. A decide (POST /api/decide, /api/proposals/accept,
// /api/proposals/change) answers once its decisions are committed locally,
// their proposals retired and the index rebuilt; it never waits on the
// network. The push to the casebook remote follows here:
//
//   - One push at a time, and at most one queued after it. A decide that
//     lands while a push is in flight asks for one more push (pushAgain),
//     which sends what the in-flight one didn't have: no push storm, and no
//     decision left behind.
//   - A push that fails (offline or otherwise) leaves its commits queued:
//     GET /api/summary counts them (offline_queued) until a later push, or
//     the next sync (App.Sync pushes local commits first), sends them.
//     Stopping serve mid-push loses nothing for the same reason.
//   - Every push that ends is announced (the "push" event, PushEvent), so the
//     page asks for the summary again. One that failed for a reason other
//     than the network is also kept for the summary (push_error: the page
//     says "push failed · N queued" and shows why) until a push succeeds or
//     nothing is queued.
//   - Once serve is stopping no push starts (stopPushes).
//
// Locks, always taken in this order (never the reverse):
//
//  1. s.remoteMu, serve's serialization of its remote git work on
//     casebook-data: this push and POST /api/sync's sync (runSyncJob).
//  2. the machine's sync lock (app.LockSync, a flock), which App.Push and
//     App.Sync take: no push beside a sync in another process (launchd, the
//     CLI). While another holds it, the push tries again on serve's clock.
//  3. casebook-data's lock (store/lock.go): an RWMutex for this process,
//     then a flock on .git/casebook.lock for every other casebook process.
//     Held exclusive per local mutation (a write and its commit, Batch; a
//     rebase) and shared by serve's rebuild (Read); never held while taking
//     1 or 2. A decide's commit can land while a push waits on the network,
//     but never inside its rebase; a rebuild never reads a tree mid-rebase
//     or mid-write. The rebase runs on a context serve's shutdown doesn't
//     cancel (bounded by store.RebaseTimeout), so stopping never leaves
//     casebook-data mid-rebase.
//
// rebuildMu (server.go) comes before 3 (rebuild takes the shared lock while
// holding it); nothing holding 3 takes rebuildMu.
//
// s.pushMu guards only the pusher's state and is never held across git.

// pushBusyWait is how long the push waits before trying again while another
// process's sync holds the machine's sync lock.
const pushBusyWait = 2 * time.Second

// pushGit runs one push of the local commits (App.Push; tests replace it).
func (s *Server) pushGit(ctx context.Context) error {
	if s.runPush != nil {
		return s.runPush(ctx)
	}
	return s.App.Push(ctx)
}

// wait is serve's clock for the pusher's retries.
func (s *Server) wait(d time.Duration) <-chan time.Time {
	if s.after != nil {
		return s.after(d)
	}
	return time.After(d)
}

// schedulePush asks for a push of everything committed so far. It starts one
// when none is in flight, else queues one follow-up (however many ask).
//
// Once serve is stopping (stopPushes, or its context done) it starts
// nothing: the commit stays queued for the next push or sync. Checking under
// pushMu, which stopPushes takes before Run waits on pushWG, means no
// pushWG.Add races that Wait.
func (s *Server) schedulePush() {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	if s.pushStopped || s.laneCtx().Err() != nil {
		return
	}
	if s.pushRunning {
		s.pushAgain = true
		return
	}
	s.pushRunning = true
	s.pushWG.Add(1)
	go s.pushLoop(s.laneCtx())
}

// pushLoop pushes, then once more while decides asked during the push.
func (s *Server) pushLoop(ctx context.Context) {
	defer s.pushWG.Done()
	for {
		err := s.pushOnce(ctx)
		s.pushMu.Lock()
		s.pushFailed = err != nil
		switch {
		case err == nil:
			s.pushError = ""
		case errors.Is(err, store.ErrOffline) || ctx.Err() != nil:
			// The network, or serve stopping: not a reason to show.
		default:
			s.pushError = err.Error()
		}
		again := s.pushAgain && ctx.Err() == nil
		s.pushAgain = false
		if !again {
			s.pushRunning = false
		}
		s.pushMu.Unlock()
		if ctx.Err() != nil {
			// serve is stopping: the commits stay queued for the next push
			// or sync, and there's nobody to tell.
			return
		}
		s.announcePush(ctx, err)
		if !again {
			return
		}
	}
}

// pushOnce runs one push under serve's remote lock, trying again while
// another process's sync holds the machine's sync lock.
func (s *Server) pushOnce(ctx context.Context) error {
	s.remoteMu.Lock()
	defer s.remoteMu.Unlock()
	for {
		err := s.pushGit(ctx)
		if !errors.Is(err, app.ErrSyncBusy) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.wait(pushBusyWait):
		}
	}
}

// announcePush publishes how a push ended (and logs one that failed for a
// reason other than the network).
func (s *Server) announcePush(ctx context.Context, err error) {
	ev := PushEvent{State: PushDone}
	switch {
	case err == nil:
	case errors.Is(err, store.ErrOffline):
		ev.State, ev.Error = PushOffline, err.Error()
	default:
		ev.State, ev.Error = PushFailed, err.Error()
		lw := s.Log
		if lw == nil {
			lw = os.Stderr
		}
		_, _ = fmt.Fprintf(lw, "casebook serve: push: %v\n", err)
	}
	s.publish(ctx, "push", ev)
}

// stopPushes is Run's, once serve's context is cancelled and before it waits
// on pushWG: no push starts after it.
func (s *Server) stopPushes() {
	s.pushMu.Lock()
	s.pushStopped = true
	s.pushMu.Unlock()
}

// pushState is the summary's view of the pusher: the local commits the
// casebook remote doesn't have, and why the last push failed when that
// wasn't the network (PushError). While a push is in flight the count is 0
// unless the push before it failed: a decision on its way out isn't
// offline. Once nothing is queued (a push or a sync, serve's or another
// process's, sent it all) the last failure is forgotten.
func (s *Server) pushState(ctx context.Context) (queued int, pushErr string) {
	s.pushMu.Lock()
	pushing, failed := s.pushRunning, s.pushFailed
	s.pushMu.Unlock()
	if pushing && !failed {
		return 0, ""
	}
	out, err := gitx.Run(ctx, s.App.Repo.Dir, "rev-list", "--count", "origin/main..HEAD")
	if err != nil {
		return 0, ""
	}
	queued = atoi(out)
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	if queued == 0 {
		s.pushFailed, s.pushError = false, ""
	}
	return queued, s.pushError
}
