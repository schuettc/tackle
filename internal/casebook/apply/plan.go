// Package apply builds execution plans from decided to-apply items and renders
// the exact shell commands for every step. It is pure: no I/O.
//
// Two lanes carry steps:
//   - casebook: mechanical, local, reversible steps run by casebook itself
//     (local branch delete, remote branch delete, worktree remove).
//   - agent: outward or judgment steps handed to the chosen agent session
//     (repo archive/delete, PR close/merge, issue close).
package apply

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/observe"
)

// Lane identifies which execution lane a step belongs to.
type Lane string

const (
	// LaneCasebook covers mechanical, local, reversible steps run by casebook.
	LaneCasebook Lane = "casebook"
	// LaneAgent covers outward or judgment steps handed to the agent session.
	LaneAgent Lane = "agent"
)

// Step is one action inside a Plan.
type Step struct {
	// Key is the item key this step acts on (e.g. "branch:owner/name@branch").
	Key string `json:"key"`
	// Action is a machine-readable verb identifying the step type.
	Action string `json:"action"`
	// Lane is the execution lane (casebook or agent).
	Lane Lane `json:"lane"`
	// Command is the exact, copy-pasteable shell command for this step.
	Command string `json:"command"`
	// Precondition is the live check run before executing (empty means none).
	Precondition string `json:"precondition,omitempty"`
	// Posts is true when executing this step publishes public text and requires
	// a per-item confirmation before the text is sent (§5.4).
	Posts bool `json:"posts,omitempty"`
	// ExpectedTip is the branch tip SHA we expect to see at execution time.
	// For local-delete steps it is the snapshot's local branch tip for that
	// clone; for remote-delete steps it is the snapshot tip from the first
	// clone used for the push. Task 10 compares this against the live tip
	// before executing, and treats an already-absent remote branch as done.
	ExpectedTip string `json:"expected_tip,omitempty"`
}

// Plan is the ordered set of steps for one apply session.
type Plan struct {
	// BuiltAt is the wall-clock time at which the plan was built (= now).
	BuiltAt time.Time `json:"built_at"`
	// Head is the casebook-data HEAD SHA at build time; set by the caller.
	Head string `json:"head,omitempty"`
	// Steps are the ordered apply steps.
	Steps []Step `json:"steps"`
}

// Group groups steps that share the same action and lane, for page display.
type Group struct {
	Action string `json:"action"`
	Lane   Lane   `json:"lane"`
	Steps  []Step `json:"steps"`
}

// ErrStale is returned by Build when the observation age exceeds syncInterval.
// The caller should offer a sync before retrying.
var ErrStale = errors.New("observation is older than one sync interval; sync first")

// Groups returns the plan's steps aggregated by (action, lane), preserving
// the first-seen order of each (action, lane) pair.
func (p Plan) Groups() []Group {
	type key struct {
		action string
		lane   Lane
	}
	seen := map[key]int{} // key → index in result
	var result []Group

	for _, s := range p.Steps {
		k := key{s.Action, s.Lane}
		if idx, ok := seen[k]; ok {
			result[idx].Steps = append(result[idx].Steps, s)
		} else {
			seen[k] = len(result)
			result = append(result, Group{
				Action: s.Action,
				Lane:   s.Lane,
				Steps:  []Step{s},
			})
		}
	}
	return result
}

// Build constructs a Plan from decided to-apply items for the machine described
// by snap.
//
// Staleness: ErrStale is returned when now.Sub(builtAt) > syncInterval.
// The stale check uses builtAt (the snapshot/cache time the index was built
// from), not the plan's own build time.
//
// Machine scope:
//   - local steps (branch delete, worktree remove) are generated only for
//     locations on snap.Machine;
//   - remote and GitHub steps run once, from whichever machine applies them;
//   - for remote branch delete, the remote name is the entry in Clone.Remotes
//     whose value equals the item's repo (case-insensitive); the first clone
//     (stable sort order) that has such a remote provides the working directory;
//     if no clone on this machine has a matching remote, no remote delete step
//     is generated.
func Build(items []engine.Item, snap observe.Snapshot, now, builtAt time.Time, syncInterval time.Duration) (Plan, error) {
	if now.Sub(builtAt) > syncInterval {
		return Plan{}, ErrStale
	}

	sl := newSnapLookup(snap)
	plan := Plan{BuiltAt: now}

	for _, it := range items {
		if it.Status != item.StatusToApply || it.Decision == nil {
			continue
		}
		plan.Steps = append(plan.Steps, stepsForItem(it, snap.Machine, now, sl)...)
	}

	return plan, nil
}

