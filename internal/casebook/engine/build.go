// Package engine combines decisions, observations and policy into items with
// a computed status: the casebook's view of the world. It is pure: no I/O.
package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/observe"
)

// Input is everything a build reads.
type Input struct {
	Now       time.Time
	GitHub    *observe.GitHub
	Snapshots []observe.Snapshot
	Decisions map[string]item.Decision
	Policy    item.Policy
	Seen      map[string]time.Time // item key → decided_at of a decision observed done
}

// Item is one tracked thing with its computed status.
type Item struct {
	Key         item.Key          `json:"-"`
	ID          string            `json:"key"`
	Kind        item.Kind         `json:"kind"`
	Repo        string            `json:"repo,omitempty"`
	Title       string            `json:"title,omitempty"`
	URL         string            `json:"url,omitempty"`
	Relation    string            `json:"relation,omitempty"`
	Status      item.Status       `json:"status"`
	DueReason   string            `json:"due_reason,omitempty"` // why a Not now came back other than its condition being met
	Decision    *item.Decision    `json:"decision,omitempty"`
	Hits        []item.Hit        `json:"hits,omitempty"`
	Observed    item.Observed     `json:"observed"`
	Stale       bool              `json:"stale,omitempty"`
	Locations   []string          `json:"locations,omitempty"`
	Evidence    []string          `json:"evidence,omitempty"`
	Author      string            `json:"author,omitempty"`
	AuthorIsBot bool              `json:"author_is_bot,omitempty"` // true when GitHub GraphQL __typename == "Bot"
	CreatedAt   time.Time         `json:"created_at,omitzero"`
	UpdatedAt   time.Time         `json:"updated_at,omitzero"`
	Labels      []string          `json:"labels,omitempty"`
	Body        string            `json:"body,omitempty"`
	Landed      string            `json:"landed,omitempty"`      // combined across machines: "all-machines", "some-machines", "none", "unknown"
	LandedHow   string            `json:"landed_how,omitempty"`  // human-readable: "in main", "merged #N", or "; "-joined when both
	LandedVia   []string          `json:"landed_via,omitempty"`  // machine codes: "default-branch", "merged-pr" (multi-valued)
	LandedTips  map[string]string `json:"landed_tips,omitempty"` // machine → tip checked

	fresh         bool // observed from a fresh owner listing
	signals       item.Signals
	goneUpstream  bool   // branch: remote upstream was deleted
	worktreeState string // branch: "dirty", "clean", or "" (none)
	dirty         bool   // worktree: has uncommitted changes
	openPRs       int    // repo: number of open PRs
	openIssues    int    // repo: number of open issues
}

// Result is a build.
type Result struct {
	Items   []Item   `json:"items"`
	Notices []string `json:"notices,omitempty"`
}

// Find returns the item with key text id.
func (r Result) Find(id string) (Item, bool) {
	i := sort.Search(len(r.Items), func(i int) bool { return r.Items[i].ID >= id })
	if i < len(r.Items) && r.Items[i].ID == id {
		return r.Items[i], true
	}
	return Item{}, false
}

// Attention returns items that are new, due, drifted, conflicted, or have policy hits.
func (r Result) Attention() []Item {
	var out []Item
	for _, it := range r.Items {
		switch {
		case it.Status == item.StatusNew, it.Status == item.StatusDue, it.Status == item.StatusDrift, it.Status == item.StatusConflict, len(it.Hits) > 0:
			out = append(out, it)
		}
	}
	return out
}

// machineLanded records the per-machine landed verdict for one branch item.
type machineLanded struct {
	Machine     string
	Landed      string // "in main", "merged #N", or "" (not landed)
	LandedState string // "yes" | "no" | "unknown" | "" (empty = snapshot written before LandedState existed)
	Tip         string // tip SHA; non-empty only when LandedState == "yes"
	HowType     string // "default-branch" or "merged-pr" (machine-readable; from Branch.LandedHow)
}

