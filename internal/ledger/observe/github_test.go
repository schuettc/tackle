package observe

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/ledger/item"
)

var ctx = context.Background()

// fakeGh answers gh invocations from a handler. field returns a -f/-F value.
type fakeGh struct {
	handle func(args []string) (string, error)
	calls  [][]string
}

func (f *fakeGh) Gh(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	out, err := f.handle(args)
	return []byte(out), err
}

func field(args []string, name string) string {
	for i, a := range args {
		if (a == "-f" || a == "-F") && i+1 < len(args) && strings.HasPrefix(args[i+1], name+"=") {
			return strings.TrimPrefix(args[i+1], name+"=")
		}
	}
	return ""
}

func isQuery(args []string, marker string) bool {
	return len(args) > 1 && args[0] == "api" && args[1] == "graphql" && strings.Contains(field(args, "query"), marker)
}

func samlErr() error {
	return &GhError{Code: 1, Stderr: "gh: Resource protected by organization SAML enforcement. You must grant your OAuth token access to this organization. (HTTP 403)"}
}

const ownerPage = `{"data":{"repositoryOwner":{"repositories":{"pageInfo":{"hasNextPage":%t,"endCursor":"c1"},"nodes":[%s]}}}}`

const repoNode = `{"nameWithOwner":%q,"isArchived":false,"isFork":false,"isPrivate":true,"pushedAt":"2025-01-02T00:00:00Z",
 "parent":null,"defaultBranchRef":{"name":"main"},"latestRelease":null,
 "pullRequests":{"pageInfo":{"hasNextPage":false},"nodes":[%s]},
 "issues":{"pageInfo":{"hasNextPage":false},"nodes":[]}}`

const prNode = `{"number":%d,"title":"t","url":"u","state":"OPEN","isDraft":false,"createdAt":"2026-09-01T00:00:00Z",
 "updatedAt":"2026-09-02T00:00:00Z","author":{"login":%q},"repository":{"nameWithOwner":%q},
 "comments":{"nodes":[{"author":{"login":"schuettc"},"createdAt":"2026-09-03T00:00:00Z"}]}}`

func TestRefreshHappyPath(t *testing.T) {
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	gh := &fakeGh{handle: func(a []string) (string, error) {
		j := strings.Join(a, " ")
		switch {
		case j == "api user --jq .login":
			return "schuettc\n", nil
		case strings.HasPrefix(j, "api user/orgs"):
			return "Acme\n", nil
		case strings.HasPrefix(j, "api orgs/Acme/repos"):
			return "1\n", nil
		case isQuery(a, "repositoryOwner") && field(a, "login") == "schuettc":
			return fmt.Sprintf(ownerPage, false, fmt.Sprintf(repoNode, "schuettc/hail", fmt.Sprintf(prNode, 3, "bob", "schuettc/hail"))), nil
		case isQuery(a, "repositoryOwner") && field(a, "login") == "Acme":
			return fmt.Sprintf(ownerPage, false, fmt.Sprintf(repoNode, "Acme/widget", "")), nil
		case isQuery(a, "search("):
			return `{"data":{"search":{"pageInfo":{"hasNextPage":false},"nodes":[` + fmt.Sprintf(prNode, 5, "schuettc", "up/stream") + `]}}}`, nil
		case isQuery(a, "k0:"):
			return `{"data":{"k0":{"pullRequest":{"state":"MERGED","updatedAt":"2026-09-20T00:00:00Z"}},
			 "k1":{"isArchived":false,"pushedAt":"2026-09-10T00:00:00Z","latestRelease":{"publishedAt":"2026-09-11T00:00:00Z"}},
			 "k2":{"ref":null},"k3":null},
			 "errors":[{"type":"NOT_FOUND","path":["k3","issue"],"message":"Could not resolve to an Issue"}]}`, &GhError{Code: 1}
		}
		return "", fmt.Errorf("unexpected gh %s", j)
	}}
	keys := []item.Key{item.PRKey("up/stream", 5), item.RepoKey("up/stream"), item.BranchKey("schuettc/hail", "gone"), item.IssueKey("schuettc/hail", 404)}
	g, rep := Refresh(ctx, gh, NewGitHub(), RefreshOptions{Keys: keys, Now: now})
	if len(rep.Errors) != 0 {
		t.Fatalf("errors: %v", rep.Errors)
	}
	if g.User != "schuettc" || len(g.Owners) != 2 {
		t.Fatalf("owners %+v", g.Owners)
	}
	own := g.Owners["schuettc"]
	if !own.Reachable || own.Stale || len(own.Repos) != 1 || len(own.Repos[0].PRs) != 1 || own.Repos[0].PRs[0].Author != "bob" || own.Repos[0].PRs[0].LastCommentAuthor != "schuettc" {
		t.Errorf("own %+v", own)
	}
	if acme := g.Owners["acme"]; acme == nil || !acme.Reachable || acme.Repos[0].Repo != "Acme/widget" {
		t.Errorf("acme %+v", acme)
	}
	if len(g.Authored) != 1 || g.Authored[0].Repo != "up/stream" || !g.AuthoredAt.Equal(now) {
		t.Errorf("authored %+v", g.Authored)
	}
	want := map[string]Ref{
		"pr:up/stream#5":            {Exists: true, State: "MERGED", UpdatedAt: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)},
		"repo:up/stream":            {Exists: true, UpdatedAt: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), LatestRelease: time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)},
		"branch:schuettc/hail@gone": {Exists: false},
		"issue:schuettc/hail#404":   {Exists: false},
	}
	for k, w := range want {
		if got, ok := g.Refs[k]; !ok || got != w {
			t.Errorf("ref %s = %+v (%v), want %+v", k, got, ok, w)
		}
	}
	// Variables, never string interpolation, carry owner/name/branch.
	for _, c := range gh.calls {
		if isQuery(c, "k0:") && (strings.Contains(field(c, "query"), "up/stream") || field(c, "o0") != "up" || field(c, "m0") != "5") {
			t.Errorf("lookup not parameterized: %v", c)
		}
	}
}

