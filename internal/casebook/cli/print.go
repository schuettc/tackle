package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/schuettc/tackle/internal/casebook/app"
	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/journal"
)

func printSync(out io.Writer, machine string, r app.SyncReport) {
	state := "nothing new"
	if r.Committed {
		state = "committed"
	}
	if r.Pushed {
		state += ", pushed"
	}
	_, _ = fmt.Fprintf(out, "synced %s: %d event(s), %d clone(s), %s\n", machine, r.Events, r.Clones, state)
	if r.TempEvents > 0 {
		_, _ = fmt.Fprintf(out, "skipped %d event(s) in temp folders (never journalled)\n", r.TempEvents)
	}
	if r.Offline {
		_, _ = fmt.Fprintln(out, "offline: the casebook remote is unreachable; changes are queued locally")
	}
	for _, f := range r.Resolved {
		_, _ = fmt.Fprintf(out, "resolved a decision race in %s (later decision kept; see `casebook attention`)\n", f)
	}
	for _, e := range r.GitHubErrors {
		_, _ = fmt.Fprintf(out, "github: %s\n", e)
	}
	for _, e := range r.ScanErrors {
		_, _ = fmt.Fprintf(out, "scan: %s\n", e)
	}
	_, _ = fmt.Fprintf(out, "attention: %d item(s) (casebook attention)\n", r.Attention)
}

