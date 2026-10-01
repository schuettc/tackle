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

	"github.com/schuettc/tackle/internal/casebook/config"
	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/journal"
	"github.com/schuettc/tackle/internal/casebook/observe"
	"github.com/schuettc/tackle/internal/casebook/spool"
	"github.com/schuettc/tackle/internal/casebook/store"
	"github.com/schuettc/tackle/internal/casebook/temppath"
	"github.com/schuettc/tackle/internal/casebook/view"
	tools "github.com/schuettc/tools-common"
)

// ErrSyncBusy is returned by Sync when another sync is already running on this
// machine (detected via an exclusive file lock on the sync lock file).
var ErrSyncBusy = errors.New("another casebook sync is running")

// SyncOptions controls a sync.
type SyncOptions struct {
	NoGitHub bool // skip the GitHub refresh (use the cache)
	NoPush   bool // commit locally, don't pull or push
}

// SyncReport says what a sync did.
type SyncReport struct {
	Events       int      `json:"events"`
	TempEvents   int      `json:"temp_events,omitempty"` // drained from the spool but in a temp folder: dropped
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
	unlock, err := LockSync()
	if err != nil {
		return rep, err
	}
	defer unlock()
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
	// Events spooled in a temp folder (by a binary before the hook skipped
	// them, or by one that still doesn't) are dropped here: the spool files
	// go with batch.Done like the rest.
	events := a.dropTemp(batch.Events)
	rep.Events, rep.TempEvents, rep.BadEvents = len(events), len(batch.Events)-len(events), batch.Bad

	snap, scanErrs := observe.Scan(ctx, a.Cfg.Roots, a.Cfg.Machine)
	rep.Clones = len(snap.Clones)
	for _, e := range scanErrs {
		rep.ScanErrors = append(rep.ScanErrors, e.Error())
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
		// Two-phase landed computation:
		// 1. Git ancestry check for all non-default branches.
		// 2. Fetch merged PRs only for repos that still have unlanded branches.
		// 3. Merged-PR check for the remaining branches.
		allRepos := observe.AllRepos(g)
		observe.ComputeAncestorLanded(ctx, &snap, allRepos)
		reposToCheck := observe.ReposWithUnlandedBranches(snap, allRepos)
		g.MergedPRs = observe.FetchMergedPRs(ctx, a.Gh, reposToCheck, g.MergedPRs, a.Now())
		observe.ComputeMergedPRLanded(ctx, &snap, g.MergedPRs)
		if err := observe.SaveGitHub(config.CachePath(), g); err != nil {
			return rep, err
		}
	}
	// Encode the snapshot after landed computation so branch landed fields are
	// persisted to machines/<machine>.json for other machines to read.
	sb, err := observe.EncodeSnapshot(snap)
	if err != nil {
		return rep, err
	}
	// The write phase and its commit are one step under casebook-data's
	// lock (store.Repo.Batch): a decide (serve's, or the CLI's in another
	// process) can't commit this sync's files halfway, and serve's rebuild
	// never reads them halfway.
	var snaps []observe.Snapshot
	committed, err := a.Repo.Batch(ctx, fmt.Sprintf("sync %s: %d events, %d clones", a.Cfg.Machine, rep.Events, rep.Clones), func() error {
		if _, err := a.Repo.WriteFile("machines/"+a.Cfg.Machine+".json", sb); err != nil {
			return err
		}
		var err error
		if snaps, err = a.snapshots(); err != nil {
			return err
		}
		if err := a.journal(events, snaps); err != nil {
			return err
		}
		return a.render(g, snaps, &rep)
	})
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
	if ok, err := a.Repo.Batch(ctx, "sync "+a.Cfg.Machine+": re-render views", func() error {
		return a.render(g, snaps, &rep)
	}); err != nil || !ok {
		return rep, err
	}
	_, err = a.pushSync(ctx, &rep)
	return rep, err
}

// LockSync takes this machine's sync lock (a flock on StateDir/sync.lock)
// without waiting, so that concurrent triggers (launchd, pi session events,
// manual, serve's background push) cannot collide on casebook-data's git and
// github.json. ErrSyncBusy when another holder has it. Sync and Push take it.
func LockSync() (unlock func(), err error) {
	lockDir := tools.StateDir(config.Tool)
	if err := tools.EnsureDir(lockDir); err != nil {
		return nil, err
	}
	lf, err := os.OpenFile(filepath.Join(lockDir, "sync.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lf.Close() // already returning ErrSyncBusy; close error not actionable
		return nil, ErrSyncBusy
	}
	return func() { _ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN); _ = lf.Close() }, nil
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

// dropTemp returns the events that did not happen in a temp folder
// (temppath; this machine's scan roots are never temp).
func (a *App) dropTemp(events []journal.Event) []journal.Event {
	temp := temppath.New(a.Cfg.Roots)
	out := make([]journal.Event, 0, len(events))
	for _, ev := range events {
		if !temp.Event(ev) {
			out = append(out, ev)
		}
	}
	return out
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