func TestRefreshAbsenceIsNotEvidence(t *testing.T) {
	then := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	prev := NewGitHub()
	prev.User = "schuettc"
	prev.Owners["acme"] = &Owner{Login: "acme", Reachable: true, FetchedAt: then, Repos: []RepoObs{{Repo: "acme/x"}}}
	prev.Owners["beta"] = &Owner{Login: "beta", Reachable: true, FetchedAt: then, Repos: []RepoObs{{Repo: "beta/y"}}}
	prev.Refs["pr:a/b#1"] = Ref{Exists: true, State: "OPEN"}
	gh := &fakeGh{handle: func(a []string) (string, error) {
		j := strings.Join(a, " ")
		switch {
		case j == "api user --jq .login":
			return "schuettc", nil
		case strings.HasPrefix(j, "api orgs/acme/repos"):
			return "", samlErr()
		case strings.HasPrefix(j, "api orgs/beta/repos"):
			return "1", nil
		case isQuery(a, "repositoryOwner") && field(a, "login") == "schuettc":
			return fmt.Sprintf(ownerPage, false, ""), nil
		case isQuery(a, "repositoryOwner") && field(a, "login") == "beta" && field(a, "after") == "":
			return fmt.Sprintf(ownerPage, true, fmt.Sprintf(repoNode, "beta/z", "")), nil
		case isQuery(a, "repositoryOwner") && field(a, "login") == "beta":
			return "", &GhError{Code: 1, Stderr: "gh: HTTP 502: Bad Gateway"}
		case isQuery(a, "search("):
			return "", &GhError{Code: 1, Stderr: "gh: HTTP 502"}
		case isQuery(a, "k0:"):
			return `{"data":{"k0":null},"errors":[{"type":"FORBIDDEN","path":["k0"],"message":"SAML"}]}`, &GhError{Code: 1}
		}
		return "", fmt.Errorf("unexpected gh %s", j)
	}}
	g, rep := Refresh(ctx, gh, prev, RefreshOptions{Owners: []string{"schuettc", "acme", "beta"}, Keys: []item.Key{item.PRKey("a/b", 1)}, Now: then.Add(24 * time.Hour)})
	acme := g.Owners["acme"]
	if acme.Reachable || !acme.Stale || !strings.Contains(acme.Reason, "SAML") || len(acme.Repos) != 1 || acme.Repos[0].Repo != "acme/x" || !acme.FetchedAt.Equal(then) {
		t.Errorf("acme %+v", acme)
	}
	beta := g.Owners["beta"]
	if !beta.Stale || len(beta.Repos) != 1 || beta.Repos[0].Repo != "beta/y" || !strings.Contains(beta.Reason, "502") {
		t.Errorf("beta kept a partial listing: %+v", beta)
	}
	if r := g.Refs["pr:a/b#1"]; !r.Exists || r.State != "OPEN" {
		t.Errorf("ref overwritten by a failed lookup: %+v", r)
	}
	if len(rep.Errors) < 3 {
		t.Errorf("report %v", rep.Errors)
	}
	if prev.Owners["beta"].Stale {
		t.Error("Refresh mutated prev")
	}
}

