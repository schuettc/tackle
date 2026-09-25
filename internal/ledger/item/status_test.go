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
		{"keep", repo, dec(Keep, ""), live, false, StatusDone},
		{"keep with unknown observation", repo, dec(Keep, ""), Observed{}, false, StatusDone},
		{"keep snooze due", repo, dec(Keep, "date(2026-09-01)"), live, false, StatusDue},
		{"keep snooze pending", repo, dec(Keep, "date(2026-12-01)"), live, false, StatusDone},
		{"ignore", repo, dec(Ignore, ""), live, false, StatusDone},
	}
	for _, tc := range cases {
		if got := Compute(tc.k, tc.d, tc.obs, f, now, tc.seen); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}
