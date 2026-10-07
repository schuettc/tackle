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
	// StatusLeftOpen: kept ("Leave it open", "Keep it") and still open, with
	// nobody else's activity since the decision. It stays in its views, in
	// their left-open group, and needs no decision.
	StatusLeftOpen Status = "left-open"
)

// Observed is what the casebook last saw of an item.
type Observed struct {
	Known    bool   `json:"known"`              // there is an observation at all
	Exists   bool   `json:"exists"`             // repo/branch/worktree present; pr/issue found
	Archived bool   `json:"archived,omitempty"` // repo
	State    string `json:"state,omitempty"`    // pr/issue: OPEN, CLOSED or MERGED
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
//
// What the choices that leave an item alone leave behind (spec amendment
// 2026-10-07): a kept item that is still open is left open, and a kept PR or
// issue closed or merged since is done; a Not now (wait, watch) waits. Either
// is due again when someone else's activity on the PR or issue is newer than
// the decision, and a Not now also when its condition is met.
func Compute(k Key, d *Decision, obs Observed, f Facts, now time.Time, seenDone bool) Status {
	if d == nil {
		return StatusNew
	}
	if d.Conflict != nil {
		return StatusConflict
	}
	kept := d.Disposition == Keep
	notNow := d.Disposition == Wait || d.Disposition == Watch
	if kept && !stillOpen(k, obs) {
		return StatusDone
	}
	if conditionDue(k, d, f, now) {
		return StatusDue
	}
	if (kept || notNow) && NewActivity(k, d, f) {
		return StatusDue
	}
	if notNow && untilParses(d) {
		return StatusWaiting
	}
	if kept {
		return StatusLeftOpen
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

// DueReason says why an item with decision d is due (Compute's StatusDue):
// "" when it isn't due, or is due for no reason a decision gives.
func DueReason(k Key, d *Decision, obs Observed, f Facts, now time.Time) string {
	if d == nil || d.Conflict != nil || Compute(k, d, obs, f, now, false) != StatusDue {
		return ""
	}
	if c, err := ParseUntil(d.Until); err == nil {
		if met, known := c.Met(k, d.DecidedAt, f, now); known && met {
			return "its condition was met"
		}
	}
	if ref, gone := MissingRef(d, f); gone {
		return "its condition names " + ref.String() + ", which GitHub can't find"
	}
	if NewActivity(k, d, f) {
		if d.Disposition == Keep {
			return "new activity since you left it open"
		}
		return "new activity since Not now"
	}
	return ""
}

// NewActivity reports that someone else's activity on PR or issue k is newer
// than decision d. Branches, worktrees and repos have none.
func NewActivity(k Key, d *Decision, f Facts) bool {
	if d == nil || f == nil || (k.Kind != KindPR && k.Kind != KindIssue) {
		return false
	}
	at, ok := f.OthersActivity(k)
	return ok && at.After(d.DecidedAt)
}

// stillOpen reports that a kept item is still there to leave open: a PR or
// issue open on GitHub; a branch, worktree or repo that exists.
func stillOpen(k Key, obs Observed) bool {
	if !obs.Known || !obs.Exists {
		return false
	}
	if k.Kind == KindPR || k.Kind == KindIssue {
		return obs.State == "OPEN"
	}
	return true
}

// conditionDue reports that d's until condition is met, or names something
// GitHub can't find (which would hide the item for good: it asks again,
// never counted as met).
func conditionDue(k Key, d *Decision, f Facts, now time.Time) bool {
	c, err := ParseUntil(d.Until)
	if d.Until == "" || err != nil {
		return false
	}
	if met, known := c.Met(k, d.DecidedAt, f, now); known && met {
		return true
	}
	_, gone := MissingRef(d, f)
	return gone
}

func untilParses(d *Decision) bool {
	_, err := ParseUntil(d.Until)
	return d.Until != "" && err == nil
}