// snapLookup is a precomputed index into an observe.Snapshot for fast lookups.
type snapLookup struct {
	// cloneByPath maps clone path → Clone.
	cloneByPath map[string]observe.Clone
	// cloneForWorktree maps worktree path → Clone that owns it.
	cloneForWorktree map[string]observe.Clone
}

func newSnapLookup(snap observe.Snapshot) snapLookup {
	sl := snapLookup{
		cloneByPath:      make(map[string]observe.Clone, len(snap.Clones)),
		cloneForWorktree: make(map[string]observe.Clone),
	}
	for _, c := range snap.Clones {
		sl.cloneByPath[c.Path] = c
		for _, w := range c.Worktrees {
			sl.cloneForWorktree[w.Path] = c
		}
	}
	return sl
}

// branchTip returns the Tip SHA for branchName in the clone at clonePath, or
// "" if not found.
func (sl snapLookup) branchTip(clonePath, branchName string) string {
	b, _ := sl.branch(clonePath, branchName)
	return b.Tip
}

// branch returns the snapshot's record of branchName in the clone at clonePath.
func (sl snapLookup) branch(clonePath, branchName string) (observe.Branch, bool) {
	c, ok := sl.cloneByPath[clonePath]
	if !ok {
		return observe.Branch{}, false
	}
	for _, b := range c.Branches {
		if b.Name == branchName {
			return b, true
		}
	}
	return observe.Branch{}, false
}

// identityRemote returns the remote name and clone for the first clone (by
// stable sorted order) on this machine that has a remote pointing to repo
// (case-insensitive). Returns "", observe.Clone{} if none found.
func (sl snapLookup) identityRemote(clonePaths []string, repo string) (remoteName string, clone observe.Clone) {
	repoLower := strings.ToLower(repo)
	// clonePaths come from machineClones which preserves item.Locations order;
	// sort for stable deterministic selection.
	sorted := make([]string, len(clonePaths))
	copy(sorted, clonePaths)
	sort.Strings(sorted)
	for _, p := range sorted {
		c, ok := sl.cloneByPath[p]
		if !ok {
			continue
		}
		// Sort remote names for determinism within one clone.
		names := make([]string, 0, len(c.Remotes))
		for n := range c.Remotes {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if strings.ToLower(c.Remotes[n]) == repoLower {
				return n, c
			}
		}
	}
	return "", observe.Clone{}
}

// stepsForItem dispatches to the kind-specific builder.
func stepsForItem(it engine.Item, machine string, now time.Time, sl snapLookup) []Step {
	switch it.Kind {
	case item.KindBranch:
		return branchSteps(it, machine, now, sl)
	case item.KindWorktree:
		return worktreeSteps(it, machine, sl)
	case item.KindRepo:
		return repoSteps(it)
	case item.KindPR:
		return prSteps(it)
	case item.KindIssue:
		return issueSteps(it)
	}
	return nil
}

// machineClones returns the clone paths that are on the given machine,
// extracted from item.Locations entries of the form "machine:path".
func machineClones(it engine.Item, machine string) []string {
	var paths []string
	for _, loc := range it.Locations {
		m, p, ok := strings.Cut(loc, ":")
		if ok && m == machine {
			paths = append(paths, p)
		}
	}
	return paths
}

// branchSteps generates steps for a branch item with disposition delete.
//
// Local delete: one step per clone on machine, with ExpectedTip set from the
// snapshot's branch tip for that clone (precondition:
// branch-tip-unchanged-and-landed).
//
// Remote delete: one step from the first clone (stable order) that has a
// remote whose Remotes value equals the item's repo (case-insensitive). The
// remote name comes from that Remotes entry. The branch is on the remote only
// when that clone's branch has an upstream that isn't gone; a branch that was
// never pushed, or whose upstream the remote already deleted, gets no remote
// step. ExpectedTip is set only when the local tip equals what the remote has
// (no unpushed commits); otherwise it is empty, and the precondition
// (remote-tip-unchanged-and-landed) reads the remote's tip live and requires
// that exact commit to be landed. Omitted when no clone on this machine has a
// matching remote.
func branchSteps(it engine.Item, machine string, now time.Time, sl snapLookup) []Step {
	if it.Decision.Disposition != item.Delete {
		return nil
	}

	branch := it.Key.Branch
	localClones := machineClones(it, machine)

	var steps []Step

	// One local-delete step per clone on this machine.
	for _, clonePath := range localClones {
		steps = append(steps, Step{
			Key:          it.ID,
			Action:       "branch-delete-local",
			Lane:         LaneCasebook,
			Command:      branchDeleteLocalCmd(clonePath, branch),
			Precondition: "branch-tip-unchanged-and-landed",
			ExpectedTip:  sl.branchTip(clonePath, branch),
		})
	}

	// Remote delete: once, from the first clone that has a matching remote,
	// and only if that clone's branch is on the remote.
	remoteName, remoteClone := sl.identityRemote(localClones, it.Key.Repo())
	if remoteName != "" {
		if b, ok := sl.branch(remoteClone.Path, branch); ok && b.Upstream != "" && !b.Gone {
			tip := ""
			if b.Unpushed == 0 {
				tip = b.Tip
			}
			steps = append(steps, Step{
				Key:          it.ID,
				Action:       "branch-delete-remote",
				Lane:         LaneCasebook,
				Command:      branchDeleteRemoteCmd(remoteClone.Path, remoteName, branch),
				Precondition: "remote-tip-unchanged-and-landed",
				ExpectedTip:  tip,
			})
		}
	}

	return steps
}