func TestRefreshRateLimitStopsFetching(t *testing.T) {
	prev := NewGitHub()
	prev.Owners["zeta"] = &Owner{Login: "zeta", Reachable: true, Repos: []RepoObs{{Repo: "zeta/q"}}}
	n := 0
	gh := &fakeGh{handle: func(a []string) (string, error) {
		j := strings.Join(a, " ")
		switch {
		case j == "api user --jq .login":
			return "schuettc", nil
		case isQuery(a, "repositoryOwner"):
			n++
			return `{"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`, &GhError{Code: 1}
		case strings.HasPrefix(j, "api orgs/"):
			return "1", nil
		}
		return "", fmt.Errorf("unexpected gh %s", j)
	}}
	g, rep := Refresh(ctx, gh, prev, RefreshOptions{Owners: []string{"schuettc", "zeta"}, Keys: []item.Key{item.RepoKey("a/b")}, Now: time.Now()})
	if !rep.RateLimited || n != 1 {
		t.Fatalf("rate limited=%v, owner queries=%d", rep.RateLimited, n)
	}
	if z := g.Owners["zeta"]; !z.Stale || len(z.Repos) != 1 || !strings.Contains(z.Reason, "rate") {
		t.Errorf("zeta %+v", z)
	}
}

func TestRefreshLookupStopsOnRateLimit(t *testing.T) {
	// 45 repo keys → two batches (40 + 5). First batch returns RATE_LIMITED.
	// After the fix, exactly one lookup call should be made and the second
	// batch must not be attempted, leaving prev Refs intact.
	prev := NewGitHub()
	prev.Refs["repo:z/z45"] = Ref{Exists: true} // key in second batch; must survive

	var keys []item.Key
	for i := 0; i < 45; i++ {
		keys = append(keys, item.RepoKey(fmt.Sprintf("org/repo%d", i)))
	}
	keys = append(keys, item.RepoKey("z/z45")) // 46th key, also in second batch

	gh := &fakeGh{handle: func(a []string) (string, error) {
		j := strings.Join(a, " ")
		switch {
		case j == "api user --jq .login":
			return "schuettc", nil
		case isQuery(a, "repositoryOwner"):
			return fmt.Sprintf(ownerPage, false, ""), nil
		case isQuery(a, "search("):
			return `{"data":{"search":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}`, nil
		case isQuery(a, "k0:"):
			return `{"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`, &GhError{Code: 1}
		}
		return "", fmt.Errorf("unexpected gh %s", j)
	}}

	g, rep := Refresh(ctx, gh, prev, RefreshOptions{
		Owners: []string{"schuettc"},
		Keys:   keys,
		Now:    time.Now(),
	})

	// Count calls whose query contains "k0:" (lookup batch calls).
	lookupCalls := 0
	for _, c := range gh.calls {
		if isQuery(c, "k0:") {
			lookupCalls++
		}
	}
	if !rep.RateLimited {
		t.Fatalf("expected RateLimited=true")
	}
	if lookupCalls != 1 {
		t.Fatalf("expected exactly 1 lookup call with k0:, got %d", lookupCalls)
	}
	if r := g.Refs["repo:z/z45"]; r != (Ref{Exists: true}) {
		t.Errorf("prev Ref was mutated or lost: %+v", r)
	}
}

func TestRefreshWithoutGh(t *testing.T) {
	prev := NewGitHub()
	prev.Owners["acme"] = &Owner{Login: "acme", Reachable: true, Repos: []RepoObs{{Repo: "acme/x"}}}
	gh := &fakeGh{handle: func([]string) (string, error) {
		return "", &GhError{Code: 4, Stderr: "gh: To get started with GitHub CLI, please run: gh auth login"}
	}}
	g, rep := Refresh(ctx, gh, prev, RefreshOptions{Now: time.Now()})
	if len(rep.Errors) != 1 || !g.Owners["acme"].Stale || len(g.Owners["acme"].Repos) != 1 {
		t.Fatalf("got %+v %v", g.Owners["acme"], rep.Errors)
	}
}