func printItems(out io.Writer, items []engine.Item, notices []string) {
	for _, n := range notices {
		_, _ = fmt.Fprintf(out, "note: %s\n", n)
	}
	tw := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "STATUS\tITEM\tWHY")
	for _, it := range items {
		why := ""
		switch {
		case len(it.Hits) > 0:
			why = it.Hits[0].Rule + ": " + it.Hits[0].Detail
		case len(it.Evidence) > 0:
			why = it.Evidence[0]
		}
		if it.Stale {
			why += " (stale)"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", it.Status, it.ID, why)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(out, "%d item(s)\n", len(items))
}

func printItem(out io.Writer, it engine.Item, hist []journal.Event) {
	_, _ = fmt.Fprintf(out, "%s\n  status: %s\n", it.ID, it.Status)
	for _, kv := range [][2]string{{"relation", it.Relation}, {"title", it.Title}, {"url", it.URL}} {
		if kv[1] != "" {
			_, _ = fmt.Fprintf(out, "  %s: %s\n", kv[0], kv[1])
		}
	}
	if d := it.Decision; d != nil {
		_, _ = fmt.Fprintf(out, "  decision: %s", d.Disposition)
		if d.Until != "" {
			_, _ = fmt.Fprintf(out, " until %s", d.Until)
		}
		_, _ = fmt.Fprintf(out, " by %s at %s\n", d.DecidedBy, d.DecidedAt.UTC().Format("2006-01-02 15:04"))
		if d.Note != "" {
			_, _ = fmt.Fprintf(out, "  note: %s\n", d.Note)
		}
		if c := d.Conflict; c != nil {
			_, _ = fmt.Fprintf(out, "  CONFLICT: %s by %s at %s lost the race; decide again to confirm\n", c.Disposition, c.DecidedBy, c.DecidedAt.UTC().Format("2006-01-02 15:04"))
		}
	}
	obs := "unknown"
	switch {
	case it.Observed.Known && it.Observed.Exists:
		obs = "exists"
		if it.Observed.State != "" {
			obs += ", " + strings.ToLower(it.Observed.State)
		}
		if it.Observed.Archived {
			obs += ", archived"
		}
	case it.Observed.Known:
		obs = "gone"
	}
	if it.Stale {
		obs += " (stale)"
	}
	_, _ = fmt.Fprintf(out, "  observed: %s\n", obs)
	for _, h := range it.Hits {
		_, _ = fmt.Fprintf(out, "  flag: %s: %s\n", h.Rule, h.Detail)
	}
	for _, e := range it.Evidence {
		_, _ = fmt.Fprintf(out, "  evidence: %s\n", e)
	}
	for _, l := range it.Locations {
		_, _ = fmt.Fprintf(out, "  location: %s\n", l)
	}
	for _, ev := range hist {
		_, _ = fmt.Fprintf(out, "  %s  %s\n", ev.TS.UTC().Format("2006-01-02 15:04"), eventLine(ev))
	}
}

// eventLine summarizes a journal event in one line.
func eventLine(ev journal.Event) string {
	who := "human"
	switch {
	case ev.Child && ev.AgentID != "":
		who = "pi:" + ev.AgentID
	case ev.ClaudeID != "":
		who = "claude:" + ev.ClaudeID
	case ev.AgentID != "":
		who = "pi:" + ev.AgentID
	}
	var what []string
	if ev.Hook != "" {
		what = append(what, ev.Hook+" "+strings.Join(ev.Args, " "))
	}
	for _, a := range ev.Actions {
		s := a.Tool + " " + a.Verb
		if a.Number > 0 {
			s += fmt.Sprintf(" #%d", a.Number)
		}
		if len(a.Refs) > 0 {
			s += " " + strings.Join(a.Refs, " ")
		}
		if len(a.Flags) > 0 {
			s += " " + strings.Join(a.Flags, " ")
		}
		what = append(what, s)
	}
	return fmt.Sprintf("%s %s [%s] %s", ev.Machine, ev.Repo, who, strings.Join(what, "; "))
}

// printPrune shows a prune: per-file counts, the top temp prefixes and the
// derived records, then what happened (or how to apply a dry run).
func printPrune(out io.Writer, r app.PruneReport) {
	mode := "dry run; nothing changed"
	if r.Applied {
		mode = "applied"
	}
	_, _ = fmt.Fprintf(out, "casebook prune --temp on %s (%s)\n", r.Machine, mode)
	if r.Removed == 0 && len(r.Clones) == 0 {
		_, _ = fmt.Fprintf(out, "nothing to prune: %d journal file(s), %d line(s), none in temp folders\n", len(r.Files), r.Kept)
		printPruneDecisions(out, r)
		return
	}
	for _, f := range r.Files {
		note := ""
		if f.Delete {
			note = " (file removed)"
		}
		_, _ = fmt.Fprintf(out, "  %s: remove %d, keep %d%s\n", f.Path, f.Removed, f.Kept, note)
	}
	_, _ = fmt.Fprintf(out, "total: remove %d of %d line(s), keep %d\n", r.Removed, r.Removed+r.Kept, r.Kept)
	if len(r.Prefixes) > 0 {
		_, _ = fmt.Fprintln(out, "top temp prefixes:")
		for i, p := range r.Prefixes {
			if i == 10 {
				_, _ = fmt.Fprintf(out, "  … %d more prefix(es)\n", len(r.Prefixes)-10)
				break
			}
			_, _ = fmt.Fprintf(out, "  %7d  %s\n", p.Lines, p.Prefix)
		}
	}
	// A removed line that carries a repo may be real work misclassified:
	// show them before anyone applies.
	_, _ = fmt.Fprintf(out, "removed lines that carry a repo: %d\n", r.RepoLines)
	for i, rc := range r.Repos {
		if i == 10 {
			_, _ = fmt.Fprintf(out, "  … %d more repo(s)\n", len(r.Repos)-10)
			break
		}
		_, _ = fmt.Fprintf(out, "  %7d  %s\n", rc.Lines, rc.Repo)
	}
	_, _ = fmt.Fprintf(out, "derived records: machines/%s.json: %d temp clone(s)\n", r.Machine, len(r.Clones))
	for _, c := range r.Clones {
		_, _ = fmt.Fprintf(out, "  %s\n", c)
	}
	printPruneDecisions(out, r)
	switch {
	case !r.Applied:
		_, _ = fmt.Fprintln(out, "run again with --apply to rewrite this machine's journal files, commit and push")
	case r.Pushed:
		_, _ = fmt.Fprintln(out, "committed and pushed; the old lines stay in casebook-data's history")
	case r.Offline:
		_, _ = fmt.Fprintln(out, "committed; the casebook remote is unreachable, so the next sync pushes it")
	case r.Committed:
		_, _ = fmt.Fprintln(out, "committed (not pushed: --no-push); the old lines stay in casebook-data's history")
	}
}

func printPruneDecisions(out io.Writer, r app.PruneReport) {
	if len(r.WorktreeDecisions) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "worktree decisions at temp paths (decisions are intent; left alone): %d\n", len(r.WorktreeDecisions))
	for _, d := range r.WorktreeDecisions {
		_, _ = fmt.Fprintf(out, "  %s\n", d)
	}
}
