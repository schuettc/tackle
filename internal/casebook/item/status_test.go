package item

import (
	"testing"
	"time"
)

func TestCompute(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	repo := Key{Kind: KindRepo, Owner: "a", Name: "b"}
	pr := Key{Kind: KindPR, Owner: "a", Name: "b", Number: 1}
	br := Key{Kind: KindBranch, Owner: "a", Name: "b", Branch: "x"}
	f := fakeFacts{state: map[string]string{"pr:x/y#9": "MERGED", "pr:x/y#8": "OPEN"}}
	dec := func(d Disposition, until string) *Decision {
		return &Decision{Disposition: d, Until: until, DecidedBy: "c", DecidedAt: now.Add(-time.Hour)}
	}
	live := Observed{Known: true, Exists: true}
	cases := []struct {
		name string
		k    Key
		d    *Decision
		obs  Observed
		seen bool
		want Status
	}{
		{"undecided", repo, nil, live, false, StatusNew},
		{"conflict", repo, &Decision{Disposition: Keep, DecidedBy: "c", DecidedAt: now, Conflict: &Conflict{Disposition: Archive}}, live, false, StatusConflict},
		{"archive pending", repo, dec(Archive, ""), live, false, StatusToApply},
		{"archive done", repo, dec(Archive, ""), Observed{Known: true, Exists: true, Archived: true}, false, StatusDone},
		{"archive undone later", repo, dec(Archive, ""), live, true, StatusDrift},
		{"archive unknown observation", repo, dec(Archive, ""), Observed{}, false, StatusToApply},
		{"close done by merge", pr, dec(Close, ""), Observed{Known: true, Exists: true, State: "MERGED"}, false, StatusDone},
		{"merge pending", pr, dec(Merge, ""), Observed{Known: true, Exists: true, State: "OPEN"}, false, StatusToApply},
		{"delete done", br, dec(Delete, ""), Observed{Known: true}, false, StatusDone},
		{"delete unknown", br, dec(Delete, ""), Observed{}, false, StatusToApply},
		{"delete done, now unknown", br, dec(Delete, ""), Observed{}, true, StatusDone},
		{"watch waiting", pr, dec(Watch, "merged(pr:x/y#8)"), live, false, StatusWaiting},
		{"watch due", pr, dec(Watch, "merged(pr:x/y#9)"), live, false, StatusDue},
		{"watch unknown ref", pr, dec(Watch, "merged(pr:x/y#7)"), live, false, StatusWaiting},
		{"keep", repo, dec(Keep, ""), live, false, StatusLeftOpen},
		{"keep with unknown observation", repo, dec(Keep, ""), Observed{}, false, StatusDone},
		{"keep snooze due", repo, dec(Keep, "date(2026-09-01)"), live, false, StatusDue},
		{"keep snooze pending", repo, dec(Keep, "date(2026-12-01)"), live, false, StatusLeftOpen},
		{"ignore", repo, dec(Ignore, ""), live, false, StatusDone},
	}
	for _, tc := range cases {
		if got := Compute(tc.k, tc.d, tc.obs, f, now, tc.seen); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

// TestComputeLeftOpenAndNewActivity: what Leave it open and Not now leave
// behind (spec amendment 2026-10-07). A kept item that is still open is
// left open; someone else's activity newer than the decision brings a kept
// or Not now item back as due; a kept PR or issue closed since is done.
func TestComputeLeftOpenAndNewActivity(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	decided := now.Add(-48 * time.Hour)
	pr := Key{Kind: KindPR, Owner: "a", Name: "b", Number: 1}
	issue := Key{Kind: KindIssue, Owner: "a", Name: "b", Number: 2}
	br := Key{Kind: KindBranch, Owner: "a", Name: "b", Branch: "x"}
	wt := Key{Kind: KindWorktree, Machine: "m", Path: "/w"}
	repo := Key{Kind: KindRepo, Owner: "a", Name: "b"}
	newer := fakeFacts{other: map[string]time.Time{pr.String(): now.Add(-time.Hour), issue.String(): now.Add(-time.Hour)}}
	older := fakeFacts{other: map[string]time.Time{pr.String(): decided.Add(-time.Hour), issue.String(): decided.Add(-time.Hour)}}
	quiet := fakeFacts{}
	dec := func(d Disposition, until string) *Decision {
		return &Decision{Disposition: d, Until: until, DecidedBy: "c", DecidedAt: decided}
	}
	open := Observed{Known: true, Exists: true, State: "OPEN"}
	closed := Observed{Known: true, Exists: true, State: "CLOSED"}
	merged := Observed{Known: true, Exists: true, State: "MERGED"}
	live := Observed{Known: true, Exists: true}
	cases := []struct {
		name   string
		k      Key
		d      *Decision
		obs    Observed
		f      fakeFacts
		want   Status
		reason string
	}{
		{"keep pr, quiet", pr, dec(Keep, ""), open, quiet, StatusLeftOpen, ""},
		{"keep pr, others' activity before the decision", pr, dec(Keep, ""), open, older, StatusLeftOpen, ""},
		{"keep pr, others' activity after the decision", pr, dec(Keep, ""), open, newer, StatusDue, "new activity since you left it open"},
		{"keep issue, others' activity after the decision", issue, dec(Keep, ""), open, newer, StatusDue, "new activity since you left it open"},
		{"keep pr closed since", pr, dec(Keep, ""), closed, quiet, StatusDone, ""},
		{"keep pr merged since, with activity", pr, dec(Keep, ""), merged, newer, StatusDone, ""},
		{"keep issue, no observation", issue, dec(Keep, ""), Observed{}, quiet, StatusDone, ""},
		{"keep branch", br, dec(Keep, ""), live, quiet, StatusLeftOpen, ""},
		{"keep worktree", wt, dec(Keep, ""), live, quiet, StatusLeftOpen, ""},
		{"keep repo", repo, dec(Keep, ""), live, quiet, StatusLeftOpen, ""},
		{"keep branch gone", br, dec(Keep, ""), Observed{Known: true}, quiet, StatusDone, ""},
		{"wait, condition not met, quiet", pr, dec(Wait, "date(2026-12-01)"), open, quiet, StatusWaiting, ""},
		{"wait, condition not met, others' activity after", pr, dec(Wait, "date(2026-12-01)"), open, newer, StatusDue, "new activity since Not now"},
		{"watch, others' activity after", issue, dec(Watch, "date(2026-12-01)"), open, newer, StatusDue, "new activity since Not now"},
		{"wait, others' activity before", pr, dec(Wait, "date(2026-12-01)"), open, older, StatusWaiting, ""},
		{"wait, condition met", pr, dec(Wait, "date(2026-10-01)"), open, quiet, StatusDue, "its condition was met"},
		{"wait, condition met and activity", pr, dec(Wait, "date(2026-10-01)"), open, newer, StatusDue, "its condition was met"},
		{"ignore, others' activity after", pr, dec(Ignore, ""), open, newer, StatusDone, ""},
	}
	for _, tc := range cases {
		if got := Compute(tc.k, tc.d, tc.obs, tc.f, now, false); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
		if got := DueReason(tc.k, tc.d, tc.obs, tc.f, now); got != tc.reason {
			t.Errorf("%s: reason %q, want %q", tc.name, got, tc.reason)
		}
	}
}
