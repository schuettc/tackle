package item

import "time"

// Status is computed from a decision plus observation; it is never stored.
type Status string

// Statuses.
const (
	StatusNew      Status = "new"
	StatusToApply  Status = "to-apply"
	StatusWaiting  Status = "waiting"
	StatusDue      Status = "due"
	StatusDone     Status = "done"
	StatusDrift    Status = "drift"
	StatusConflict Status = "conflict"
)

// Observed is what the ledger last saw of an item.
type Observed struct {
	Known    bool   // there is an observation at all
	Exists   bool   // repo/branch/worktree present; pr/issue found
	Archived bool   // repo
	State    string // pr/issue: OPEN, CLOSED or MERGED
}

// Satisfied reports whether obs already reflects disposition d. Dispositions
// that need nothing applied (keep, wait, watch, ignore) are always satisfied.
func Satisfied(d Disposition, obs Observed) bool {
	switch d {
	case Archive:
		return obs.Known && obs.Exists && obs.Archived
	case Close:
		return obs.Known && (obs.State == "CLOSED" || obs.State == "MERGED")
	case Merge:
		return obs.Known && obs.State == "MERGED"
	case Delete:
		return obs.Known && !obs.Exists
	}
	return true
}

// Compute returns item k's status. seenDone reports that this same decision
// (same decided_at) was observed satisfied before, so an unsatisfied
// observation now is drift rather than to-apply (and a missing observation
// stays done).
func Compute(k Key, d *Decision, obs Observed, f Facts, now time.Time, seenDone bool) Status {
	if d == nil {
		return StatusNew
	}
	if d.Conflict != nil {
		return StatusConflict
	}
	if d.Until != "" {
		if c, err := ParseUntil(d.Until); err == nil {
			if met, known := c.Met(k, d.DecidedAt, f, now); known && met {
				return StatusDue
			}
			if d.Disposition == Wait || d.Disposition == Watch {
				return StatusWaiting
			}
		}
	}
	if Satisfied(d.Disposition, obs) {
		return StatusDone
	}
	if seenDone {
		if !obs.Known {
			return StatusDone // last known state; missing data is not drift
		}
		return StatusDrift
	}
	return StatusToApply
}
