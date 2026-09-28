package observe

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/testgit"
)

// recordRunner captures gh invocations for inspection.
type recordRunner struct {
	calls []string
	reply func(args []string) ([]byte, error)
}

func (r *recordRunner) Gh(_ context.Context, args ...string) ([]byte, error) {
	r.calls = append(r.calls, strings.Join(args, " "))
	if r.reply != nil {
		return r.reply(args)
	}
	return []byte("[]"), nil
}

// makeRepo creates a repo with a default branch at commit c1 and optionally
// a feature branch at commit c2 (if c2 != ""). If featureAncestor is true,
// c2 == "" and c1 is used for both main and feat (feat tip is ancestor of main
// because main gets another commit after feat is created).
func setupAncestorRepo(t *testing.T) (dir, featTip, mainTip string) {
	t.Helper()
	testgit.Env(t)
	dir = testgit.NewRepo(t)
	// c1 is the initial commit, on main.
	c1 := testgit.Git(t, dir, "rev-parse", "HEAD")
	// Create feat branch at c1.
	testgit.Git(t, dir, "branch", "feat")
	// Advance main so that feat's tip (c1) is an ancestor of main.
	mainTip = testgit.Commit(t, dir, "advance", "advanced")
	// Set up remotes/origin/main to point to mainTip.
	testgit.Git(t, dir, "update-ref", "refs/remotes/origin/main", mainTip)
	return dir, c1, mainTip
}

func setupDivergedRepo(t *testing.T) (dir, featTip string) {
	t.Helper()
	testgit.Env(t)
	dir = testgit.NewRepo(t)
	c1 := testgit.Git(t, dir, "rev-parse", "HEAD")
	// Advance main (so feat and main diverge from c1).
	testgit.Commit(t, dir, "mainwork", "main work")
	mainTip := testgit.Git(t, dir, "rev-parse", "HEAD")
	testgit.Git(t, dir, "update-ref", "refs/remotes/origin/main", mainTip)
	// Create feat branch at c1 (before main advanced; NOT an ancestor of current main).
	testgit.Git(t, dir, "branch", "feat", c1)
	// Commit on feat so feat's tip is NOT c1 (to make it unambiguously diverged).
	testgit.Git(t, dir, "checkout", "-q", "feat")
	featTip = testgit.Commit(t, dir, "featwork", "feat work")
	testgit.Git(t, dir, "checkout", "-q", "main")
	return dir, featTip
}

func makeSnapForDir(dir, repo, remote string) Snapshot {
	return Snapshot{
		Version: SnapshotVersion,
		Machine: "mbp",
		Clones: []Clone{{
			Path:    dir,
			Repo:    repo,
			Remotes: map[string]string{remote: repo},
		}},
	}
}

func addBranch(snap *Snapshot, cloneIdx int, name, tip string) {
	snap.Clones[cloneIdx].Branches = append(snap.Clones[cloneIdx].Branches, Branch{Name: name, Tip: tip})
}

// TestLandedInDefaultBranchByAncestor: branch tip is an ancestor of
// refs/remotes/origin/main → Landed "in main", LandedHow "default-branch".
func TestLandedInDefaultBranchByAncestor(t *testing.T) {
	dir, featTip, _ := setupAncestorRepo(t)
	snap := makeSnapForDir(dir, "acme/proj", "origin")
	addBranch(&snap, 0, "main", "ignored")
	addBranch(&snap, 0, "feat", featTip)

	repos := map[string]RepoObs{"acme/proj": {Repo: "acme/proj", DefaultBranch: "main"}}
	merged := map[string]MergedPRList{"acme/proj": {Fetched: true, PRs: nil}}

	ComputeLanded(ctx, &snap, repos, merged)

	b := snap.Clones[0].Branches[1] // feat
	if b.Landed != "in main" {
		t.Errorf("Landed = %q, want %q", b.Landed, "in main")
	}
	if b.LandedHow != "default-branch" {
		t.Errorf("LandedHow = %q, want %q", b.LandedHow, "default-branch")
	}
	if b.LandedTip != featTip {
		t.Errorf("LandedTip = %q, want %q", b.LandedTip, featTip)
	}
}

