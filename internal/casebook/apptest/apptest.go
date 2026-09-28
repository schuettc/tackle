// Package apptest builds an initialized, hermetic casebook for tests of the
// packages above app (serve, channel, cli): isolated git and HOME, a bare
// local remote, a scanned clone and a fake gh. Test-only by convention.
package apptest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/app"
	"github.com/schuettc/tackle/internal/casebook/observe"
	"github.com/schuettc/tackle/internal/casebook/testgit"
)

// FakeGh serves a tiny GitHub: user schuettc, no orgs, one repo schuettc/hail
// with open incoming PR (#3 by bob) and open issues (#4 by alice, #5 by carol,
// #6 by frank, #7 by grace, and #8–#19 for TestSinceListsOverruledWithReasons).
// The extra issues give the probe's T5 scenarios enough dedicated items so no
// two scenarios share an item.
type FakeGh struct{}

// Gh answers the queries casebook makes.
func (FakeGh) Gh(_ context.Context, args ...string) ([]byte, error) {
	j := strings.Join(args, " ")
	switch {
	case j == "api user --jq .login":
		return []byte("schuettc\n"), nil
	case strings.HasPrefix(j, "api user/orgs"):
		return []byte(""), nil
	case strings.HasPrefix(j, "auth status"):
		return []byte("ok"), nil
	case strings.Contains(j, "repositoryOwner("):
		return []byte(`{"data":{"repositoryOwner":{"repositories":{"pageInfo":{"hasNextPage":false},"nodes":[
		 {"nameWithOwner":"schuettc/hail","pushedAt":"2026-09-20T00:00:00Z","defaultBranchRef":{"name":"main"},
		  "pullRequests":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":3,"title":"fix nudge","state":"OPEN","createdAt":"2026-08-01T00:00:00Z",
		   "updatedAt":"2026-08-01T00:00:00Z","author":{"login":"bob","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}}]},
		  "issues":{"pageInfo":{"hasNextPage":false},"nodes":[
		   {"number":4,"title":"crash on start","state":"OPEN","createdAt":"2026-08-02T00:00:00Z",
		    "updatedAt":"2026-08-02T00:00:00Z","author":{"login":"alice","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}},
		   {"number":5,"title":"docs missing","state":"OPEN","createdAt":"2026-08-03T00:00:00Z",
		    "updatedAt":"2026-08-03T00:00:00Z","author":{"login":"carol","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}},
		   {"number":6,"title":"perf regression","state":"OPEN","createdAt":"2026-08-05T00:00:00Z",
		    "updatedAt":"2026-08-05T00:00:00Z","author":{"login":"frank","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}},
		   {"number":7,"title":"memory leak","state":"OPEN","createdAt":"2026-08-06T00:00:00Z",
		    "updatedAt":"2026-08-06T00:00:00Z","author":{"login":"grace","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}},
	   {"number":8,"title":"auth timeout","state":"OPEN","createdAt":"2026-08-07T00:00:00Z",
	    "updatedAt":"2026-08-07T00:00:00Z","author":{"login":"henry","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}},
	   {"number":9,"title":"log rotation","state":"OPEN","createdAt":"2026-08-08T00:00:00Z",
	    "updatedAt":"2026-08-08T00:00:00Z","author":{"login":"iris","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}},
	   {"number":10,"title":"rate limit","state":"OPEN","createdAt":"2026-08-09T00:00:00Z",
	    "updatedAt":"2026-08-09T00:00:00Z","author":{"login":"jake","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}},
	   {"number":11,"title":"dark mode","state":"OPEN","createdAt":"2026-08-10T00:00:00Z",
	    "updatedAt":"2026-08-10T00:00:00Z","author":{"login":"kara","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}},
	   {"number":12,"title":"mobile layout","state":"OPEN","createdAt":"2026-08-11T00:00:00Z",
	    "updatedAt":"2026-08-11T00:00:00Z","author":{"login":"leo","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}},
	   {"number":13,"title":"api versioning","state":"OPEN","createdAt":"2026-08-12T00:00:00Z",
	    "updatedAt":"2026-08-12T00:00:00Z","author":{"login":"mia","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}},
	   {"number":14,"title":"search index","state":"OPEN","createdAt":"2026-08-13T00:00:00Z",
	    "updatedAt":"2026-08-13T00:00:00Z","author":{"login":"noah","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}},
	   {"number":15,"title":"webhook retry","state":"OPEN","createdAt":"2026-08-14T00:00:00Z",
	    "updatedAt":"2026-08-14T00:00:00Z","author":{"login":"olga","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}},
	   {"number":16,"title":"cache invalidation","state":"OPEN","createdAt":"2026-08-15T00:00:00Z",
	    "updatedAt":"2026-08-15T00:00:00Z","author":{"login":"pete","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}},
	   {"number":17,"title":"migration tool","state":"OPEN","createdAt":"2026-08-16T00:00:00Z",
	    "updatedAt":"2026-08-16T00:00:00Z","author":{"login":"quinn","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}},
	   {"number":18,"title":"retry backoff","state":"OPEN","createdAt":"2026-08-17T00:00:00Z",
	    "updatedAt":"2026-08-17T00:00:00Z","author":{"login":"rosa","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}},
	   {"number":19,"title":"export api","state":"OPEN","createdAt":"2026-08-18T00:00:00Z",
	    "updatedAt":"2026-08-18T00:00:00Z","author":{"login":"sam","__typename":"User"},"bodyText":"","labels":{"nodes":[]},"comments":{"nodes":[]}}]}}]}}}}`), nil
	case strings.Contains(j, "search("):
		return []byte(`{"data":{"search":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}`), nil
	case strings.Contains(j, "k0:"):
		return []byte(`{"data":{}}`), nil
	case strings.Contains(j, "pr list") && strings.Contains(j, "--state merged"):
		// Merged PR list: no merged PRs in the test fixture.
		return []byte(`[]`), nil
	case strings.Contains(j, "pr list") && strings.Contains(j, "--state open") && strings.Contains(j, "author"):
		// `gh pr list --json author` returns author objects with `login` and
		// `is_bot` (no `__typename`). hail's only open PR (#3) is by bob, a human.
		return []byte(`[{"author":{"login":"bob","is_bot":false}}]`), nil
	case strings.Contains(j, "pr view") && strings.Contains(j, "updatedAt"):
		return []byte(`{"updatedAt":"2026-08-01T00:00:00Z"}`), nil
	case strings.Contains(j, "pr view") && strings.Contains(j, "--json state"):
		// Agent-lane step verification: report PR as CLOSED.
		return []byte(`{"state":"CLOSED"}`), nil
	case strings.Contains(j, "issue view") && strings.Contains(j, "--json state"):
		// Agent-lane step verification: report issue as CLOSED.
		return []byte(`{"state":"CLOSED"}`), nil
	case strings.Contains(j, "repo view") && strings.Contains(j, "--json isArchived"):
		// Agent-lane step verification: repo is archived.
		return []byte(`{"isArchived":true}`), nil
	case strings.HasPrefix(j, "repo view "):
		// Agent-lane step verification: repo exists.
		return []byte(`{}`), nil
	}
	return nil, fmt.Errorf("apptest.FakeGh: unexpected %s", j)
}

