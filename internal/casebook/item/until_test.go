package item

import (
	"testing"
	"time"
)

// fakeFacts is shared by this package's tests.
type fakeFacts struct {
	state map[string]string
	rel   map[string]time.Time
	act   map[string]time.Time
}

func (f fakeFacts) State(k Key) (string, bool) { s, ok := f.state[k.String()]; return s, ok }
func (f fakeFacts) LatestRelease(k Key) (time.Time, bool) {
	t, ok := f.rel[k.String()]
	return t, ok
}
func (f fakeFacts) LastActivity(k Key) (time.Time, bool) {
	t, ok := f.act[k.String()]
	return t, ok
}

func TestParseUntilRoundTrip(t *testing.T) {
	for _, s := range []string{"date(2026-10-01)", "merged(pr:elidickinson/pi-claude-bridge#97)",
		"closed(issue:schuettc/muster#112)", "closed(pr:a/b#1)", "inactive(90d)", "inactive(2w)",
		"inactive(36h)", "released(repo:pungggi/pi-schedule)"} {
		c, err := ParseUntil(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if c.String() != s {
			t.Errorf("String() = %q, want %q", c.String(), s)
		}
	}
}

func TestParseUntilRejects(t *testing.T) {
	for _, s := range []string{"", "soon", "date(2026-13-01)", "date()", "merged(issue:a/b#1)",
		"merged(a/b#1)", "released(pr:a/b#1)", "inactive(0d)", "inactive(3m)", "inactive(d)",
		"closed(repo:a/b)", "merged(pr:a/b#1"} {
		if _, err := ParseUntil(s); err == nil {
			t.Errorf("ParseUntil(%q) accepted", s)
		}
	}
}

func TestMet(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	decided := now.Add(-48 * time.Hour)
	it := Key{Kind: KindRepo, Owner: "a", Name: "b"}
	f := fakeFacts{
		state: map[string]string{"pr:a/b#1": "MERGED", "pr:a/b#2": "OPEN", "issue:a/b#3": "CLOSED"},
		rel:   map[string]time.Time{"repo:u/p": now.Add(-time.Hour), "repo:u/old": now.Add(-72 * time.Hour)},
		act:   map[string]time.Time{"repo:a/b": now.Add(-100 * 24 * time.Hour)},
	}
	cases := []struct {
		until      string
		met, known bool
	}{
		{"date(2026-09-24)", true, true}, {"date(2026-09-25)", false, true},
		{"merged(pr:a/b#1)", true, true}, {"merged(pr:a/b#2)", false, true}, {"merged(pr:a/b#9)", false, false},
		{"closed(pr:a/b#1)", true, true}, {"closed(issue:a/b#3)", true, true}, {"closed(pr:a/b#2)", false, true},
		{"released(repo:u/p)", true, true}, {"released(repo:u/old)", false, true}, {"released(repo:u/none)", false, false},
		{"inactive(90d)", true, true}, {"inactive(120d)", false, true},
	}
	for _, tc := range cases {
		c, err := ParseUntil(tc.until)
		if err != nil {
			t.Fatal(err)
		}
		met, known := c.Met(it, decided, f, now)
		if met != tc.met || known != tc.known {
			t.Errorf("%s: met=%v known=%v, want %v %v", tc.until, met, known, tc.met, tc.known)
		}
	}
}
