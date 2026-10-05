package check

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/host"
)

type fakeHost map[host.Ref]host.State

func (f fakeHost) Lookup(_ context.Context, r host.Ref) (host.State, error) {
	s, ok := f[r]
	if !ok {
		return host.State{}, errors.New("not found")
	}
	return s, nil
}

func TestStaleStatus(t *testing.T) {
	hit := inRepo(fixture(t, "stale-status", "hit.md", discover.ClassRepo), "/w/app", "CLAUDE.md")
	in := input(hit)
	in.Host = fakeHost{{Repo: "owner/app", Number: 42}: {Kind: "pr", State: "merged"}}
	rows := only(t, "stale-status", in)
	if got := lines(rows); len(got) != 4 || got[0] != 3 || got[1] != 4 || got[2] != 5 || got[3] != 6 {
		t.Fatalf("lines %v", got)
	}
	// Naming a merged PR is not enough: the line records finished work.
	for _, r := range rows {
		if r.Certain {
			t.Errorf("line %d certain", r.Source.Start)
		}
	}
	if got := evidence(rows[1], "reference"); got != "owner/app#42: pr merged" {
		t.Errorf("reference %q", got)
	}
	if got := evidence(rows[3], "date"); !strings.Contains(got, "2026-01-15") {
		t.Errorf("date %q", got)
	}
}

// Without gh the references are findings to judge, never errors and never
// certain.
func TestStaleStatusWithoutAHost(t *testing.T) {
	hit := inRepo(fixture(t, "stale-status", "hit.md", discover.ClassRepo), "/w/app", "CLAUDE.md")
	rows := only(t, "stale-status", input(hit))
	if len(rows) != 4 {
		t.Fatalf("%+v", rows)
	}
	for _, r := range rows {
		if r.Certain {
			t.Errorf("line %d certain without a host", r.Source.Start)
		}
	}
	if got := evidence(rows[1], "reference"); got != "owner/app#42: state unknown (no gh)" {
		t.Errorf("reference %q", got)
	}
}

func TestStaleStatusNearMiss(t *testing.T) {
	near := inRepo(fixture(t, "stale-status", "near.md", discover.ClassRepo), "/w/app", "CLAUDE.md")
	if rows := only(t, "stale-status", input(near)); len(rows) != 0 {
		t.Fatalf("%+v", rows)
	}
}

// "until X lands" stays within one sentence.
func TestStalePhraseStaysInASentence(t *testing.T) {
	f := inRepo(&discover.File{Class: discover.ClassRepo,
		Content: "- Merge once CI is green. Anything merged that changes a release ships next week.\n"}, "/w/app", "CLAUDE.md")
	if rows := only(t, "stale-status", input(f)); len(rows) != 0 {
		t.Fatalf("%+v", rows)
	}
}

// A host error is reported by its first line only.
func TestStaleHostErrorFirstLine(t *testing.T) {
	f := inRepo(&discover.File{Class: discover.ClassRepo, Content: "- See #7.\n"}, "/w/app", "CLAUDE.md")
	in := input(f)
	in.Host = errHost{}
	rows := only(t, "stale-status", in)
	if len(rows) != 1 || evidence(rows[0], "reference") != "owner/app#7: state unknown (gh: not logged in)" {
		t.Fatalf("%+v", rows)
	}
}

type errHost struct{}

func (errHost) Lookup(context.Context, host.Ref) (host.State, error) {
	return host.State{}, errors.New("gh: not logged in\nrun gh auth login")
}

// A row is certain only when the line waits on a PR, in the same sentence,
// and the host reports it merged or closed, with no reference on the line
// still open.
func TestStaleStatusCertainOnlyWhenWaiting(t *testing.T) {
	h := fakeHost{
		{Repo: "owner/tool", Number: 7}: {Kind: "pr", State: "merged"},
		{Repo: "owner/tool", Number: 8}: {Kind: "pr", State: "open"},
		{Repo: "owner/app", Number: 9}:  {Kind: "pr", State: "closed"},
		{Repo: "owner/app", Number: 3}:  {Kind: "issue", State: "closed"},
	}
	for _, c := range []struct {
		line    string
		certain bool
	}{
		{"- Pin the old client until owner/tool#7 merges.", true},
		{"- Keep the shim once #9 lands; then drop it.", true},
		{"- Pin the old client until PR owner/tool#7 merges.", true},
		{"- Waiting on owner/tool#7 for the new flag.", true},
		{"- Blocked on #9.", true},
		{"- Use the fallback after owner/tool#7 merges.", true},
		{"- These patches shipped in owner/tool#7.", false},
		{"- Pin the old client until owner/tool#7 merges and owner/tool#8 lands.", false},
		{"- Waiting on review. See owner/tool#7.", false},
		{"- Waiting on #3.", false},
		{"- Pin the old client until owner/tool#8 merges.", false},
	} {
		f := inRepo(&discover.File{Class: discover.ClassRepo, Content: c.line + "\n"}, "/w/app", "CLAUDE.md")
		in := input(f)
		in.Host = h
		rows := only(t, "stale-status", in)
		if len(rows) != 1 || rows[0].Certain != c.certain {
			t.Errorf("%q: want certain=%v, got %+v", c.line, c.certain, rows)
		}
	}
}

// The wait phrases are configurable, beside the stale phrases.
func TestStaleWaitPhrasesAreConfigurable(t *testing.T) {
	f := inRepo(&discover.File{Class: discover.ClassRepo, Content: "- Hold the release pending #9.\n"}, "/w/app", "CLAUDE.md")
	in := input(f)
	in.Host = fakeHost{{Repo: "owner/app", Number: 9}: {Kind: "pr", State: "merged"}}
	if rows := only(t, "stale-status", in); len(rows) != 1 || rows[0].Certain {
		t.Fatalf("default phrases: %+v", rows)
	}
	in.Config.Stale.Waits = []string{`\bpending {ref}`}
	if rows := only(t, "stale-status", in); len(rows) != 1 || !rows[0].Certain || evidence(rows[0], "waits on") != "owner/app#9" {
		t.Fatalf("configured phrase: %+v", rows)
	}
}
