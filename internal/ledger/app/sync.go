package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/schuettc/tackle/internal/ledger/config"
	"github.com/schuettc/tackle/internal/ledger/engine"
	"github.com/schuettc/tackle/internal/ledger/journal"
	"github.com/schuettc/tackle/internal/ledger/observe"
	"github.com/schuettc/tackle/internal/ledger/spool"
	"github.com/schuettc/tackle/internal/ledger/store"
	"github.com/schuettc/tackle/internal/ledger/view"
	tools "github.com/schuettc/tools-common"
)

// ErrSyncBusy is returned by Sync when another sync is already running on this
// machine (detected via an exclusive file lock on the sync lock file).
var ErrSyncBusy = errors.New("another ledger sync is running")

// SyncOptions controls a sync.
type SyncOptions struct {
	NoGitHub bool // skip the GitHub refresh (use the cache)
	NoPush   bool // commit locally, don't pull or push
}

// SyncReport says what a sync did.
type SyncReport struct {
	Events       int      `json:"events"`
	BadEvents    int      `json:"bad_events,omitempty"`
	Clones       int      `json:"clones"`
	ScanErrors   []string `json:"scan_errors,omitempty"`
	GitHubErrors []string `json:"github_errors,omitempty"`
	RateLimited  bool     `json:"rate_limited,omitempty"`
	Resolved     []string `json:"resolved,omitempty"`
	Committed    bool     `json:"committed"`
	Pushed       bool     `json:"pushed"`
	Offline      bool     `json:"offline,omitempty"`
	Attention    int      `json:"attention"`
	Notices      []string `json:"notices,omitempty"`
}

