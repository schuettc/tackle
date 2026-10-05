package check

import (
	"context"
	"regexp"
	"time"

	"github.com/schuettc/tackle/internal/sift/host"
	"github.com/schuettc/tackle/internal/sift/row"
)

var dateRE = regexp.MustCompile(`\b(20\d\d-[01]\d-[0-3]\d)\b`)

// staleStatus flags lines that describe a moment rather than a standing
// rule: status phrases ("waiting on", "until X lands"), PR and issue
// references, and dates older than the stale window. A line naming a PR the
// host reports merged or closed is certain; without a host (no gh) every
// reference is a finding to judge.
func staleStatus(ctx context.Context, in *Input) []row.Row {
	var phrases []*regexp.Regexp
	for _, p := range in.Config.Stale.Phrases {
		phrases = append(phrases, regexp.MustCompile("(?i)"+p))
	}
	cutoff := in.Now.AddDate(0, 0, -in.Config.Windows.StaleDays)
	var rows []row.Row
	for _, f := range in.Files {
		slug := ""
		if f.Repo != nil {
			slug, _ = host.Slug(f.Repo.Remote)
		}
		for _, l := range prose(f.Content) {
			var ev []row.Fact
			certain := false
			for _, re := range phrases {
				if m := re.FindString(l.Text); m != "" {
					ev = append(ev, fact("phrase", "%s", m))
				}
			}
			for _, ref := range host.Refs(l.Text, slug) {
				if in.Host == nil {
					ev = append(ev, fact("reference", "%s: state unknown (no gh)", ref))
					continue
				}
				st, err := in.Host.Lookup(ctx, ref)
				if err != nil {
					ev = append(ev, fact("reference", "%s: state unknown (%v)", ref, err))
					continue
				}
				ev = append(ev, fact("reference", "%s: %s %s", ref, st.Kind, st.State))
				if st.Kind == "pr" && (st.State == "merged" || st.State == "closed") {
					certain = true
				}
			}
			for _, d := range dateRE.FindAllString(l.Text, -1) {
				t, err := time.Parse("2006-01-02", d)
				if err != nil || !t.Before(cutoff) {
					continue
				}
				ev = append(ev, fact("date", "%s (%d days ago)", d, int(in.Now.Sub(t).Hours()/24)))
			}
			if len(ev) == 0 {
				continue
			}
			summary := "status that may have gone stale"
			if certain {
				summary = "names a pull request that is done"
			}
			rows = append(rows, newRow(f, "stale-status", l.N, l.N, l.Text, "", summary, certain, ev...))
		}
	}
	return rows
}
