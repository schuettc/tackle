package engine

import (
	"fmt"
	"strings"

	"github.com/schuettc/tackle/internal/docket/item"
)

// Brief is the session-start briefing for one repo: its items that need
// attention (at most max) and its own recorded decision. "" when there is
// nothing to say.
func Brief(r Result, repo string, max int) string {
	repo = strings.ToLower(repo)
	var att []Item
	for _, it := range r.Attention() {
		if it.Repo == repo {
			att = append(att, it)
		}
	}
	own, hasOwn := r.Find(item.RepoKey(repo).String())
	if len(att) == 0 && (!hasOwn || own.Decision == nil) {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "docket: %s", repo)
	if hasOwn && own.Decision != nil {
		fmt.Fprintf(&b, " (decided %s", own.Decision.Disposition)
		if own.Decision.Note != "" {
			fmt.Fprintf(&b, ": %s", own.Decision.Note)
		}
		b.WriteString(")")
	}
	if len(att) > 0 {
		fmt.Fprintf(&b, ": %d item(s) need attention", len(att))
	}
	b.WriteString("\n")
	for i, it := range att {
		if i == max {
			fmt.Fprintf(&b, "… %d more: docket attention --repo %s\n", len(att)-max, repo)
			break
		}
		fmt.Fprintf(&b, "- %-9s %s", it.Status, it.ID)
		if len(it.Hits) > 0 {
			fmt.Fprintf(&b, " · %s", it.Hits[0].Detail)
		} else if len(it.Evidence) > 0 {
			fmt.Fprintf(&b, " · %s", it.Evidence[0])
		}
		b.WriteString("\n")
	}
	return b.String()
}
