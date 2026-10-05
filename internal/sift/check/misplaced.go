package check

import (
	"context"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/profile"
	"github.com/schuettc/tackle/internal/sift/row"
)

// minTerm is the shortest repo name used as a term: shorter names are too
// often ordinary words.
const minTerm = 4

// term is a name or path that belongs to one root.
type term struct {
	text, root string
	re         *regexp.Regexp
}

// terms returns each root's path (as configured and with ~ for the home
// directory) and the names of the repos under it.
func terms(in *Input) []term {
	home := profile.Expand("~")
	var out []term
	seen := map[string]bool{}
	addTerm := func(text, root string, pathLike bool) {
		if seen[text] || len(text) < minTerm {
			return
		}
		seen[text] = true
		pat := `(?:^|[^\w-])` + regexp.QuoteMeta(text) + `(?:$|[^\w-])`
		if pathLike {
			pat = regexp.QuoteMeta(text) + `(?:$|[/\s` + "`" + `'")\],.;:])`
		}
		out = append(out, term{text: text, root: root, re: regexp.MustCompile(pat)})
	}
	for _, r := range in.Config.Roots {
		addTerm(r.Path, r.Path, true)
		if home != "~" && strings.HasPrefix(r.Path, home+string(filepath.Separator)) {
			addTerm("~"+strings.TrimPrefix(r.Path, home), r.Path, true)
		}
		var names []string
		for _, repo := range in.Repos {
			if repo.Root == r.Path || strings.HasPrefix(repo.Root, r.Path+string(filepath.Separator)) {
				names = append(names, filepath.Base(repo.Root))
			}
		}
		sort.Strings(names)
		for _, n := range names {
			addTerm(n, r.Path, false)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].text < out[j].text })
	return out
}

// misplaced flags lines in a global file that name a configured root or a
// repo under it: content that matters in one place, loaded in every session.
func misplaced(_ context.Context, in *Input) []row.Row {
	ts := terms(in)
	var rows []row.Row
	for _, f := range in.Files {
		if f.Class != discover.ClassGlobal {
			continue
		}
		for _, l := range prose(f.Content) {
			var ev []row.Fact
			for _, t := range ts {
				if t.re.MatchString(l.Text) {
					ev = append(ev, fact("term", "%s (from %s)", t.text, t.root))
				}
			}
			if len(ev) == 0 {
				continue
			}
			rows = append(rows, newRow(f, "misplaced", l.N, l.N, l.Text, "",
				"global file names something from one root", false, ev...))
		}
	}
	return rows
}