// TestLandedByMergedPRHeadOid: tip not in main; merged PR #7 headRefOid == tip
// → Landed "merged #7", LandedHow "merged-pr".
func TestLandedByMergedPRHeadOid(t *testing.T) {
	dir, featTip := setupDivergedRepo(t)
	snap := makeSnapForDir(dir, "acme/proj", "origin")
	addBranch(&snap, 0, "feat", featTip)

	repos := map[string]RepoObs{"acme/proj": {Repo: "acme/proj", DefaultBranch: "main"}}
	merged := map[string]MergedPRList{
		"acme/proj": {Fetched: true, PRs: []MergedPR{
			{Number: 7, HeadRefName: "feat", HeadRefOid: featTip},
		}},
	}

	ComputeLanded(ctx, &snap, repos, merged)

	b := snap.Clones[0].Branches[0]
	if b.Landed != "merged #7" {
		t.Errorf("Landed = %q, want %q", b.Landed, "merged #7")
	}
	if b.LandedHow != "merged-pr" {
		t.Errorf("LandedHow = %q, want %q", b.LandedHow, "merged-pr")
	}
	if b.LandedTip != featTip {
		t.Errorf("LandedTip = %q, want %q", b.LandedTip, featTip)
	}
}

// TestNotLandedWhenTipDivergesAndNoMergedPR: Fetched=true, no matching PR,
// not an ancestor → Landed "".
func TestNotLandedWhenTipDivergesAndNoMergedPR(t *testing.T) {
	dir, featTip := setupDivergedRepo(t)
	snap := makeSnapForDir(dir, "acme/proj", "origin")
	addBranch(&snap, 0, "feat", featTip)

	repos := map[string]RepoObs{"acme/proj": {Repo: "acme/proj", DefaultBranch: "main"}}
	merged := map[string]MergedPRList{"acme/proj": {Fetched: true, PRs: nil}} // no matching PR

	ComputeLanded(ctx, &snap, repos, merged)

	b := snap.Clones[0].Branches[0]
	if b.Landed != "" {
		t.Errorf("Landed = %q, want %q (not landed)", b.Landed, "")
	}
	// LandedTip should be set (we checked this tip)
	if b.LandedTip != featTip {
		t.Errorf("LandedTip = %q, want %q (should record checked tip)", b.LandedTip, featTip)
	}
}

// TestLandedUnknownWhenMergedListUnavailable: MergedPRList.Fetched=false and
// not an ancestor → Landed "" and LandedHow "" (unknown).
func TestLandedUnknownWhenMergedListUnavailable(t *testing.T) {
	dir, featTip := setupDivergedRepo(t)
	snap := makeSnapForDir(dir, "acme/proj", "origin")
	addBranch(&snap, 0, "feat", featTip)

	repos := map[string]RepoObs{"acme/proj": {Repo: "acme/proj", DefaultBranch: "main"}}
	merged := map[string]MergedPRList{"acme/proj": {Fetched: false}} // unavailable

	ComputeLanded(ctx, &snap, repos, merged)

	b := snap.Clones[0].Branches[0]
	if b.Landed != "" {
		t.Errorf("Landed = %q, want %q (unknown)", b.Landed, "")
	}
	if b.LandedHow != "" {
		t.Errorf("LandedHow = %q, want %q (unknown)", b.LandedHow, "")
	}
	// LandedTip should still be set because we ran the git ancestor check
	if b.LandedTip != featTip {
		t.Errorf("LandedTip = %q, want %q (should record checked tip)", b.LandedTip, featTip)
	}
}

// TestFetchMergedPRsOnlyForReposWithUnlandedCandidates: FetchMergedPRs only
// queries repos in its input list; other repos keep their previous entry.
func TestFetchMergedPRsOnlyForReposWithUnlandedCandidates(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	prJSON := `[{"number":7,"headRefName":"feat","headRefOid":"abc123"}]`
	runner := &recordRunner{reply: func(args []string) ([]byte, error) {
		j := strings.Join(args, " ")
		if strings.Contains(j, "acme/repo-b") {
			return []byte(prJSON), nil
		}
		return nil, fmt.Errorf("unexpected repo: %s", j)
	}}

	// prev has repo-a (which is all landed; the caller already excluded it)
	prev := map[string]MergedPRList{
		"acme/repo-a": {Fetched: true, At: now.Add(-time.Hour), PRs: nil},
	}

	// Only pass repo-b to FetchMergedPRs (repo-a not included, caller filtered it)
	result := FetchMergedPRs(ctx, runner, []string{"acme/repo-b"}, prev, now)

	// repo-a should still be there unchanged (from prev)
	if a, ok := result["acme/repo-a"]; !ok || !a.Fetched {
		t.Errorf("repo-a missing or Fetched changed: %+v", result)
	}
	// repo-b should be fetched
	if b, ok := result["acme/repo-b"]; !ok || !b.Fetched || len(b.PRs) != 1 || b.PRs[0].Number != 7 {
		t.Errorf("repo-b not fetched correctly: %+v", result)
	}
	// Runner should only have been called for repo-b
	for _, call := range runner.calls {
		if strings.Contains(call, "acme/repo-a") {
			t.Errorf("runner called for repo-a (should not be queried): %s", call)
		}
	}
}