// Sync pulls, records, observes, rebuilds the views, commits and pushes.
// At most one Sync may run on this machine at a time; concurrent callers
// receive ErrSyncBusy immediately.
func (a *App) Sync(ctx context.Context, o SyncOptions) (SyncReport, error) {
	var rep SyncReport
	// Acquire a machine-level exclusive lock so that concurrent triggers
	// (launchd, pi session events, manual) cannot collide on git and github.json.
	lockDir := tools.StateDir(config.Tool)
	if err := tools.EnsureDir(lockDir); err != nil {
		return rep, err
	}
	lf, err := os.OpenFile(filepath.Join(lockDir, "sync.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return rep, err
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lf.Close()
		return rep, ErrSyncBusy
	}
	defer func() { syscall.Flock(int(lf.Fd()), syscall.LOCK_UN); lf.Close() }() //nolint:errcheck
	if !o.NoPush {
		if err := a.remoteSync(ctx, &rep); err != nil {
			return rep, err
		}
	}
	batch, err := spool.Drain(config.SpoolDir())
	if err != nil {
		return rep, err
	}
	defer batch.Close()
	rep.Events, rep.BadEvents = len(batch.Events), batch.Bad

	snap, scanErrs := observe.Scan(ctx, a.Cfg.Roots, a.Cfg.Machine)
	rep.Clones = len(snap.Clones)
	for _, e := range scanErrs {
		rep.ScanErrors = append(rep.ScanErrors, e.Error())
	}
	sb, err := observe.EncodeSnapshot(snap)
	if err != nil {
		return rep, err
	}
	if _, err := a.Repo.WriteFile("machines/"+a.Cfg.Machine+".json", sb); err != nil {
		return rep, err
	}
	snaps, err := a.snapshots()
	if err != nil {
		return rep, err
	}
	if err := a.journal(batch.Events, snaps); err != nil {
		return rep, err
	}

	g, err := observe.LoadGitHub(config.CachePath())
	if err != nil {
		return rep, err
	}
	if !o.NoGitHub {
		decisions, _ := a.Repo.Decisions()
		var gr observe.RefreshReport
		g, gr = observe.Refresh(ctx, a.Gh, g, observe.RefreshOptions{Owners: a.Cfg.Owners, Keys: engine.LookupKeys(decisions), Now: a.Now()})
		rep.GitHubErrors, rep.RateLimited = gr.Errors, gr.RateLimited
		if err := observe.SaveGitHub(config.CachePath(), g); err != nil {
			return rep, err
		}
	}
	if err := a.render(g, snaps, &rep); err != nil {
		return rep, err
	}
	committed, err := a.Repo.Commit(ctx, fmt.Sprintf("sync %s: %d events, %d clones", a.Cfg.Machine, rep.Events, rep.Clones))
	if err != nil {
		// The drained events stay in the spool and are journaled again by the
		// next sync; the uncommitted journal lines from this attempt may then
		// appear twice. Duplicates beat losses.
		return rep, err
	}
	rep.Committed = committed
	if err := batch.Done(); err != nil {
		return rep, err
	}
	if o.NoPush {
		return rep, nil
	}
	res, err := a.pushSync(ctx, &rep)
	if err != nil || !res.ViewsTaken {
		return rep, err
	}
	// Another machine's views won a rebase conflict: render ours again.
	if err := a.render(g, snaps, &rep); err != nil {
		return rep, err
	}
	if ok, err := a.Repo.Commit(ctx, "sync "+a.Cfg.Machine+": re-render views"); err != nil || !ok {
		return rep, err
	}
	_, err = a.pushSync(ctx, &rep)
	return rep, err
}

func (a *App) remoteSync(ctx context.Context, rep *SyncReport) error {
	_, err := a.pushSync(ctx, rep)
	return err
}

// pushSync runs store.Sync and folds offline into the report.
func (a *App) pushSync(ctx context.Context, rep *SyncReport) (store.SyncResult, error) {
	res, err := a.Repo.Sync(ctx)
	rep.Resolved = append(rep.Resolved, res.Resolved...)
	if res.Pushed {
		rep.Pushed = true
	}
	if errors.Is(err, store.ErrOffline) {
		rep.Offline = true
		return res, nil
	}
	return res, err
}

func (a *App) render(g *observe.GitHub, snaps []observe.Snapshot, rep *SyncReport) error {
	in, err := a.input(g, snaps)
	if err != nil {
		return err
	}
	res := engine.Build(in)
	if err := saveSeen(engine.NextSeen(res, in.Seen)); err != nil {
		return err
	}
	rep.Attention, rep.Notices = len(res.Attention()), res.Notices
	for name, b := range view.Render(res, snaps) {
		if _, err := a.Repo.WriteFile(name, b); err != nil {
			return err
		}
	}
	return nil
}

// journal annotates events with this machine and their repos, and appends
// them to the day files.
func (a *App) journal(events []journal.Event, snaps []observe.Snapshot) error {
	byFile := map[string][]byte{}
	for _, ev := range events {
		ev.Machine = a.Cfg.Machine
		for _, p := range []string{ev.GitDir, ev.CWD} {
			if ev.Repo == "" && p != "" {
				ev.Repo = engine.RepoForPath(snaps, a.Cfg.Machine, p)
			}
		}
		for i := range ev.Actions {
			act := &ev.Actions[i]
			if act.Repo != "" {
				continue
			}
			dir := ev.CWD
			if act.Dir != "" {
				dir = act.Dir
				if !filepath.IsAbs(dir) {
					dir = filepath.Join(ev.CWD, dir)
				}
			}
			act.Repo = engine.RepoForPath(snaps, a.Cfg.Machine, filepath.Clean(dir))
			if ev.Repo == "" {
				ev.Repo = act.Repo
			}
		}
		line, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		ts := ev.TS.UTC()
		f := fmt.Sprintf("journal/%s/%04d/%02d-%02d.jsonl", a.Cfg.Machine, ts.Year(), ts.Month(), ts.Day())
		byFile[f] = append(append(byFile[f], line...), '\n')
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		if err := a.Repo.AppendFile(f, byFile[f]); err != nil {
			return err
		}
	}
	return nil
}
