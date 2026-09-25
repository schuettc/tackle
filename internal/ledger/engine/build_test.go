package engine

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/ledger/item"
	"github.com/schuettc/tackle/internal/ledger/journal"
	"github.com/schuettc/tackle/internal/ledger/observe"
)

var now = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func days(n int) time.Time { return now.Add(-time.Duration(n) * 24 * time.Hour) }

func fixture() Input {
	g := observe.NewGitHub()
	g.User = "schuettc"
	g.Owners["schuettc"] = &observe.Owner{Login: "schuettc", Reachable: true, FetchedAt: now, Repos: []observe.RepoObs{
		{Repo: "schuettc/hail", PushedAt: days(2), DefaultBranch: "main",
			PRs:    []observe.PRObs{{Repo: "schuettc/hail", Number: 3, Author: "bob", State: "OPEN", CreatedAt: days(20), UpdatedAt: days(20), Title: "fix"}},
			Issues: []observe.PRObs{{Repo: "schuettc/hail", Number: 4, Author: "schuettc", State: "OPEN", CreatedAt: days(1), UpdatedAt: days(1)}}},
		{Repo: "schuettc/pi-usage", Fork: true, Parent: "Sreetej510/pi-extensions", PushedAt: days(1)},
		{Repo: "schuettc/old", PushedAt: days(800)},
	}}
	g.Owners["acme"] = &observe.Owner{Login: "Acme", Reachable: false, Stale: true, Reason: "SAML SSO", FetchedAt: days(30),
		Repos: []observe.RepoObs{{Repo: "Acme/widget", PushedAt: days(40)}}}
	g.Authored = []observe.PRObs{{Repo: "elidickinson/pi-claude-bridge", Number: 97, Author: "schuettc", State: "OPEN", CreatedAt: days(30), UpdatedAt: days(20)}}
	g.Refs["pr:up/stream#5"] = observe.Ref{Exists: true, State: "MERGED"}
	snap := observe.Snapshot{Version: 1, Machine: "mbp", Clones: []observe.Clone{{
		Path: "/c/hail", Repo: "schuettc/hail",
		Branches: []observe.Branch{
			{Name: "main", Tip: "a"},
			{Name: "feat/client", Tip: "b", Unpushed: 2, OldestUnpushed: days(10)},
			{Name: "fix/landed", Tip: "c", Upstream: "origin/fix/landed", Gone: true, Unpushed: 1, OldestUnpushed: days(30)},
		},
		Worktrees: []observe.Worktree{{Path: "/w/hail-client", Branch: "feat/client", Dirty: true}},
	}}}
	return Input{Now: now, GitHub: g, Snapshots: []observe.Snapshot{snap}, Decisions: map[string]item.Decision{}, Policy: item.DefaultPolicy(), Seen: map[string]time.Time{}}
}

func decide(in Input, key string, d item.Disposition, until string) {
	in.Decisions[key] = item.Decision{Disposition: d, Until: until, DecidedBy: "court", DecidedAt: days(1)}
}

func mustFind(t *testing.T, r Result, id string) Item {
	t.Helper()
	it, ok := r.Find(id)
	if !ok {
		t.Fatalf("item %s missing", id)
	}
	return it
}

func hitRules(it Item) []string {
	var r []string
	for _, h := range it.Hits {
		r = append(r, h.Rule)
	}
	return r
}

