// Package view renders the docket's Markdown boards (README.md,
// CONTRIBUTIONS.md, MACHINES.md), committed to the private docket repo and
// read on github.com.
package view

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schuettc/tackle/internal/docket/engine"
	"github.com/schuettc/tackle/internal/docket/item"
	"github.com/schuettc/tackle/internal/docket/observe"
)

// Render produces every view.
func Render(r engine.Result, snaps []observe.Snapshot) map[string][]byte {
	return map[string][]byte{
		"README.md":        readme(r),
		"CONTRIBUTIONS.md": contributions(r),
		"MACHINES.md":      machines(snaps),
	}
}

var cellEscaper = strings.NewReplacer("|", `\|`, "\r\n", " ", "\n", " ", "<", "&lt;")

func cell(s string) string { return cellEscaper.Replace(s) }

func decisionText(d *item.Decision) string {
	if d == nil {
		return ""
	}
	s := string(d.Disposition)
	if d.Until != "" {
		s += " until " + d.Until
	}
	if d.Note != "" {
		s += " (" + d.Note + ")"
	}
	return s
}

func why(it engine.Item) string {
	var parts []string
	for _, h := range it.Hits {
		parts = append(parts, h.Rule+": "+h.Detail)
	}
	if it.Decision != nil && it.Decision.Conflict != nil {
		c := it.Decision.Conflict
		parts = append(parts, fmt.Sprintf("raced with %s by %s", c.Disposition, c.DecidedBy))
	}
	if len(parts) == 0 && len(it.Evidence) > 0 {
		parts = append(parts, it.Evidence[0])
	}
	if it.Stale {
		parts = append(parts, "stale data")
	}
	return strings.Join(parts, "; ")
}

func table(b *strings.Builder, items []engine.Item) {
	b.WriteString("| Item | Title | Decision | Why |\n|---|---|---|---|\n")
	for _, it := range items {
		id := "`" + it.ID + "`"
		if it.URL != "" {
			id = "[" + id + "](" + it.URL + ")"
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n", id, cell(it.Title), cell(decisionText(it.Decision)), cell(why(it)))
	}
	b.WriteString("\n")
}

func readme(r engine.Result) []byte {
	lanes := []struct {
		title string
		match func(engine.Item) bool
	}{
		{"Conflict", func(it engine.Item) bool { return it.Status == item.StatusConflict }},
		{"Drift", func(it engine.Item) bool { return it.Status == item.StatusDrift }},
		{"Due", func(it engine.Item) bool { return it.Status == item.StatusDue }},
		{"Flagged", func(it engine.Item) bool {
			return len(it.Hits) > 0 && it.Status != item.StatusNew && it.Status != item.StatusConflict && it.Status != item.StatusDrift && it.Status != item.StatusDue
		}},
		{"New", func(it engine.Item) bool { return it.Status == item.StatusNew }},
		{"Waiting", func(it engine.Item) bool { return it.Status == item.StatusWaiting && len(it.Hits) == 0 }},
		{"To apply", func(it engine.Item) bool { return it.Status == item.StatusToApply && len(it.Hits) == 0 }},
	}
	var b strings.Builder
	b.WriteString("# docket\n\nWritten by `docket sync`; decide with `docket decide <key> <disposition>`.\n\n")
	counts := map[item.Status]int{}
	for _, it := range r.Items {
		counts[it.Status]++
	}
	fmt.Fprintf(&b, "%d items: %d new, %d due, %d drift, %d conflict, %d waiting, %d to apply, %d done.\n\n",
		len(r.Items), counts[item.StatusNew], counts[item.StatusDue], counts[item.StatusDrift], counts[item.StatusConflict],
		counts[item.StatusWaiting], counts[item.StatusToApply], counts[item.StatusDone])
	if len(r.Notices) > 0 {
		b.WriteString("> [!WARNING]\n")
		for _, n := range r.Notices {
			fmt.Fprintf(&b, "> %s\n", cell(n))
		}
		b.WriteString("\n")
	}
	for _, lane := range lanes {
		var items []engine.Item
		for _, it := range r.Items {
			if lane.match(it) {
				items = append(items, it)
			}
		}
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s (%d)\n\n", lane.title, len(items))
		if lane.title == "New" {
			byKind := map[item.Kind][]engine.Item{}
			for _, it := range items {
				byKind[it.Kind] = append(byKind[it.Kind], it)
			}
			for _, k := range []item.Kind{item.KindPR, item.KindIssue, item.KindBranch, item.KindWorktree, item.KindRepo} {
				if len(byKind[k]) > 0 {
					fmt.Fprintf(&b, "### %s (%d)\n\n", k, len(byKind[k]))
					table(&b, byKind[k])
				}
			}
			continue
		}
		table(&b, items)
	}
	return []byte(b.String())
}

func contributions(r engine.Result) []byte {
	var b strings.Builder
	b.WriteString("# Contributions\n\nOpen pull requests to other people's repositories.\n\n")
	b.WriteString("| Pull request | Title | Status | Decision | Why |\n|---|---|---|---|---|\n")
	n := 0
	for _, it := range r.Items {
		if it.Kind != item.KindPR || it.Relation != "outgoing" {
			continue
		}
		n++
		fmt.Fprintf(&b, "| [`%s`](%s) | %s | %s | %s | %s |\n", it.ID, it.URL, cell(it.Title), it.Status, cell(decisionText(it.Decision)), cell(why(it)))
	}
	if n == 0 {
		b.WriteString("| none | | | | |\n")
	}
	return []byte(b.String())
}

func machines(snaps []observe.Snapshot) []byte {
	sorted := append([]observe.Snapshot(nil), snaps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Machine < sorted[j].Machine })
	var b strings.Builder
	b.WriteString("# Machines\n\nLocal state from each machine's last sync.\n\n")
	for _, s := range sorted {
		fmt.Fprintf(&b, "## %s\n\n%d clones under %s.\n\n", s.Machine, len(s.Clones), strings.Join(s.Roots, ", "))
		b.WriteString("| Clone | Repo | State |\n|---|---|---|\n")
		for _, c := range s.Clones {
			var st []string
			if c.Dirty {
				st = append(st, "dirty")
			}
			if c.Stashes > 0 {
				st = append(st, fmt.Sprintf("%d stashes", c.Stashes))
			}
			for _, br := range c.Branches {
				if br.Unpushed > 0 {
					st = append(st, fmt.Sprintf("%d unpushed on %s", br.Unpushed, br.Name))
				}
			}
			if len(c.Worktrees) > 0 {
				st = append(st, fmt.Sprintf("%d worktrees", len(c.Worktrees)))
			}
			if c.LocalHooksPath != "" {
				st = append(st, "not journaled (local core.hooksPath "+c.LocalHooksPath+")")
			}
			repo := c.Repo
			if repo == "" {
				repo = "no GitHub remote"
			}
			if len(st) == 0 && c.Repo != "" {
				continue // clean and ordinary: keep the table short
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", cell(c.Path), cell(repo), cell(strings.Join(st, ", ")))
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}
