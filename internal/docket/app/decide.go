package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/schuettc/tackle/internal/docket/item"
	"github.com/schuettc/tackle/internal/docket/store"
	"github.com/schuettc/tools-common/harness"
)

// Actor names who is deciding: the harness session (by the family identity
// rule) when there is one, else the configured user.
func (a *App) Actor() string {
	id := harness.FromEnv()
	switch {
	case id.SessionID == "":
		return a.Cfg.User
	case id.SessionID == id.ClaudeID:
		return "claude:" + id.SessionID
	default:
		return "pi:" + id.SessionID
	}
}

// DecideOptions are the optional parts of a decision.
type DecideOptions struct {
	Until  string
	Note   string
	By     string // overrides Actor()
	NoPush bool
}

func (a *App) decision(k item.Key, disposition string, o DecideOptions) (item.Decision, error) {
	by := o.By
	if by == "" {
		by = a.Actor()
	}
	d := item.Decision{Disposition: item.Disposition(disposition), Until: o.Until, Note: o.Note, DecidedBy: by, DecidedAt: a.Now().UTC()}
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

func (a *App) push(ctx context.Context) (bool, error) {
	res, err := a.Repo.Sync(ctx)
	if errors.Is(err, store.ErrOffline) {
		return false, nil
	}
	return res.Pushed, err
}
