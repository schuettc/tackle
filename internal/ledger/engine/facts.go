package engine

import (
	"time"

	"github.com/schuettc/tackle/internal/ledger/item"
	"github.com/schuettc/tackle/internal/ledger/observe"
)

// facts answers until conditions from the built items plus individual lookups.
type facts struct {
	items map[string]*Item
	repos map[string]observe.RepoObs
	prs   map[string]observe.PRObs
	refs  map[string]observe.Ref
}

// State returns the state of item k if it is known.
func (f facts) State(k item.Key) (string, bool) {
	if it := f.items[k.String()]; it != nil && it.Observed.Known && it.Observed.Exists && it.Observed.State != "" {
		return it.Observed.State, true
	}
	if r, ok := f.refs[k.String()]; ok && r.Exists {
		return r.State, true
	}
	return "", false
}

// LatestRelease returns the time of the latest release for item k if known.
func (f facts) LatestRelease(k item.Key) (time.Time, bool) {
	if r, ok := f.repos[k.String()]; ok {
		return r.LatestRelease, true
	}
	if r, ok := f.refs[k.String()]; ok && r.Exists {
		return r.LatestRelease, true
	}
	return time.Time{}, false
}

// LastActivity returns the time of the most recent activity for item k if known.
func (f facts) LastActivity(k item.Key) (time.Time, bool) {
	if r, ok := f.repos[k.String()]; ok && !r.PushedAt.IsZero() {
		return r.PushedAt, true
	}
	if p, ok := f.prs[k.String()]; ok && !p.UpdatedAt.IsZero() {
		return p.UpdatedAt, true
	}
	if r, ok := f.refs[k.String()]; ok && r.Exists && !r.UpdatedAt.IsZero() {
		return r.UpdatedAt, true
	}
	return time.Time{}, false
}