func TestBuildUniverseAndRelations(t *testing.T) {
	r := Build(fixture())
	for id, rel := range map[string]string{
		"repo:schuettc/hail":                  "owned",
		"repo:schuettc/pi-usage":              "fork-of:sreetej510/pi-extensions",
		"repo:acme/widget":                    "org",
		"pr:schuettc/hail#3":                  "incoming",
		"issue:schuettc/hail#4":               "own",
		"pr:elidickinson/pi-claude-bridge#97": "outgoing",
	} {
		if it := mustFind(t, r, id); it.Relation != rel || it.Status != item.StatusNew {
			t.Errorf("%s: relation %q status %s", id, it.Relation, it.Status)
		}
	}
	if _, ok := r.Find("branch:schuettc/hail@main"); ok {
		t.Error("default branch became an item")
	}
	br := mustFind(t, r, "branch:schuettc/hail@feat/client")
	if !slices.Equal(br.Locations, []string{"mbp:/c/hail"}) || !slices.Equal(hitRules(br), []string{"unpushed"}) {
		t.Errorf("branch %+v", br)
	}
	landed := mustFind(t, r, "branch:schuettc/hail@fix/landed")
	if len(landed.Hits) != 0 || !strings.Contains(strings.Join(landed.Evidence, " "), "deleted") {
		t.Errorf("gone-upstream branch flagged as unpushed work: %+v", landed)
	}
	wt := mustFind(t, r, "worktree:mbp:/w/hail-client")
	if !wt.Observed.Exists || !slices.Equal(hitRules(wt), []string{"unpushed"}) {
		t.Errorf("worktree %+v", wt)
	}
	if !slices.Equal(hitRules(mustFind(t, r, "pr:schuettc/hail#3")), []string{"incoming-no-reply"}) {
		t.Error("incoming PR not flagged")
	}
	if !slices.Equal(hitRules(mustFind(t, r, "pr:elidickinson/pi-claude-bridge#97")), []string{"outgoing-stale"}) {
		t.Error("outgoing PR not flagged")
	}
	if !slices.Equal(hitRules(mustFind(t, r, "repo:schuettc/old")), []string{"dormant"}) {
		t.Error("dormant repo not flagged")
	}
	if w := mustFind(t, r, "repo:acme/widget"); !w.Stale {
		t.Error("repo from a stale owner not marked stale")
	}
	if len(r.Notices) != 1 {
		t.Errorf("notices %v", r.Notices)
	}
}

func TestDecidedRepoMissingFromStaleOwnerIsUnknown(t *testing.T) {
	in := fixture()
	decide(in, "repo:acme/gone", item.Delete, "")
	decide(in, "repo:schuettc/gone", item.Delete, "")
	decide(in, "repo:stranger/thing", item.Archive, "")
	r := Build(in)
	a := mustFind(t, r, "repo:acme/gone")
	if a.Observed.Known || a.Status != item.StatusToApply || !a.Stale {
		t.Errorf("stale owner: %+v", a)
	}
	if s := mustFind(t, r, "repo:schuettc/gone"); !s.Observed.Known || s.Observed.Exists || s.Status != item.StatusDone {
		t.Errorf("fresh owner: %+v", s)
	}
	if x := mustFind(t, r, "repo:stranger/thing"); x.Observed.Known || x.Status != item.StatusToApply {
		t.Errorf("unobserved owner: %+v", x)
	}
}

func TestUntilAndIgnore(t *testing.T) {
	in := fixture()
	decide(in, "repo:schuettc/pi-usage", item.Watch, "merged(pr:up/stream#5)")
	decide(in, "pr:elidickinson/pi-claude-bridge#97", item.Wait, "date(2026-12-01)")
	decide(in, "repo:schuettc/old", item.Ignore, "")
	r := Build(in)
	if s := mustFind(t, r, "repo:schuettc/pi-usage").Status; s != item.StatusDue {
		t.Errorf("watch status %s", s)
	}
	pr := mustFind(t, r, "pr:elidickinson/pi-claude-bridge#97")
	if pr.Status != item.StatusWaiting || len(pr.Hits) != 1 {
		t.Errorf("wait: %+v (policies still apply to waiting items)", pr)
	}
	if old := mustFind(t, r, "repo:schuettc/old"); old.Status != item.StatusDone || len(old.Hits) != 0 {
		t.Errorf("ignore: %+v", old)
	}
	att := r.Attention()
	for _, it := range att {
		if it.ID == "repo:schuettc/old" {
			t.Error("ignored item in attention")
		}
	}
}

