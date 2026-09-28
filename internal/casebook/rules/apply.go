package rules

import (
	"context"
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
// Returns the number of new proposals created.
func EvaluateActive(ctx context.Context, active []Rule, res engine.Result, now time.Time, props *propose.Store) (int, error) {
	pending, err := props.Pending(ctx)
	if err != nil {
		return 0, err
	}

	created := 0
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
			ps, _ := props.Propose(ctx, "rule:"+r.ID, []string{m.Key}, r.Propose.Disposition, r.Propose.Until, note)
			created += len(ps)
			// Keep the pending map current so later rules in this same pass
			// see newly created proposals and do not double-propose the same item.
			for _, p := range ps {
				p := p
				pending[p.Key] = p
			}
		}
	}
	return created, nil
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
	for _, m := range matches {
		it, ok := res.Find(m.Key)
		if !ok {
			continue
		}
		note := Render(r.Propose.Note, it.Fields(now), m)
		ps, _ := props.Propose(ctx, "rule:"+r.ID, []string{m.Key}, r.Propose.Disposition, r.Propose.Until, note)
		created += len(ps)
	}
	return created, nil
}
