package engine

import (
	"sort"
	"strings"
	"time"

	"github.com/subaud-tools/tackle/internal/casebook/item"
)

// Fields holds every value that a rule Condition can read from one item.
// It is computed once per item per rule evaluation via Item.Fields.
type Fields struct {
	// Identity
	Kind     string
	Repo     string // "owner/name"
	Owner    string // first segment of Repo
	Relation string // PR/issue: "outgoing", "incoming", "own"; empty on every other kind (repos match with Fork)
	Status   string // item.Status string value
	Author   string
	Title    string
	Bot      bool     // author ends "[bot]" (GitHub's convention for bot accounts)
	Labels   []string // PR/issue labels

	// Age / activity (durations from now; 0 when the underlying time is zero)
	Age     time.Duration // now - CreatedAt
	Updated time.Duration // now - UpdatedAt (repo: pushedAt; branch: tipAt)

	// Landing / branch
	Landed       string   // "all-machines" | "some-machines" | "none" | "unknown"
	LandedHow    []string // machine codes: "default-branch", "merged-pr" (from Item.LandedVia)
	Tip          string   // tip SHA of the lexically first machine that landed (for {tip} template)
	GoneUpstream bool     // branch upstream was deleted on the remote
	Unpushed     bool     // has unpushed local-only commits
	Worktree     string   // worktree: "dirty" or "clean"; branch: its worktree's, or "none"; other kinds: empty

	// Repository attributes (set on repo items)
	Archived   bool
	Fork       bool
	OpenPRs    int
	OpenIssues int
}

// Fields computes the rule-matching field set for it at time now.
func (it Item) Fields(now time.Time) Fields {
	var age, updated time.Duration
	if !it.CreatedAt.IsZero() {
		age = now.Sub(it.CreatedAt)
	}
	if !it.UpdatedAt.IsZero() {
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

	// Worktree state: a worktree item's own; a branch's worktree's, or
	// "none". Other kinds have none to read.
	worktree := ""
	switch {
	case it.Kind == item.KindWorktree && it.dirty:
		worktree = "dirty"
	case it.Kind == item.KindWorktree:
		worktree = "clean"
	case it.Kind != item.KindBranch:
	case it.worktreeState != "":
		worktree = it.worktreeState
	default:
		worktree = "none"
	}

	// Relation is a PR's or issue's; a repo's org/owned/fork-of is not
	// matchable as relation (repos match with fork).
	relation := ""
	if it.Kind == item.KindPR || it.Kind == item.KindIssue {
		relation = it.Relation
	}

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

	return Fields{
		Kind:         string(it.Kind),
		Repo:         it.Repo,
		Owner:        owner,
		Relation:     relation,
		Status:       string(it.Status),
		Author:       it.Author,
		Title:        it.Title,
		Bot:          bot,
		Labels:       it.Labels,
		Age:          age,
		Updated:      updated,
		Landed:       it.Landed,
		LandedHow:    it.LandedVia,
		Tip:          tip,
		GoneUpstream: it.goneUpstream,
		Unpushed:     !it.oldestUnpushed.IsZero(),
		Worktree:     worktree,
		Archived:     it.Observed.Archived,
		Fork:         fork,
		OpenPRs:      it.openPRs,
		OpenIssues:   it.openIssues,
	}
}