type builder struct {
	in             Input
	user           string
	items          map[string]*Item
	f              facts
	local          map[string]bool            // branch keys present in some snapshot
	fresh          map[string]bool            // lower-case owners with a fresh, complete listing
	stale          map[string]bool            // lower-case owners whose listing is stale
	snaps          map[string]bool            // machines with a snapshot
	branchLanded   map[string][]machineLanded // branch item ID → per-machine verdicts
	branchWorktree map[string]string          // branch item ID → "dirty" or "clean"
}

func (b *builder) get(k item.Key) *Item {
	id := k.String()
	it := b.items[id]
	if it == nil {
		it = &Item{Key: k, ID: id, Kind: k.Kind, Repo: k.Repo()}
		it.signals.Kind = k.Kind
		b.items[id] = it
	}
	return it
}

// Build computes every item.
func Build(in Input) Result {
	if in.GitHub == nil {
		in.GitHub = observe.NewGitHub()
	}
	b := &builder{
		in:    in,
		user:  strings.ToLower(in.GitHub.User),
		items: map[string]*Item{},
		f: facts{
			repos: map[string]observe.RepoObs{},
			prs:   map[string]observe.PRObs{},
			refs:  in.GitHub.Refs,
		},
		local:          map[string]bool{},
		fresh:          map[string]bool{},
		stale:          map[string]bool{},
		snaps:          map[string]bool{},
		branchLanded:   map[string][]machineLanded{},
		branchWorktree: map[string]string{},
	}
	b.f.items = b.items
	var res Result
	b.owners(&res)
	b.authored()
	b.snapshots()
	for id, d := range in.Decisions {
		k, err := item.ParseKey(id)
		if err != nil {
			continue
		}
		dc := d
		b.get(k).Decision = &dc
	}
	b.unobserved()
	for _, it := range b.items {
		b.finish(it)
		res.Items = append(res.Items, *it)
	}
	sort.Slice(res.Items, func(i, j int) bool { return res.Items[i].ID < res.Items[j].ID })
	sort.Strings(res.Notices)
	return res
}

func (b *builder) owners(res *Result) {
	for key, ow := range b.in.GitHub.Owners {
		switch {
		case !ow.Reachable:
			res.Notices = append(res.Notices, fmt.Sprintf("owner %s unreachable: %s (its items show last known state)", ow.Login, ow.Reason))
		case ow.Stale:
			res.Notices = append(res.Notices, fmt.Sprintf("owner %s not refreshed since %s: %s", ow.Login, ow.FetchedAt.Format("2006-01-02"), ow.Reason))
		}
		isFresh := ow.Reachable && !ow.Stale
		b.fresh[key], b.stale[key] = isFresh, !isFresh
		for _, r := range ow.Repos {
			k := item.RepoKey(r.Repo)
			it := b.get(k)
			it.Observed = item.Observed{Known: true, Exists: true, Archived: r.Archived}
			it.fresh, it.Stale = isFresh, !isFresh
			it.URL = "https://github.com/" + r.Repo
			switch {
			case key != b.user:
				it.Relation = "org"
			case r.Fork:
				it.Relation = "fork-of:" + strings.ToLower(r.Parent)
			default:
				it.Relation = "owned"
			}
			it.signals.UpdatedAt, it.signals.Archived = r.PushedAt, r.Archived
			it.UpdatedAt = r.PushedAt
			if !r.PushedAt.IsZero() {
				it.Evidence = append(it.Evidence, "pushed "+r.PushedAt.Format("2006-01-02"))
			}
			if r.Archived {
				it.Evidence = append(it.Evidence, "archived")
			}
			it.openPRs, it.openIssues = len(r.PRs), len(r.Issues)
			if n := len(r.PRs); n > 0 {
				it.Evidence = append(it.Evidence, fmt.Sprintf("%d open PRs", n))
			}
			b.f.repos[k.String()] = r
			for _, p := range r.PRs {
				b.pr(item.PRKey(p.Repo, p.Number), p, b.direction(p), isFresh)
			}
			for _, p := range r.Issues {
				b.pr(item.IssueKey(p.Repo, p.Number), p, b.direction(p), isFresh)
			}
		}
	}
}