// worktreeSteps generates a step to remove a worktree with disposition delete.
// Only generated when the worktree's machine matches the plan machine.
// The clone path is looked up from the snapshot (the clone that owns the
// worktree), not from the item's Locations list.
func worktreeSteps(it engine.Item, machine string, sl snapLookup) []Step {
	if it.Decision.Disposition != item.Delete {
		return nil
	}
	if it.Key.Machine != machine {
		return nil
	}
	worktreePath := it.Key.Path
	clone, ok := sl.cloneForWorktree[worktreePath]
	if !ok {
		// Worktree not found in snapshot; fall back to Locations.
		clonePath := machineCloneForWorktree(it, machine)
		if clonePath == "" {
			return nil
		}
		clone.Path = clonePath
	}
	return []Step{{
		Key:          it.ID,
		Action:       "worktree-remove",
		Lane:         LaneCasebook,
		Command:      worktreeRemoveCmd(clone.Path, worktreePath),
		Precondition: "worktree-clean",
	}}
}

// machineCloneForWorktree returns the clone path from the first Locations
// entry on the given machine. Used as fallback when the worktree is not found
// in the snapshot's cloneForWorktree index.
func machineCloneForWorktree(it engine.Item, machine string) string {
	for _, loc := range it.Locations {
		m, p, ok := strings.Cut(loc, ":")
		if ok && m == machine {
			return p
		}
	}
	return ""
}

// repoSteps generates steps for a repo item with disposition archive or delete.
func repoSteps(it engine.Item) []Step {
	repo := it.Key.Repo()
	switch it.Decision.Disposition {
	case item.Archive:
		return []Step{{
			Key:          it.ID,
			Action:       "repo-archive",
			Lane:         LaneAgent,
			Command:      repoArchiveCmd(repo),
			Precondition: "repo-no-open-human-prs",
		}}
	case item.Delete:
		return []Step{{
			Key:     it.ID,
			Action:  "repo-delete",
			Lane:    LaneAgent,
			Command: repoDeleteCmd(repo),
		}}
	}
	return nil
}

// prSteps generates steps for a PR item with disposition close or merge.
func prSteps(it engine.Item) []Step {
	repo := it.Key.Repo()
	n := it.Key.Number
	switch it.Decision.Disposition {
	case item.Close:
		comment := it.Decision.Note
		if comment == "" {
			comment = "Closing."
		}
		return []Step{{
			Key:          it.ID,
			Action:       "pr-close",
			Lane:         LaneAgent,
			Command:      prCloseCmd(n, repo, comment),
			Precondition: "pr-no-new-activity",
			Posts:        true,
		}}
	case item.Merge:
		return []Step{{
			Key:          it.ID,
			Action:       "pr-merge",
			Lane:         LaneAgent,
			Command:      prMergeCmd(n, repo),
			Precondition: "pr-no-new-activity",
		}}
	}
	return nil
}

// issueSteps generates a step for an issue item with disposition close.
func issueSteps(it engine.Item) []Step {
	if it.Decision.Disposition != item.Close {
		return nil
	}
	repo := it.Key.Repo()
	n := it.Key.Number
	comment := it.Decision.Note
	if comment == "" {
		comment = "Closing."
	}
	return []Step{{
		Key:     it.ID,
		Action:  "issue-close",
		Lane:    LaneAgent,
		Command: issueCloseCmd(n, repo, comment),
		Posts:   true,
	}}
}
