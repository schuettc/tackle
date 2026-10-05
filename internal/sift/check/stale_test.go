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
	// Only the line naming a PR the host reports merged is certain.
	for _, r := range rows {
		if r.Certain != (r.Source.Start == 4) {
			t.Errorf("line %d certain=%v", r.Source.Start, r.Certain)
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
