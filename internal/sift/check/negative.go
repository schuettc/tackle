package check

import (
	"context"
	"regexp"
	"strings"

	"github.com/schuettc/tackle/internal/sift/row"
)

// negativeRule flags rules phrased as prohibitions (the configured patterns;
// English by default). Rewriting one as guidance is a judgment, so none is
// certain.
func negativeRule(_ context.Context, in *Input) []row.Row {
	var pats []*regexp.Regexp
	for _, p := range in.Config.Negative.Patterns {
		pats = append(pats, regexp.MustCompile("(?i)"+p))
	}
	var rows []row.Row
	for _, f := range in.Files {
		for _, l := range prose(f.Content) {
			if headingRE.MatchString(l.Text) {
				continue
			}
			var ev []row.Fact
			for _, re := range pats {
				if m := re.FindString(l.Text); m != "" {
					ev = append(ev, fact("matched", "%s", trimMatch(m)))
				}
			}
			if len(ev) == 0 {
				continue
			}
			rows = append(rows, newRow(f, "negative-rule", l.N, l.N, l.Text, "",
				"a rule phrased as a prohibition", false, ev...))
		}
	}
	return rows
}

var leadRE = regexp.MustCompile(`^\s*(?:[-*+]|\d+\.)\s+(?:\*\*)?`)

// trimMatch drops a list marker and trailing space from a match ("- No " →
// "No").
func trimMatch(m string) string { return strings.TrimSpace(leadRE.ReplaceAllString(m, "")) }
