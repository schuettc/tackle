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

// Revision is what an undo compares a decision by: its disposition, until,
// note and decided_at, to the second (the stored precision).
type Revision struct {
	Disposition item.Disposition
	Until       string
	Note        string
	DecidedAt   time.Time
}

// RevisionOf is d's revision.
func RevisionOf(d item.Decision) Revision {
	return Revision{Disposition: d.Disposition, Until: d.Until, Note: d.Note, DecidedAt: d.DecidedAt.UTC().Truncate(time.Second)}
}

// Is reports whether d is this revision. A revision with no disposition
// (an older page's clear, which knows only decided_at) compares decided_at
// alone.
func (r Revision) Is(d item.Decision) bool {
	got := RevisionOf(d)
	if !got.DecidedAt.Equal(r.DecidedAt.UTC().Truncate(time.Second)) {
		return false
	}
	return r.Disposition == "" || (got.Disposition == r.Disposition && got.Until == r.Until && got.Note == r.Note)
}

// Clear removes key's decision (items/<key>.toml) in one commit, but only
// when it is still the expected revision: an undo never removes a decision
// made after the one it is undoing (ErrStaleDecision). ErrNotDecided when
// there is no decision. It does not push; serve's background push sends it.
func (a *App) Clear(ctx context.Context, key string, expect Revision) error {
	k, err := item.ParseKey(key)
	if err != nil {
		return err
	}
	// A refusal is returned from write, so nothing is committed.
	_, err = a.Repo.Batch(ctx, "clear "+k.String()+" by "+a.Cfg.User, func() error {
		d, err := a.Repo.ReadDecision(k)
		if err != nil {
			return err
		}
		if d == nil {
			return ErrNotDecided
		}
		if !expect.Is(*d) {
			return stale(k, d)
		}
		return a.removeDecision(k)
	})
	return err
}

func stale(k item.Key, d *item.Decision) error {
	if d == nil {
		return fmt.Errorf("%s: %w (now undecided)", k, ErrStaleDecision)
	}
	return fmt.Errorf("%s: %w (now %s, decided at %s)", k, ErrStaleDecision, d.Disposition, d.DecidedAt.UTC().Format(time.RFC3339))
}

func (a *App) removeDecision(k item.Key) error {
	if err := os.Remove(filepath.Join(a.Repo.Dir, filepath.FromSlash(k.File()))); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Undo takes back a decision in one commit under the store lock: when key's
// decision is still expect (every field, decided_at to the second), it
// decides restore (a new decision by o.By, now) or, when restore is nil,
// removes the decision. Otherwise ErrStaleDecision and nothing changes. It
// returns the decision it wrote (nil when it removed it). It does not push.
func (a *App) Undo(ctx context.Context, key string, expect Revision, restore *Revision, o DecideOptions) (*item.Decision, error) {
	k, err := item.ParseKey(key)
	if err != nil {
		return nil, err
	}
	if expect.Disposition == "" {
		return nil, fmt.Errorf("%s: undo needs the decision it expects", k)
	}
	var put *item.Decision
	var b []byte
	if restore != nil {
		d, err := a.decision(k, string(restore.Disposition), DecideOptions{Until: restore.Until, Note: restore.Note, By: o.By})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		if b, err = item.EncodeDecision(d); err != nil {
			return nil, err
		}
		d.DecidedAt = d.DecidedAt.UTC().Truncate(time.Second)
		put = &d
	}
	by := o.By
	if by == "" {
		by = a.Cfg.User
	}
	_, err = a.Repo.Batch(ctx, "undo "+k.String()+" by "+by, func() error {
		d, err := a.Repo.ReadDecision(k)
		if err != nil {
			return err
		}
		if d == nil || !expect.Is(*d) {
			return stale(k, d)
		}
		if put == nil {
			return a.removeDecision(k)
		}
		_, err = a.Repo.WriteFile(k.File(), b)
		return err
	})
	if err != nil {
		return nil, err
	}
	return put, nil
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