var _ observe.Runner = FakeGh{}

// Rig is an initialized casebook.
type Rig struct {
	App    *app.App
	Remote string // the bare casebook-data remote
	Root   string // the scan root
	Now    time.Time
}

// New initializes a casebook, syncs it once (so the index has items:
// repo:schuettc/hail, pr:schuettc/hail#3, issue:schuettc/hail#4 through #7,
// and the branch/worktree items from the scanned clone), and opens it.
func New(t testing.TB) *Rig {
	t.Helper()
	ctx := context.Background()
	testgit.Env(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CASEBOOK_HOME", filepath.Join(home, "casebook-home"))
	r := &Rig{Remote: testgit.NewBare(t), Now: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)}
	r.Root, _ = filepath.EvalSymlinks(t.TempDir())
	clone := filepath.Join(r.Root, "hail")
	os.MkdirAll(clone, 0o755)
	testgit.Git(t, clone, "init", "-q", "-b", "main")
	c1 := testgit.Commit(t, clone, "a", "1")
	testgit.Git(t, clone, "remote", "add", "origin", "git@github.com:schuettc/hail.git")
	testgit.Git(t, clone, "update-ref", "refs/remotes/origin/main", c1)
	testgit.Git(t, clone, "switch", "-q", "-c", "feat/client")
	testgit.Commit(t, clone, "b", "2")
	gh := FakeGh{}
	if _, err := app.Init(ctx, app.InitOptions{Remote: r.Remote, Machine: "mbp", User: "schuettc", Roots: []string{r.Root}}, gh); err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(gh)
	if err != nil {
		t.Fatal(err)
	}
	a.Now = func() time.Time { return r.Now }
	if _, err := a.Sync(ctx, app.SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	r.App = a
	return r
}
