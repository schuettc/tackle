package item

import (
	"slices"
	"testing"
	"time"
)

func TestPolicyEvaluate(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	p := DefaultPolicy()
	ago := func(d int) time.Time { return now.Add(-time.Duration(d) * 24 * time.Hour) }
	rules := func(h []Hit) []string {
		var r []string
		for _, x := range h {
			r = append(r, x.Rule)
		}
		return r
	}
	cases := []struct {
		name    string
		s       Signals
		ignored bool
		want    []string
	}{
		{"outgoing stale", Signals{Kind: KindPR, Direction: "outgoing", Open: true, UpdatedAt: ago(20)}, false, []string{"outgoing-stale"}},
		{"outgoing fresh", Signals{Kind: KindPR, Direction: "outgoing", Open: true, UpdatedAt: ago(3)}, false, nil},
		{"outgoing closed", Signals{Kind: KindPR, Direction: "outgoing", Open: false, UpdatedAt: ago(90)}, false, nil},
		{"incoming unanswered", Signals{Kind: KindIssue, Direction: "incoming", Open: true, LastActivity: ago(10)}, false, []string{"incoming-no-reply"}},
		{"incoming answered", Signals{Kind: KindIssue, Direction: "incoming", Open: true, LastActivity: ago(10), LastReplyByMe: ago(10)}, false, nil},
		{"incoming recent", Signals{Kind: KindPR, Direction: "incoming", Open: true, LastActivity: ago(2)}, false, nil},
		{"dormant undecided", Signals{Kind: KindRepo, UpdatedAt: ago(400), Undecided: true}, false, []string{"dormant"}},
		{"dormant decided", Signals{Kind: KindRepo, UpdatedAt: ago(400)}, false, nil},
		{"dormant archived", Signals{Kind: KindRepo, UpdatedAt: ago(400), Undecided: true, Archived: true}, false, nil},
		{"unpushed old", Signals{Kind: KindBranch, OldestUnpushed: ago(5), UnpushedWhere: "mbp:/x"}, false, []string{"unpushed"}},
		{"unpushed new", Signals{Kind: KindBranch, OldestUnpushed: ago(1)}, false, nil},
		{"worktree unpushed", Signals{Kind: KindWorktree, OldestUnpushed: ago(9)}, false, []string{"unpushed"}},
		{"ignored", Signals{Kind: KindBranch, OldestUnpushed: ago(50)}, true, nil},
	}
	for _, tc := range cases {
		if got := rules(p.Evaluate(tc.s, tc.ignored, now)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPolicyDecode(t *testing.T) {
	p, err := DecodePolicy([]byte("unpushed_days = 7\n"))
	if err != nil || p.UnpushedDays != 7 || p.OutgoingPRStaleDays != 14 {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := DecodePolicy([]byte("unpushed_dayz = 7\n")); err == nil {
		t.Error("unknown key accepted")
	}
	if _, err := DecodePolicy([]byte("unpushed_days = 0\n")); err == nil {
		t.Error("zero threshold accepted")
	}
	b, err := EncodePolicy(DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodePolicy(b)
	if err != nil || back != DefaultPolicy() {
		t.Fatalf("round trip %+v %v", back, err)
	}
}
