package serve

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/schuettc/tackle/internal/casebook/app"
)

// Sync states, as the "sync" live event and SyncView say them.
const (
	SyncRunning = "running"
	SyncDone    = "done"
	SyncFailed  = "failed"
)

// SyncView is the response body of POST /api/sync: a sync is running, and
// whether this request started it (false: it joined the one already running).
type SyncView struct {
	Running bool `json:"running"`
	Started bool `json:"started"`
}

// SyncEvent is the payload of the "sync" live event: running when a sync
// starts, then done (the index is rebuilt from what it observed) or failed
// with serve's reason.
type SyncEvent struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

// syncNow is the sync POST /api/sync runs: the whole sync (spec §5.1 "offers
// to sync first"), App.Sync with its own machine lock. Tests replace it.
func (s *Server) syncNow(ctx context.Context) error {
	if s.runSync != nil {
		return s.runSync(ctx)
	}
	_, err := s.App.Sync(ctx, app.SyncOptions{})
	return err
}

// Syncing reports whether a sync POST /api/sync started is still running.
func (s *Server) Syncing() bool {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return s.syncing
}

// postSync is POST /api/sync. It starts one sync in the background and
// answers at once; a request while one runs joins it (no second sync). The
// live wire says "sync running", then "sync done" once the index is rebuilt
// from the new observation, or "sync failed" with the reason. Another
// process's sync (the machine lock, app.ErrSyncBusy) is a failure the page
// reads in serve's words.
func (s *Server) postSync(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	s.syncMu.Lock()
	if s.syncing {
		s.syncMu.Unlock()
		reply(w, SyncView{Running: true, Started: false}, nil)
		return
	}
	s.syncing = true
	s.syncMu.Unlock()

	ctx := s.laneCtx()
	s.publish(ctx, "sync", SyncEvent{State: SyncRunning})
	go s.runSyncJob(ctx)
	reply(w, SyncView{Running: true, Started: true}, nil)
}

// runSyncJob runs the sync, rebuilds the index (so its built_at, the age a
// plan is refused by, is the sync's), and announces how it ended.
//
// The sync holds serve's remote lock (s.remoteMu, push.go) so it never runs
// beside serve's background push; App.Sync then takes the machine's sync
// lock (the order push.go documents).
func (s *Server) runSyncJob(ctx context.Context) {
	s.remoteMu.Lock()
	err := s.syncNow(ctx)
	s.remoteMu.Unlock()
	if err == nil {
		err = s.rebuild(ctx)
	}
	s.syncMu.Lock()
	s.syncing = false
	s.syncMu.Unlock()
	if err != nil {
		msg := err.Error()
		if errors.Is(err, app.ErrSyncBusy) {
			msg = "another casebook sync is running on this machine"
		}
		lw := s.Log
		if lw == nil {
			lw = os.Stderr
		}
		_, _ = fmt.Fprintf(lw, "casebook serve: sync: %v\n", err)
		s.publish(ctx, "sync", SyncEvent{State: SyncFailed, Error: msg})
		return
	}
	s.publish(ctx, "sync", SyncEvent{State: SyncDone})
}
