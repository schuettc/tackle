package rules

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/propose"
)

// EvaluateActive proposes decisions for all items that every rule in active
// matches and that are eligible (undecided, no pending proposal from this rule,
// not rejected for this rule since its last edit). Rules whose Status is not
// StatusActive are skipped defensively (callers should pre-filter).
//
// The note is rendered per item, so each proposal carries fields specific to
// that item (e.g. {repo}, {how}, {tip}).
//
// Returns the number of new proposals created and a joined error collecting
// every Propose failure (e.g. closed DB, invalid disposition). Callers must
// not ignore the error: a non-nil return means some proposals were silently
// dropped.
func EvaluateActive(ctx context.Context, active []Rule, res engine.Result, now time.Time, props *propose.Store) (int, error) {
	pending, err := props.Pending(ctx)
	if err != nil {
		return 0, err
	}

	created := 0
	var allErrs []error
	for _, r := range active {
		// Defensive: skip non-active rules (caller should pre-filter, but we
		// never let a draft generate automatic proposals).
		if r.Status != StatusActive {
			continue
		}
		rejected, err := props.RejectedFor(ctx, "rule:"+r.ID, r.EditedAt)
		if err != nil {
			return created, err
		}
		matches := r.Proposable(res, now, pending, rejected)
		for _, m := range matches {
			it, ok := res.Find(m.Key)
			if !ok {
				continue
			}
			note := Render(r.Propose.Note, it.Fields(now), m)
			// Collect Propose errors instead of discarding them with "_".
			// A closed DB or a disposition that slips past rule Validate
			// would otherwise silently drop proposals.
			ps, propErrs := props.Propose(ctx, "rule:"+r.ID, []string{m.Key}, r.Propose.Disposition, r.Propose.Until, note)
			for _, pe := range propErrs {
				allErrs = append(allErrs, fmt.Errorf("rule %s: %w", r.ID, pe))
			}
			created += len(ps)
			// Keep the pending map current so later rules in this same pass
			// see newly created proposals and do not double-propose the same item.
			for _, p := range ps {
				p := p
				pending[p.Key] = p
			}
		}
	}
	return created, errors.Join(allErrs...)
}

// ProposeOnce proposes decisions for every item that r currently matches and
// that passes Proposable (undecided, no pending proposal from this rule,
// not rejected since the rule's last edit). It does not require r to be
// active; a draft rule can use ProposeOnce to preview its proposals (spec §4.2).
//
// Unlike EvaluateActive, ProposeOnce operates on a single rule and is the
// entry-point for Task 7's "propose once" endpoint.
func ProposeOnce(ctx context.Context, r Rule, res engine.Result, now time.Time, props *propose.Store) (int, error) {
	pending, err := props.Pending(ctx)
	if err != nil {
		return 0, err
	}
	rejected, err := props.RejectedFor(ctx, "rule:"+r.ID, r.EditedAt)
	if err != nil {
		return 0, err
	}
	matches := r.Proposable(res, now, pending, rejected)
	created := 0
	var propErrs []error
	for _, m := range matches {
		it, ok := res.Find(m.Key)
		if !ok {
			continue
		}
		note := Render(r.Propose.Note, it.Fields(now), m)
		// Collect Propose errors; they must not be silently discarded.
		ps, errs := props.Propose(ctx, "rule:"+r.ID, []string{m.Key}, r.Propose.Disposition, r.Propose.Until, note)
		for _, pe := range errs {
			propErrs = append(propErrs, fmt.Errorf("rule %s: %w", r.ID, pe))
		}
		created += len(ps)
	}
	return created, errors.Join(propErrs...)
}
