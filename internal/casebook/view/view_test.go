package view

import (
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/observe"
)

var now = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func result() engine.Result {
	return engine.Result{
		Notices: []string{"owner Acme unreachable: SAML SSO"},
		Items: []engine.Item{
			{ID: "branch:schuettc/hail@feat/client", Kind: item.KindBranch, Status: item.StatusNew, Hits: []item.Hit{{Rule: "unpushed", Detail: "local-only commits for 10d on mbp:/c/hail"}}},
			{ID: "pr:elidickinson/pi-claude-bridge#97", Kind: item.KindPR, Relation: "outgoing", Status: item.StatusWaiting, Title: "side | request\n<b>x</b>",
				URL:      "https://github.com/elidickinson/pi-claude-bridge/pull/97",
				Decision: &item.Decision{Disposition: item.Wait, Until: "date(2026-12-01)", DecidedBy: "court", DecidedAt: now.Add(-48 * time.Hour)}},
			{ID: "repo:schuettc/old", Kind: item.KindRepo, Status: item.StatusConflict, Decision: &item.Decision{Disposition: item.Keep, DecidedBy: "a", DecidedAt: now, Conflict: &item.Conflict{Disposition: item.Archive, DecidedBy: "b"}}},
			{ID: "repo:schuettc/hail", Kind: item.KindRepo, Status: item.StatusDone, Decision: &item.Decision{Disposition: item.Keep, DecidedBy: "court", DecidedAt: now}},
		},
	}
}

func TestRender(t *testing.T) {
	snaps := []observe.Snapshot{{Machine: "mbp", Clones: []observe.Clone{
		{Path: "/c/hail", Repo: "schuettc/hail", Dirty: true, Stashes: 2, Branches: []observe.Branch{{Name: "feat/client", Unpushed: 2}}},
		{Path: "/c/husky", Repo: "a/b", LocalHooksPath: ".husky"},
		{Path: "/c/local-only"},
	}}}
	out := Render(result(), snaps)
	readme := string(out["README.md"])
	for _, want := range []string{"## Conflict", "repo:schuettc/old", "archive by b", "## New", "branch:schuettc/hail@feat/client",
		"local-only commits", "## Waiting", "owner Acme unreachable", "1 done"} {
		if !strings.Contains(readme, want) {
			t.Errorf("README lacks %q:\n%s", want, readme)
		}
	}
	if strings.Index(readme, "## Conflict") > strings.Index(readme, "## New") {
		t.Error("conflicts must come first")
	}
	contrib := string(out["CONTRIBUTIONS.md"])
	if !strings.Contains(contrib, `side \| request &lt;b>x&lt;/b>`) || !strings.Contains(contrib, "wait") {
		t.Errorf("CONTRIBUTIONS:\n%s", contrib)
	}
	machines := string(out["MACHINES.md"])
	for _, want := range []string{"## mbp", "3 clones", "/c/hail", "dirty", "2 stashes", "2 unpushed on feat/client", "not journaled", ".husky", "no GitHub remote", "/c/local-only"} {
		if !strings.Contains(machines, want) {
			t.Errorf("MACHINES lacks %q:\n%s", want, machines)
		}
	}
	again := Render(result(), snaps)
	for k := range out {
		if string(again[k]) != string(out[k]) {
			t.Errorf("%s not deterministic", k)
		}
	}
}

// TestReadmeLeftOpen: a kept item still open is in a Left open lane at the
// end, counted on its own, never Flagged, whatever its flags.
func TestReadmeLeftOpen(t *testing.T) {
	r := result()
	r.Items = append(r.Items, engine.Item{ID: "pr:schuettc/hail#3", Kind: item.KindPR, Status: item.StatusLeftOpen,
		Hits:     []item.Hit{{Rule: "incoming-no-reply", Detail: "waiting on you for 9d (> 7d)"}},
		Decision: &item.Decision{Disposition: item.Keep, DecidedBy: "court", DecidedAt: now}})
	readme := string(Render(r, nil)["README.md"])
	if !strings.Contains(readme, "1 left open") || !strings.Contains(readme, "## Left open (1)") {
		t.Fatalf("README lacks the left-open lane or count:\n%s", readme)
	}
	if strings.Contains(readme, "## Flagged") {
		t.Errorf("a left-open item is flagged:\n%s", readme)
	}
	if strings.Index(readme, "## Left open") < strings.Index(readme, "## Waiting") {
		t.Error("left open must come last")
	}
}
