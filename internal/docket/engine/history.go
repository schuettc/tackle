package engine

import (
	"slices"
	"strings"

	"github.com/schuettc/tackle/internal/docket/item"
	"github.com/schuettc/tackle/internal/docket/journal"
	"github.com/schuettc/tackle/internal/docket/observe"
)

// RepoForPath maps a directory on machine to the GitHub repo of the clone (or
// linked worktree) containing it; "" if none.
func RepoForPath(snaps []observe.Snapshot, machine, path string) string {
	best, repo := -1, ""
	within := func(root string) bool { return path == root || strings.HasPrefix(path, root+"/") }
	for _, s := range snaps {
		if s.Machine != machine {
			continue
		}
		for _, c := range s.Clones {
			roots := []string{c.Path}
			for _, w := range c.Worktrees {
				roots = append(roots, w.Path)
			}
			for _, r := range roots {
				if within(r) && len(r) > best {
					best, repo = len(r), c.Repo
				}
			}
		}
	}
	return repo
}

// History returns the journal events that concern k, in input order.
func History(events []journal.Event, k item.Key) []journal.Event {
	var out []journal.Event
	for _, ev := range events {
		if matches(ev, k) {
			out = append(out, ev)
		}
	}
	return out
}

func matches(ev journal.Event, k item.Key) bool {
	switch k.Kind {
	case item.KindWorktree:
		return ev.Machine == k.Machine && (ev.CWD == k.Path || strings.HasPrefix(ev.CWD, k.Path+"/"))
	case item.KindRepo:
		if ev.Repo == k.Repo() {
			return true
		}
		return slices.ContainsFunc(ev.Actions, func(a journal.Action) bool { return a.Repo == k.Repo() })
	case item.KindPR, item.KindIssue:
		prefix := "pr "
		if k.Kind == item.KindIssue {
			prefix = "issue "
		}
		return slices.ContainsFunc(ev.Actions, func(a journal.Action) bool {
			return a.Repo == k.Repo() && a.Number == k.Number && strings.HasPrefix(a.Verb, prefix)
		})
	case item.KindBranch:
		if ev.Repo != k.Repo() {
			return false
		}
		ref := "refs/heads/" + k.Branch
		for _, line := range ev.Stdin {
			if slices.Contains(line, ref) {
				return true
			}
		}
		return slices.Contains(ev.Args, k.Branch) || slices.ContainsFunc(ev.Actions, func(a journal.Action) bool {
			return slices.ContainsFunc(a.Refs, func(r string) bool { return r == k.Branch || strings.HasSuffix(r, ":"+k.Branch) })
		})
	}
	return false
}
