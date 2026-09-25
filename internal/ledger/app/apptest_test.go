package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/ledger/observe"
	"github.com/schuettc/tackle/internal/ledger/testgit"
)

var ctx = context.Background()

// fakeGh serves a tiny GitHub: user schuettc, no orgs, one repo schuettc/hail
// with one open incoming PR, nothing authored elsewhere, lookups unknown.
type fakeGh struct{ fail bool }

func (f *fakeGh) Gh(_ context.Context, args ...string) ([]byte, error) {
	if f.fail {
		return nil, &observe.GhError{Code: 1, Stderr: "gh: offline"}
	}
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
		  "pullRequests":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":3,"title":"fix","state":"OPEN","createdAt":"2026-09-01T00:00:00Z",
		   "updatedAt":"2026-09-01T00:00:00Z","author":{"login":"bob"},"comments":{"nodes":[]}}]},
		  "issues":{"pageInfo":{"hasNextPage":false},"nodes":[]}}]}}}}`), nil
	case strings.Contains(j, "search("):
		return []byte(`{"data":{"search":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}`), nil
	case strings.Contains(j, "k0:"):
		return []byte(`{"data":{}}`), nil
	}
	return nil, fmt.Errorf("fakeGh: unexpected %s", j)
}

type rig struct {
	app    *App
	remote string
	root   string
	clone  string
	now    time.Time
}

// newRig initializes a ledger against a bare remote with one scanned clone
// (schuettc/hail, branch feat/client with an unpushed commit).
func newRig(t *testing.T) *rig {
	t.Helper()
	testgit.Env(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LEDGER_HOME", filepath.Join(home, "ledger-home"))
	r := &rig{remote: testgit.NewBare(t), now: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)}
	r.root, _ = filepath.EvalSymlinks(t.TempDir())
	r.clone = filepath.Join(r.root, "hail")
	os.MkdirAll(r.clone, 0o755)
	testgit.Git(t, r.clone, "init", "-q", "-b", "main")
	c1 := testgit.Commit(t, r.clone, "a", "1")
	testgit.Git(t, r.clone, "remote", "add", "origin", "git@github.com:schuettc/hail.git")
	testgit.Git(t, r.clone, "update-ref", "refs/remotes/origin/main", c1)
	testgit.Git(t, r.clone, "switch", "-q", "-c", "feat/client")
	testgit.Commit(t, r.clone, "b", "2")
	gh := &fakeGh{}
	if _, err := Init(ctx, InitOptions{Remote: r.remote, Machine: "mbp", User: "schuettc", Roots: []string{r.root}}, gh); err != nil {
		t.Fatal(err)
	}
	a, err := Open(gh)
	if err != nil {
		t.Fatal(err)
	}
	a.Now = func() time.Time { return r.now }
	r.app = a
	return r
}