func (b *builder) direction(p observe.PRObs) string {
	if strings.EqualFold(p.Author, b.user) {
		return "own"
	}
	return "incoming"
}

func (b *builder) authored() {
	for _, p := range b.in.GitHub.Authored {
		k := item.PRKey(p.Repo, p.Number)
		if _, seen := b.items[k.String()]; seen {
			continue
		}
		dir := "outgoing"
		if _, owned := b.in.GitHub.Owners[k.Owner]; owned {
			dir = "own"
		}
		b.pr(k, p, dir, true)
	}
}

func (b *builder) pr(k item.Key, p observe.PRObs, dir string, fresh bool) {
	it := b.get(k)
	it.Observed = item.Observed{Known: true, Exists: true, State: p.State}
	it.fresh, it.Stale = fresh, !fresh
	it.Title, it.URL, it.Relation = p.Title, p.URL, dir
	it.Author, it.AuthorIsBot, it.CreatedAt, it.UpdatedAt, it.Labels, it.Body = p.Author, p.AuthorIsBot, p.CreatedAt, p.UpdatedAt, p.Labels, p.Body
	last := p.CreatedAt
	if p.LastCommentAt.After(last) {
		last = p.LastCommentAt
	}
	s := &it.signals
	s.Direction, s.Open, s.UpdatedAt, s.CreatedAt, s.LastActivity = dir, p.State == "OPEN", p.UpdatedAt, p.CreatedAt, last
	if strings.EqualFold(p.LastCommentAuthor, b.user) {
		s.LastReplyByMe = p.LastCommentAt
	}
	it.Evidence = append(it.Evidence, fmt.Sprintf("%s by %s, updated %s", strings.ToLower(p.State), p.Author, p.UpdatedAt.Format("2006-01-02")))
	b.f.prs[k.String()] = p
}

func (b *builder) snapshots() {
	for _, snap := range b.in.Snapshots {
		b.snaps[snap.Machine] = true
		for _, c := range snap.Clones {
			where := snap.Machine + ":" + c.Path
			branches := map[string]observe.Branch{}
			for _, br := range c.Branches {
				branches[br.Name] = br
			}
			for _, w := range c.Worktrees {
				it := b.get(item.WorktreeKey(snap.Machine, w.Path))
				it.Observed = item.Observed{Known: true, Exists: true}
				it.fresh, it.Repo, it.Title = true, c.Repo, w.Branch
				it.Locations = append(it.Locations, where)
				if w.Dirty {
					it.dirty = true
					it.Evidence = append(it.Evidence, "uncommitted changes")
				}
				if br, ok := branches[w.Branch]; ok {
					if br.Unpushed > 0 && !br.Gone {
						it.signals.OldestUnpushed, it.signals.UnpushedWhere = br.OldestUnpushed, snap.Machine+":"+w.Path
					}
					if !br.TipAt.IsZero() {
						it.UpdatedAt = br.TipAt
					}
				}
				// Track worktree state for the branch item.
				if c.Repo != "" && w.Branch != "" {
					bk := item.BranchKey(c.Repo, w.Branch).String()
					if w.Dirty {
						b.branchWorktree[bk] = "dirty" // dirty overrides clean
					} else if b.branchWorktree[bk] == "" {
						b.branchWorktree[bk] = "clean"
					}
				}
			}
			if c.Repo == "" {
				continue
			}
			def := "main"
			if r, ok := b.f.repos[item.RepoKey(c.Repo).String()]; ok && r.DefaultBranch != "" {
				def = r.DefaultBranch
			}
			for _, br := range c.Branches {
				if br.Name == def || br.Name == "master" || br.Name == "main" {
					continue
				}
				k := item.BranchKey(c.Repo, br.Name)
				it := b.get(k)
				b.local[k.String()] = true
				it.Observed = item.Observed{Known: true, Exists: true}
				it.fresh = true
				it.Locations = append(it.Locations, where)
				// Carry tip time.
				if !br.TipAt.IsZero() && (it.UpdatedAt.IsZero() || br.TipAt.After(it.UpdatedAt)) {
					it.UpdatedAt = br.TipAt
				}
				// Record per-machine landed status for cross-machine combination.
				// Treat an empty LandedState (snapshot written before the field
				// existed) as "unknown" so we don't falsely count it as "no".
				ls := br.LandedState
				if ls == "" && br.Landed != "" {
					ls = "yes" // old snapshot that set Landed but not LandedState
				}
				b.branchLanded[k.String()] = append(b.branchLanded[k.String()], machineLanded{
					Machine:     snap.Machine,
					Landed:      br.Landed,
					LandedState: ls,
					Tip:         br.LandedTip, // non-empty only when LandedState=="yes"
					HowType:     br.LandedHow, // "default-branch" or "merged-pr"
				})
				if br.Gone {
					// Upstream deleted on the remote: usually a squash-merged PR
					// branch. Its local-only commits are not work at risk.
					it.Evidence = append(it.Evidence, "upstream branch deleted (likely merged) on "+where)
					it.goneUpstream = true
					continue
				}
				if br.Unpushed > 0 {
					it.Evidence = append(it.Evidence, fmt.Sprintf("%d unpushed on %s", br.Unpushed, where))
					if it.signals.OldestUnpushed.IsZero() || br.OldestUnpushed.Before(it.signals.OldestUnpushed) {
						it.signals.OldestUnpushed, it.signals.UnpushedWhere = br.OldestUnpushed, where
					}
				}
			}
		}
	}
}

