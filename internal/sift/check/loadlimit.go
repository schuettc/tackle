package check

import (
	"context"
	"strings"

	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/row"
)

// loadLimit flags a harness's chain (its global file plus every file from
// the repo root down to a directory) past that harness's own limit: the
// harness silently drops what comes after. The row is on the chain's
// endpoint, the deepest audited file, never on a context file above a root.
func loadLimit(_ context.Context, in *Input) []row.Row {
	var rows []row.Row
	for _, c := range in.Chains {
		var leaf *discover.File
		for _, f := range c.Files {
			if !f.Context {
				leaf = f
			}
		}
		if c.Bytes <= c.Limit || leaf == nil {
			continue
		}
		var ev []row.Fact
		var paths, dropped []string
		total := 0
		for _, f := range c.Files {
			total += len(f.Content)
			paths = append(paths, f.Path)
			ev = append(ev, fact("file", "%s (%d bytes)", f.Path, len(f.Content)))
			if total > c.Limit {
				dropped = append(dropped, f.Path)
			}
		}
		ev = append(ev, fact("chain", "%d bytes, limit %d (%s)", c.Bytes, c.Limit, c.Profile))
		for _, d := range dropped {
			ev = append(ev, fact("dropped", "%s", d))
		}
		dir := c.Dir
		if dir == "" {
			dir = "the repo root"
		}
		rows = append(rows, newRow(leaf, "load-limit", 0, 0, "", "chain "+c.Profile+"\x00"+strings.Join(paths, "\x00"),
			c.Profile+" loads "+kb(c.Bytes)+" in "+dir+", past its "+kb(c.Limit)+" limit: the rest is dropped", false, ev...))
	}
	return rows
}
