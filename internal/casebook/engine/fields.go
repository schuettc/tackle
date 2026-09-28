package engine

import (
	"sort"
	"strings"
	"time"
)

// Fields holds every value that a rule Condition can read from one item.
// It is computed once per item per rule evaluation via Item.Fields.
type Fields struct {
	// Identity
	Kind      string
	Repo      string // "owner/name"
	Owner     string // first segment of Repo
	Relation  string // PR/issue: "outgoing", "incoming", "own"; repo: "org", "owned", "fork-of:X"
	Status    string // item.Status string value
	Direction string // alias for Relation (same vocab field, different name)
	Author    string
	Title     string
	Bot       bool     // author ends "[bot]" (GitHub's convention for bot accounts)
	Labels    []string // PR/issue labels

	// Age / activity (durations from now; 0 when the underlying time is zero)
	Age     time.Duration // now - CreatedAt
	Pushed  time.Duration // now - UpdatedAt (repo: pushedAt; branch: tipAt)
	Updated time.Duration // now - UpdatedAt

	// Landing / branch
	Landed       string   // "all-machines" | "some-machines" | "none" | "unknown"
	LandedHow    []string // machine codes: "default-branch", "merged-pr" (from Item.LandedVia)
	Tip          string   // tip SHA of the lexically first machine that landed (for {tip} template)
	GoneUpstream bool     // branch upstream was deleted on the remote
	Unpushed     bool     // has unpushed local-only commits
	Dirty        bool     // worktree item: has uncommitted changes; branch: has dirty worktree
	Worktree     string   // branch: "dirty", "clean", or "none"

	// Repository attributes (set on repo items)
	Archived   bool
	Fork       bool
	OpenPRs    int
	OpenIssues int

	// Decision / policy
	HasDecision bool
	PolicyHits  []string // names of policy rules that fired (for "policy-hit" field)
}

// Fields computes the rule-matching field set for it at time now.
func (it Item) Fields(now time.Time) Fields {
	var age, pushed, updated time.Duration
	if !it.CreatedAt.IsZero() {
		age = now.Sub(it.CreatedAt)
	}
	if !it.UpdatedAt.IsZero() {
		pushed = now.Sub(it.UpdatedAt)
		updated = now.Sub(it.UpdatedAt)
	}

	// Owner is the first "/" segment of Repo.
	owner := it.Repo
	if i := strings.IndexByte(it.Repo, '/'); i >= 0 {
		owner = it.Repo[:i]
	}

	// Bot: derived from AuthorIsBot (set from GraphQL __typename == "Bot").
	// No suffix check — GitHub Bot accounts may not carry the [bot] login suffix.
	bot := it.AuthorIsBot

	// Fork: repo items whose Relation starts with "fork-of:".
	fork := strings.HasPrefix(it.Relation, "fork-of:")

	// Worktree state for branch items.
	worktree := "none"
	if it.worktreeState != "" {
		worktree = it.worktreeState
	}

	// dirty: worktree items track dirty directly; branch items have a dirty
	// worktree when worktreeState == "dirty".
	dirty := it.dirty || it.worktreeState == "dirty"

	// Tip: tip SHA of the lexically first machine in LandedTips (deterministic).
	var tip string
	if len(it.LandedTips) > 0 {
		machines := make([]string, 0, len(it.LandedTips))
		for m := range it.LandedTips {
			machines = append(machines, m)
		}
		sort.Strings(machines)
		tip = it.LandedTips[machines[0]]
	}

	// Policy hit rule names from the computed hits slice.
	var policyHits []string
	for _, h := range it.Hits {
		policyHits = append(policyHits, h.Rule)
	}

	return Fields{
		Kind:         string(it.Kind),
		Repo:         it.Repo,
		Owner:        owner,
		Relation:     it.Relation,
		Status:       string(it.Status),
		Direction:    it.Relation,
		Author:       it.Author,
		Title:        it.Title,
		Bot:          bot,
		Labels:       it.Labels,
		Age:          age,
		Pushed:       pushed,
		Updated:      updated,
		Landed:       it.Landed,
		LandedHow:    it.LandedVia,
		Tip:          tip,
		GoneUpstream: it.goneUpstream,
		Unpushed:     !it.signals.OldestUnpushed.IsZero(),
		Dirty:        dirty,
		Worktree:     worktree,
		Archived:     it.Observed.Archived,
		Fork:         fork,
		OpenPRs:      it.openPRs,
		OpenIssues:   it.openIssues,
		HasDecision:  it.Decision != nil,
		PolicyHits:   policyHits,
	}
}
