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
