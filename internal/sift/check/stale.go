package check

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/host"
	"github.com/schuettc/tackle/internal/sift/row"
)

var dateRE = regexp.MustCompile(`\b(20\d\d-[01]\d-[0-3]\d)\b`)

// staleStatus flags lines that describe a moment rather than a standing
// rule: status phrases ("waiting on", "until X lands"), PR and issue
// references, and dates older than the stale window. A line is certain only
// when it waits on a PR (a wait phrase, "until #N merges", around the
// reference) that the host reports merged or closed, and no reference on it
// is open or unknown. Any other reference, a record of finished work
// included, is a finding to judge, as is every reference without a host.
func staleStatus(ctx context.Context, in *Input) []row.Row {
	var phrases, waits []*regexp.Regexp
	for _, p := range in.Config.Stale.Phrases {
		phrases = append(phrases, regexp.MustCompile("(?i)"+p))
	}
	for _, w := range in.Config.Stale.Waits {
		re, err := config.Wait(w)
		if err != nil {
			panic(err) // config.Validate rejects it
		}
		waits = append(waits, re)
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
			for _, re := range phrases {
				if m := re.FindString(l.Text); m != "" {
					ev = append(ev, fact("phrase", "%s", m))
				}
			}
			waited := map[host.Ref]bool{}
			for _, re := range waits {
				for _, m := range re.FindAllStringSubmatch(l.Text, -1) {
					for _, ref := range host.Refs(m[re.SubexpIndex(config.RefGroup)], slug) {
						waited[ref] = true
					}
				}
			}
			waitsOnDone, settled := false, true
			for _, ref := range host.Refs(l.Text, slug) {
				if in.Host == nil {
					ev = append(ev, fact("reference", "%s: state unknown (no gh)", ref))
					settled = false
					continue
				}
				st, err := in.Host.Lookup(ctx, ref)
				if err != nil {
					msg, _, _ := strings.Cut(err.Error(), "\n")
					ev = append(ev, fact("reference", "%s: state unknown (%s)", ref, msg))
					settled = false
					continue
				}
				ev = append(ev, fact("reference", "%s: %s %s", ref, st.Kind, st.State))
				if st.State == "open" {
					settled = false
				}
				if waited[ref] && st.Kind == "pr" && (st.State == "merged" || st.State == "closed") {
					waitsOnDone = true
					ev = append(ev, fact("waits on", "%s", ref))
				}
			}
			certain := waitsOnDone && settled
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
				summary = "waits on a pull request that is done"
			}
			rows = append(rows, newRow(f, "stale-status", l.N, l.N, l.Text, "", summary, certain, ev...))
		}
	}
	return rows
}
