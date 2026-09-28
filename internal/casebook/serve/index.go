package serve

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/propose"
)

// Index is the live item index: the engine's build over the GitHub cache,
// the snapshots and the decisions, rebuilt when the casebook repo's HEAD moves
// or a decision is made here.
type Index struct {
	mu      sync.RWMutex
	res     engine.Result
	byKey   map[string]engine.Item
	builtAt time.Time
	head    string
}

func (x *Index) set(r engine.Result, head string, at time.Time) {
	m := make(map[string]engine.Item, len(r.Items))
	for _, it := range r.Items {
		m[it.ID] = it
	}
	x.mu.Lock()
	x.res, x.byKey, x.head, x.builtAt = r, m, head, at
	x.mu.Unlock()
}

// Head is the casebook repo commit the index was built from.
func (x *Index) Head() string {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.head
}

// Item returns one item.
func (x *Index) Item(key string) (engine.Item, bool) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	it, ok := x.byKey[key]
	return it, ok
}

// Views of the attention list (casebook workbench spec §3.2).
const (
	ViewWaiting  = "waiting"  // policy: incoming, no reply from you
	ViewNew      = "new"      // undecided
	ViewDue      = "due"      // due, drift, conflict
	ViewProposed = "proposed" // a proposal is pending
	ViewAll      = "all"      // all attention
)

// Query filters a view.
type Query struct {
	View   string
	Kind   string
	Repo   string // owner/name or owner
	Text   string // substring of key or title
	Offset int
	Limit  int
}

// ItemView is an item as the page and the agent see it.
type ItemView struct {
	engine.Item
	Proposal *propose.Proposal `json:"proposal,omitempty"`
}

func inView(view string, it engine.Item, pending map[string]propose.Proposal) bool {
	switch view {
	case ViewWaiting:
		for _, h := range it.Hits {
			if h.Rule == "incoming-no-reply" {
				return true
			}
		}
		return false
	case ViewNew:
		return it.Status == item.StatusNew
	case ViewDue:
		return it.Status == item.StatusDue || it.Status == item.StatusDrift || it.Status == item.StatusConflict
	case ViewProposed:
		_, ok := pending[it.ID]
		return ok
	}
	return true
}

func matches(q Query, it engine.Item) bool {
	if q.Kind != "" && string(it.Kind) != q.Kind {
		return false
	}
	if q.Repo != "" && it.Repo != strings.ToLower(q.Repo) && !strings.HasPrefix(it.Repo, strings.ToLower(q.Repo)+"/") {
		return false
	}
	if q.Text != "" {
		t := strings.ToLower(q.Text)
		if !strings.Contains(strings.ToLower(it.ID), t) && !strings.Contains(strings.ToLower(it.Title), t) {
			return false
		}
	}
	return true
}

// List returns the page of items in a view and the view's total.
func (x *Index) List(q Query, pending map[string]propose.Proposal) ([]ItemView, int) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	src := x.res.Attention()
	var out []ItemView
	for _, it := range src {
		if !inView(q.View, it, pending) || !matches(q, it) {
			continue
		}
		v := ItemView{Item: it}
		if p, ok := pending[it.ID]; ok {
			p := p
			v.Proposal = &p
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	total := len(out)
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 200
	}
	if q.Offset > total {
		q.Offset = total
	}
	end := min(q.Offset+q.Limit, total)
	return out[q.Offset:end], total
}

// Counts returns each view's size.
func (x *Index) Counts(pending map[string]propose.Proposal) map[string]int {
	x.mu.RLock()
	defer x.mu.RUnlock()
	c := map[string]int{}
	for _, it := range x.res.Attention() {
		for _, v := range []string{ViewWaiting, ViewNew, ViewDue, ViewProposed, ViewAll} {
			if inView(v, it, pending) {
				c[v]++
			}
		}
	}
	return c
}

// Notices are the engine's notices (unreachable owners and the like).
func (x *Index) Notices() []string {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.res.Notices
}

// Result returns the current engine.Result. Callers that need to evaluate
// rules against the live index use this to get a consistent snapshot.
func (x *Index) Result() engine.Result {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.res
}

// appendNotices appends additional notices (e.g. rule-load errors) to the
// index so the page can display them alongside engine notices.
func (x *Index) appendNotices(notices []string) {
	if len(notices) == 0 {
		return
	}
	x.mu.Lock()
	x.res.Notices = append(x.res.Notices, notices...)
	x.mu.Unlock()
}

// BuiltAt is when the index was last built.
func (x *Index) BuiltAt() time.Time {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.builtAt
}

// rebuild recomputes the index; build is app.Build.
func (x *Index) rebuild(ctx context.Context, build func(context.Context) (engine.Result, error), head string, now time.Time) error {
	r, err := build(ctx)
	if err != nil {
		return err
	}
	x.set(r, head, now)
	return nil
}
