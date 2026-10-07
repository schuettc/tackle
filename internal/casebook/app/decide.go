package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/store"
	"github.com/schuettc/tools-common/harness"
)

// Actor names who is deciding: the harness session (by the family identity
// rule) when there is one, else the configured user.
func (a *App) Actor() string {
	id := harness.FromEnv()
	switch id.SessionID {
	case "":
		return a.Cfg.User
	case id.ClaudeID:
		return "claude:" + id.SessionID
	default:
		return "pi:" + id.SessionID
	}
}

// DecideOptions are the optional parts of a decision.
type DecideOptions struct {
	Until      string
	Note       string
	By         string // overrides Actor()
	ProposedBy string // who proposed it, when accepting a proposal
	Rule       string // the rule that proposed it, if any
	NoPush     bool
}

func (a *App) decision(k item.Key, disposition string, o DecideOptions) (item.Decision, error) {
	by := o.By
	if by == "" {
		by = a.Actor()
	}
	d := item.Decision{Disposition: item.Disposition(disposition), Until: o.Until, Note: o.Note, DecidedBy: by, DecidedAt: a.Now().UTC(),
		ProposedBy: o.ProposedBy, Rule: o.Rule}
	return d, d.Validate(k.Kind)
}

// Decide records one decision and pushes it. pushed is false when the remote
// was unreachable; the decision is committed locally and goes out next sync.
func (a *App) Decide(ctx context.Context, key, disposition string, o DecideOptions) (item.Decision, bool, error) {
	k, err := item.ParseKey(key)
	if err != nil {
		return item.Decision{}, false, err
	}
	d, err := a.decision(k, disposition, o)
	if err != nil {
		return d, false, fmt.Errorf("%s: %w", k, err)
	}
	if err := a.Repo.Decide(ctx, k, d); err != nil {
		return d, false, err
	}
	if o.NoPush {
		return d, false, nil
	}
	pushed, err := a.push(ctx)
	return d, pushed, err
}

// ErrNotDecided means Clear found no decision for the item: it is already
// undecided, so there is nothing to clear.
var ErrNotDecided = errors.New("the item has no decision")

// ErrStaleDecision means the item's decision isn't the one Clear was asked
// to remove: someone (an agent, a rule, another page) decided it since.
var ErrStaleDecision = errors.New("the decision changed since")

// Clear removes key's decision (items/<key>.toml) in one commit, but only
// when its decided_at is decidedAt: an undo never removes a decision made
// after the one it is undoing (ErrStaleDecision). ErrNotDecided when there
// is no decision. It does not push; serve's background push sends it.
func (a *App) Clear(ctx context.Context, key string, decidedAt time.Time) error {
	k, err := item.ParseKey(key)
	if err != nil {
		return err
	}
	want := decidedAt.UTC().Truncate(time.Second)
	// A refusal is returned from write, so nothing is committed.
	_, err = a.Repo.Batch(ctx, "clear "+k.String()+" by "+a.Cfg.User, func() error {
		d, err := a.Repo.ReadDecision(k)
		if err != nil {
			return err
		}
		if d == nil {
			return ErrNotDecided
		}
		if !d.DecidedAt.UTC().Truncate(time.Second).Equal(want) {
			return fmt.Errorf("%s: %w (now %s, decided at %s)", k, ErrStaleDecision, d.Disposition, d.DecidedAt.UTC().Format(time.RFC3339))
		}
		if err := os.Remove(filepath.Join(a.Repo.Dir, filepath.FromSlash(k.File()))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	})
	return err
}

// DecideBatch records every entry (one commit each) and pushes once. Invalid
// entries are reported and skipped.
func (a *App) DecideBatch(ctx context.Context, entries []TriageEntry, o DecideOptions) (int, []error, bool) {
	n := 0
	var errs []error
	for _, e := range entries {
		eo := o
		eo.Until, eo.Note, eo.NoPush = e.Until, e.Note, true
		if _, _, err := a.Decide(ctx, e.Key, e.Disposition, eo); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Key, err))
			continue
		}
		n++
	}
	if n == 0 || o.NoPush {
		return n, errs, false
	}
	pushed, err := a.push(ctx)
	if err != nil {
		errs = append(errs, err)
	}
	return n, errs, pushed
}

// Push sends local commits (decisions made with NoPush) to the casebook
// remote under this machine's sync lock, so it never runs beside a sync:
// ErrSyncBusy when a sync holds the lock (try again later), store.ErrOffline
// when the remote can't be reached (the commits stay queued for the next push
// or sync), store.ErrRefused when the remote refused the push (a hook, branch
// protection; queued too, with the remote's reason). serve's background push
// is its caller.
func (a *App) Push(ctx context.Context) error {
	unlock, err := LockSync()
	if err != nil {
		return err
	}
	defer unlock()
	_, err = a.Repo.Sync(ctx)
	return err
}

func (a *App) push(ctx context.Context) (bool, error) {
	res, err := a.Repo.Sync(ctx)
	if errors.Is(err, store.ErrOffline) {
		return false, nil
	}
	return res.Pushed, err
}