func TestDriftAcrossBuilds(t *testing.T) {
	in := fixture()
	in.GitHub.Owners["schuettc"].Repos[2].Archived = true
	decide(in, "repo:schuettc/old", item.Archive, "")
	r1 := Build(in)
	if s := mustFind(t, r1, "repo:schuettc/old").Status; s != item.StatusDone {
		t.Fatalf("status %s", s)
	}
	in.Seen = NextSeen(r1, in.Seen)
	in.GitHub.Owners["schuettc"].Repos[2].Archived = false
	r2 := Build(in)
	if s := mustFind(t, r2, "repo:schuettc/old").Status; s != item.StatusDrift {
		t.Fatalf("status %s, want drift", s)
	}
	in.Seen = NextSeen(r2, in.Seen)
	in.GitHub.Owners["schuettc"].Stale = true
	in.GitHub.Owners["schuettc"].Repos = nil
	if s := mustFind(t, Build(in), "repo:schuettc/old").Status; s != item.StatusDone {
		t.Fatalf("status %s with no data, want done (last known)", s)
	}
}

func TestDecisionOnBranchAndWorktree(t *testing.T) {
	in := fixture()
	decide(in, "branch:schuettc/hail@feat/client", item.Delete, "")
	decide(in, "worktree:mbp:/w/removed", item.Delete, "")
	decide(in, "worktree:other:/w/x", item.Delete, "")
	in.GitHub.Refs["branch:schuettc/hail@feat/client"] = observe.Ref{Exists: false}
	r := Build(in)
	if b := mustFind(t, r, "branch:schuettc/hail@feat/client"); b.Status != item.StatusToApply {
		t.Errorf("local copy still exists: %+v", b)
	}
	if w := mustFind(t, r, "worktree:mbp:/w/removed"); w.Status != item.StatusDone {
		t.Errorf("gone from its machine's snapshot: %+v", w)
	}
	if w := mustFind(t, r, "worktree:other:/w/x"); w.Observed.Known {
		t.Errorf("no snapshot for machine: %+v", w)
	}
}

func TestLookupKeys(t *testing.T) {
	ds := map[string]item.Decision{
		"repo:a/b":      {Disposition: item.Watch, Until: "released(repo:u/p)"},
		"pr:a/b#1":      {Disposition: item.Keep},
		"worktree:m:/x": {Disposition: item.Keep},
		"branch:a/b@x":  {Disposition: item.Wait, Until: "merged(pr:a/b#1)"},
	}
	var got []string
	for _, k := range LookupKeys(ds) {
		got = append(got, k.String())
	}
	want := []string{"branch:a/b@x", "pr:a/b#1", "repo:a/b", "repo:u/p"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRepoForPathAndHistory(t *testing.T) {
	snaps := fixture().Snapshots
	for p, want := range map[string]string{"/c/hail": "schuettc/hail", "/c/hail/sub": "schuettc/hail", "/w/hail-client/x": "schuettc/hail", "/c/hailstorm": ""} {
		if got := RepoForPath(snaps, "mbp", p); got != want {
			t.Errorf("RepoForPath(%s) = %q, want %q", p, got, want)
		}
	}
	evs := []journal.Event{
		{Src: "git-hook", Hook: "pre-push", Repo: "schuettc/hail", Machine: "mbp", Stdin: [][]string{{"refs/heads/feat/client", "b", "refs/heads/feat/client", "0"}}},
		{Src: "claude", Repo: "schuettc/hail", Actions: []journal.Action{{Tool: "gh", Verb: "pr merge", Repo: "schuettc/hail", Number: 3}}},
		{Src: "git-hook", Hook: "post-commit", Repo: "schuettc/other"},
		{Src: "git-hook", Hook: "post-checkout", Machine: "mbp", CWD: "/w/hail-client"},
	}
	count := func(id string) int {
		k, _ := item.ParseKey(id)
		return len(History(evs, k))
	}
	if count("repo:schuettc/hail") != 2 || count("branch:schuettc/hail@feat/client") != 1 || count("pr:schuettc/hail#3") != 1 || count("worktree:mbp:/w/hail-client") != 1 {
		t.Errorf("history counts: repo %d branch %d pr %d worktree %d", count("repo:schuettc/hail"), count("branch:schuettc/hail@feat/client"), count("pr:schuettc/hail#3"), count("worktree:mbp:/w/hail-client"))
	}
}