// finishLanded computes it.Landed, it.LandedHow, and it.LandedTips from
// the per-machine verdicts collected in b.branchLanded during snapshots().
// Combined semantics (spec §4.1):
//
//   - "all-machines": landed on every machine (LandedState=="yes" everywhere)
//   - "some-machines": landed>0 && notLanded>0 (regardless of unknown count)
//   - "none": no landed, notLanded>0, no unknown
//   - "unknown": any other combination (any unknown with no not-landed contradicting it)
//
// LandedTips holds only the tips of machines whose LandedState=="yes".
func (b *builder) finishLanded(it *Item) {
	if it.Kind != item.KindBranch {
		return
	}
	verdicts := b.branchLanded[it.ID]
	if len(verdicts) == 0 {
		return
	}
	var (
		landedCount    int
		notLandedCount int
		unknownCount   int
		reasons        []string
		howTypes       []string
		tips           = map[string]string{}
	)
	seen := map[string]bool{}
	seenHow := map[string]bool{}
	for _, ml := range verdicts {
		switch ml.LandedState {
		case "yes":
			landedCount++
			if ml.Tip != "" {
				tips[ml.Machine] = ml.Tip
			}
			if ml.Landed != "" && !seen[ml.Landed] {
				seen[ml.Landed] = true
				reasons = append(reasons, ml.Landed)
			}
			if ml.HowType != "" && !seenHow[ml.HowType] {
				seenHow[ml.HowType] = true
				howTypes = append(howTypes, ml.HowType)
			}
		case "no":
			notLandedCount++
		default: // "unknown" or "" (snapshot from before LandedState was added)
			unknownCount++
		}
	}
	if len(tips) > 0 {
		it.LandedTips = tips
	}
	// LandedVia holds the machine-readable how codes for rule conditions.
	// LandedHow holds the human-readable text for display (templates, page).
	if len(howTypes) > 0 {
		it.LandedVia = howTypes
	}
	if len(reasons) > 0 {
		it.LandedHow = strings.Join(reasons, "; ")
	}
	switch {
	case landedCount > 0 && notLandedCount == 0 && unknownCount == 0:
		it.Landed = "all-machines"
	case landedCount > 0 && notLandedCount > 0:
		it.Landed = "some-machines" // regardless of unknown count
	case landedCount == 0 && notLandedCount > 0 && unknownCount == 0:
		it.Landed = "none"
	default:
		it.Landed = "unknown"
	}
}

