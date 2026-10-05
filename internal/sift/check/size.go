package check

import (
	"context"
	"strings"

	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/row"
)

// size flags a file over its class budget. The id hashes the whole file, so
// a mute lapses when the file changes.
func size(_ context.Context, in *Input) []row.Row {
	var rows []row.Row
	for _, f := range in.Files {
		budget, label := in.Config.Budgets.Repo, "repo file"
		switch f.Class {
		case discover.ClassGlobal:
			budget, label = in.Config.Budgets.Global, "global file"
		case discover.ClassSkill:
			budget, label = in.Config.Budgets.Skill, "skill"
		}
		n := len(f.Content)
		if n <= budget {
			continue
		}
		ev := []row.Fact{
			fact("size", "%d bytes", n),
			fact("budget", "%d bytes (%s)", budget, label),
		}
		if len(f.Profiles) > 0 {
			ev = append(ev, fact("loaded by", "%s", strings.Join(f.Profiles, ", ")))
		}
		for _, a := range f.Also {
			ev = append(ev, fact("also at", "%s", a))
		}
		rows = append(rows, newRow(f, "size", 0, 0, "", f.Content,
			"the "+label+" is "+kb(n)+", over its "+kb(budget)+" budget", false, ev...))
	}
	return rows
}
