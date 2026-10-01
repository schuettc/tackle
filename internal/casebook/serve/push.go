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
//     page asks for the summary again.
//
// Locks, always taken in this order (never the reverse):
//
//  1. s.remoteMu, serve's serialization of its remote git work on
//     casebook-data: this push and POST /api/sync's sync (runSyncJob).
//  2. the machine's sync lock (app.LockSync, a flock), which App.Push and
//     App.Sync take: no push beside a sync in another process (launchd, the
//     CLI). While another holds it, the push tries again on serve's clock.
//  3. store.Repo's own mutex, held per local mutation (a commit, a rebase)
//     and never while taking 1 or 2: a decide's commit can land while a push
//     waits on the network, but never inside its rebase.
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
func (s *Server) schedulePush() {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
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

// offlineQueued counts the local commits the casebook remote doesn't have,
// for the summary. While a push is in flight it is 0 unless the push before
// it failed: a decision on its way out isn't offline.
func (s *Server) offlineQueued(ctx context.Context) int {
	s.pushMu.Lock()
	pushing, failed := s.pushRunning, s.pushFailed
	s.pushMu.Unlock()
	if pushing && !failed {
		return 0
	}
	out, err := gitx.Run(ctx, s.App.Repo.Dir, "rev-list", "--count", "origin/main..HEAD")
	if err != nil {
		return 0
	}
	return atoi(out)
}
