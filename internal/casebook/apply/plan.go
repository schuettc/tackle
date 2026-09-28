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
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/item"
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

// Build constructs a Plan from decided to-apply items for the given machine.
//
// Staleness: ErrStale is returned when now.Sub(builtAt) > syncInterval.
// The stale check uses builtAt (the snapshot/cache time the index was built
// from), not the plan's own build time.
//
// Machine scope:
//   - local steps (branch delete, worktree remove) are generated only for
//     locations on machine;
//   - remote and GitHub steps run once, from whichever machine applies them;
//   - for remote branch delete, the first local clone is used as the git
//     working directory; if no local clone exists, no remote delete step is
//     generated (the branch stays to-apply on the other machine).
func Build(items []engine.Item, machine string, now time.Time, builtAt time.Time, syncInterval time.Duration) (Plan, error) {
	if now.Sub(builtAt) > syncInterval {
		return Plan{}, ErrStale
	}

	plan := Plan{BuiltAt: now}

	for _, it := range items {
		if it.Status != item.StatusToApply || it.Decision == nil {
			continue
		}
		plan.Steps = append(plan.Steps, stepsForItem(it, machine, now)...)
	}

	return plan, nil
}

// stepsForItem dispatches to the kind-specific builder.
func stepsForItem(it engine.Item, machine string, now time.Time) []Step {
	switch it.Kind {
	case item.KindBranch:
		return branchSteps(it, machine, now)
	case item.KindWorktree:
		return worktreeSteps(it, machine)
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
// Local delete: one step per clone on machine (precondition:
// branch-tip-unchanged-and-landed).
//
// Remote delete: one step from the first local clone, using "origin" as the
// conventional remote name (precondition: remote-tip-unchanged-and-landed).
// Omitted when the remote tracking branch is already gone (Fields.GoneUpstream).
// Omitted when this machine has no local clone for the branch (the other
// machine's plan will cover it).
//
// How we decide whether a remote copy exists: we call it.Fields(now) and read
// GoneUpstream, which reflects observe.Branch.Gone (upstream set but deleted
// on the remote, typically squash-merged). When GoneUpstream is true the
// remote branch was already deleted; no push is needed and we skip the step
// rather than producing a command that would fail.
func branchSteps(it engine.Item, machine string, now time.Time) []Step {
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
		})
	}

	// Remote delete: once, from the first local clone.
	// Skip if the remote tracking branch is already gone.
	if len(localClones) > 0 && !it.Fields(now).GoneUpstream {
		steps = append(steps, Step{
			Key:          it.ID,
			Action:       "branch-delete-remote",
			Lane:         LaneCasebook,
			Command:      branchDeleteRemoteCmd(localClones[0], "origin", branch),
			Precondition: "remote-tip-unchanged-and-landed",
		})
	}

	return steps
}

// worktreeSteps generates a step to remove a worktree with disposition delete.
// Only generated when the worktree's machine matches the plan machine.
func worktreeSteps(it engine.Item, machine string) []Step {
	if it.Decision.Disposition != item.Delete {
		return nil
	}
	if it.Key.Machine != machine {
		return nil
	}
	// The clone path is the first location entry (Locations holds "machine:clonepath").
	clonePath := firstLocation(it)
	if clonePath == "" {
		return nil
	}
	return []Step{{
		Key:          it.ID,
		Action:       "worktree-remove",
		Lane:         LaneCasebook,
		Command:      worktreeRemoveCmd(clonePath, it.Key.Path),
		Precondition: "worktree-clean",
	}}
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

// firstLocation returns the clone path from the first Locations entry (any
// machine). Returns "" if Locations is empty.
func firstLocation(it engine.Item) string {
	for _, loc := range it.Locations {
		_, p, ok := strings.Cut(loc, ":")
		if ok {
			return p
		}
	}
	return ""
}
