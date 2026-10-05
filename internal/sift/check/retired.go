package check

import (
	"context"
	"strings"

	"github.com/schuettc/tackle/internal/sift/row"
)

// retiredStore flags any pointer to a memory store that has been migrated:
// its paths, file names and tool names. Certain: the store is gone. Code
// blocks count too, since a command that reads the store is a pointer.
func retiredStore(_ context.Context, in *Input) []row.Row {
	var rows []row.Row
	for _, f := range in.Files {
		for _, l := range split(f.Content) {
			low := strings.ToLower(l.Text)
			for _, st := range in.Config.Retired {
				var ev []row.Fact
				for _, p := range st.Patterns {
					if strings.Contains(low, strings.ToLower(p)) {
						ev = append(ev, fact("pointer", "%s", p))
					}
				}
				if len(ev) == 0 {
					continue
				}
				ev = append([]row.Fact{fact("store", "%s", st.Name)}, ev...)
				rows = append(rows, newRow(f, "retired-store", l.N, l.N, l.Text, st.Name+"\x00"+l.Text,
					"points to "+st.Name+", which has been retired", true, ev...))
			}
		}
	}
	return rows
}