// unobserved fills observations for items that no listing or snapshot
// produced, from individual lookups and the two safe absence inferences.
func (b *builder) unobserved() {
	for id, it := range b.items {
		if it.fresh {
			continue
		}
		if r, ok := b.in.GitHub.Refs[id]; ok && it.Kind != item.KindWorktree {
			it.Observed = item.Observed{Known: true, Exists: r.Exists, Archived: r.Archived, State: r.State}
			it.Stale = false
			continue
		}
		if it.Observed.Known {
			continue // a stale listing: keep it, marked Stale
		}
		switch it.Kind {
		case item.KindRepo:
			if b.fresh[it.Key.Owner] && it.Decision != nil {
				it.Observed = item.Observed{Known: true, Exists: false}
			} else if b.stale[it.Key.Owner] {
				it.Stale = true
			}
		case item.KindWorktree:
			if b.snaps[it.Key.Machine] {
				it.Observed = item.Observed{Known: true, Exists: false}
			}
		}
	}
}

func (b *builder) finish(it *Item) {
	seen := it.Decision != nil && b.in.Seen[it.ID].Equal(it.Decision.DecidedAt) && !it.Decision.DecidedAt.IsZero()
	it.Status = item.Compute(it.Key, it.Decision, it.Observed, b.f, b.in.Now, seen)
	if ref, gone := item.MissingRef(it.Decision, b.f); gone && it.Status == item.StatusDue {
		it.DueReason = "its condition names " + ref.String() + ", which GitHub can't find"
	}
	// Undecided is true when the item has no decision at all, OR when a
	// wait/watch decision has lapsed (its until condition is now met) — meaning
	// the item has returned to attention and Court needs to decide again.
	// A non-lapsed wait (StatusWaiting) is NOT undecided: Court has decided to
	// wait, and that decision is still active.
	it.signals.Undecided = it.Decision == nil ||
		((it.Decision.Disposition == item.Wait || it.Decision.Disposition == item.Watch) &&
			it.Status != item.StatusWaiting)
	ignored := it.Decision != nil && it.Decision.Disposition == item.Ignore
	if it.Observed.Known && it.Observed.Exists {
		it.Hits = b.in.Policy.Evaluate(it.signals, ignored, b.in.Now)
	}
	sort.Strings(it.Locations)
	// Set worktreeState on branch items from the worktree tracking map.
	if it.Kind == item.KindBranch {
		if state, ok := b.branchWorktree[it.ID]; ok {
			it.worktreeState = state
		}
	}
	b.finishLanded(it)
}

// NextSeen records decisions observed done, for drift detection. An entry
// survives while the same decision stands.
func NextSeen(r Result, prev map[string]time.Time) map[string]time.Time {
	out := map[string]time.Time{}
	for _, it := range r.Items {
		if it.Decision == nil {
			continue
		}
		at := it.Decision.DecidedAt
		if p, ok := prev[it.ID]; ok && p.Equal(at) {
			out[it.ID] = at
			continue
		}
		switch it.Decision.Disposition {
		case item.Archive, item.Close, item.Merge, item.Delete:
			if it.Status == item.StatusDone {
				out[it.ID] = at
			}
		}
	}
	return out
}

// LookupKeys lists the keys GitHub should be asked about individually: every
// decided repo, pr, issue and branch, and every key an until condition names.
func LookupKeys(decisions map[string]item.Decision) []item.Key {
	set := map[string]item.Key{}
	add := func(k item.Key) {
		if k.Kind != item.KindWorktree {
			set[k.String()] = k
		}
	}
	for id, d := range decisions {
		if k, err := item.ParseKey(id); err == nil {
			add(k)
		}
		if c, err := item.ParseUntil(d.Until); err == nil && c.Ref.Kind != "" {
			add(c.Ref)
		}
	}
	keys := make([]item.Key, 0, len(set))
	for _, k := range set {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	return keys
}
