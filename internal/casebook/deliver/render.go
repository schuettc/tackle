package deliver

import (
	"fmt"
	"strings"
	"time"
)

// Render is the text a delivery puts into the agent's session (casebook
// workbench spec §6.3). workingOn is the first message of the delivery the
// agent was working on when these were queued ("" if none); summary is the
// "since you last looked" paragraph ("" if nothing changed); loc formats the
// times.
func Render(d Delivery, workingOn, summary string, loc *time.Location) string {
	var b strings.Builder
	n := len(d.Messages)
	noun := "message"
	if n != 1 {
		noun = "messages"
	}
	fmt.Fprintf(&b, "casebook: %d %s from Court", n, noun)
	if workingOn != "" && n > 0 {
		first, last := d.Messages[0].QueuedAt, d.Messages[n-1].QueuedAt
		fmt.Fprintf(&b, ", sent while you were working on %q (%s–%s)", snippet(workingOn, 60), first.In(loc).Format("15:04"), last.In(loc).Format("15:04"))
	}
	b.WriteString(".")
	if n > 1 {
		b.WriteString(" Read all of them before acting. Later messages may refine or cancel earlier ones; when they conflict, follow the latest and say so.")
	}
	if workingOn != "" {
		b.WriteString(" They were written before your last turn's results. If one is already answered by what you did, say so rather than redoing it.")
	}
	settle := "Settle each"
	if n == 1 {
		settle = "Settle it"
	}
	b.WriteString(" " + settle + " with casebook_reply.\n")
	sizes := map[int64]int{}
	for _, m := range d.Messages {
		if m.BatchID != 0 {
			sizes[m.BatchID]++
		}
	}
	for _, m := range d.Messages {
		fmt.Fprintf(&b, "\n[m-%d] %s", m.ID, m.QueuedAt.In(loc).Format("15:04"))
		if m.BatchID != 0 {
			fmt.Fprintf(&b, " · batch b-%d (%d of %d)", m.BatchID, m.BatchPos, sizes[m.BatchID])
		}
		if a := describe(m.Attached); a != "" {
			b.WriteString(" · attached: " + a)
		}
		b.WriteString("\n")
		for _, line := range strings.Split(strings.TrimRight(m.Body, "\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
	}
	if summary != "" {
		b.WriteString("\nSince you last looked: " + summary + "\n")
	}
	return b.String()
}

func describe(a Attached) string {
	var parts []string
	switch k := len(a.Keys); {
	case k == 1:
		parts = append(parts, a.Keys[0])
	case k > 1 && k <= 4:
		parts = append(parts, fmt.Sprintf("%d items (%s)", k, strings.Join(a.Keys, ", ")))
	case k > 4:
		parts = append(parts, fmt.Sprintf("%d items (%s, …)", k, strings.Join(a.Keys[:3], ", ")))
	}
	if a.Open != "" && (len(a.Keys) != 1 || a.Keys[0] != a.Open) {
		parts = append(parts, "open "+a.Open)
	}
	if a.Rule != "" {
		parts = append(parts, "rule "+a.Rule)
	}
	if a.Job != "" {
		parts = append(parts, "job "+a.Job)
	}
	if a.Section != "" && len(parts) == 0 {
		parts = append(parts, "section "+a.Section)
	}
	return strings.Join(parts, " · ")
}

func snippet(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
