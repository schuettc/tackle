package observe

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/casebook/gitx"
)

// AllRepos returns a map from lower-cased "owner/name" to RepoObs for all
// repos in the GitHub cache.
func AllRepos(g *GitHub) map[string]RepoObs {
	out := map[string]RepoObs{}
	for _, ow := range g.Owners {
		for _, r := range ow.Repos {
			out[strings.ToLower(r.Repo)] = r
		}
	}
	return out
}

// FetchMergedPRs fetches the list of merged pull requests for each repo in
// repos, returning an updated map. Repos not in repos keep their previous
// entry from prev unchanged. A failed fetch sets Fetched=false.
func FetchMergedPRs(ctx context.Context, r Runner, repos []string, prev map[string]MergedPRList, now time.Time) map[string]MergedPRList {
	out := make(map[string]MergedPRList, len(prev)+len(repos))
	for k, v := range prev {
		out[k] = v
	}
	for _, repo := range repos {
		key := strings.ToLower(repo)
		b, err := r.Gh(ctx, "pr", "list", "-R", repo, "--state", "merged", "--limit", "1000", "--json", "number,headRefName,headRefOid")
		if err != nil {
			out[key] = MergedPRList{Fetched: false}
			continue
		}
		var prs []MergedPR
		if err := json.Unmarshal(b, &prs); err != nil {
			out[key] = MergedPRList{Fetched: false}
			continue
		}
		out[key] = MergedPRList{Fetched: true, At: now.UTC(), PRs: prs}
	}
	return out
}

// ComputeLanded determines whether each non-default local branch in the
// snapshot has landed: its tip is an ancestor of the default branch, or it
// matches a merged PR (squash-merge aware). It modifies the snapshot in-place.
//
// After ComputeLanded runs, each non-default branch has LandedTip set to the
// tip SHA that was checked. This sentinel distinguishes "checked and not landed"
// (LandedTip set, Landed="") from "not yet checked" (both empty).
func ComputeLanded(ctx context.Context, snap *Snapshot, repos map[string]RepoObs, merged map[string]MergedPRList) {
	for ci := range snap.Clones {
		c := &snap.Clones[ci]
		if c.Repo == "" {
			continue
		}
		r := repos[strings.ToLower(c.Repo)]
		def := r.DefaultBranch
		if def == "" {
			def = "main"
		}
		// Pick the remote whose value matches this clone's repo (identity remote).
		remote := "origin"
		for name, val := range c.Remotes {
			if strings.EqualFold(val, c.Repo) {
				remote = name
				break
			}
		}
		ml := merged[strings.ToLower(c.Repo)]
		for bi := range c.Branches {
			b := &c.Branches[bi]
			if b.Name == def || b.Name == "main" || b.Name == "master" {
				continue
			}
			// Record the tip we are about to check (LandedTip != "" means we ran).
			b.LandedTip = b.Tip

			// First: is the tip an ancestor of the default branch?
			for _, ref := range []string{
				"refs/remotes/" + remote + "/" + def,
				"refs/heads/" + def,
			} {
				if _, err := gitx.Run(ctx, c.Path, "merge-base", "--is-ancestor", b.Tip, ref); err == nil {
					b.Landed = "in " + def
					b.LandedHow = "default-branch"
					break
				}
			}
			if b.Landed != "" {
				continue
			}

			// Second: check merged PRs (squash-merge aware).
			if !ml.Fetched {
				// Cannot determine; leave Landed="" (unknown).
				continue
			}
			for _, pr := range ml.PRs {
				if pr.HeadRefName != b.Name {
					continue
				}
				if pr.HeadRefOid == b.Tip {
					b.Landed = "merged #" + strconv.Itoa(pr.Number)
					b.LandedHow = "merged-pr"
					break
				}
				// Squash merge: our tip may be an ancestor of the merged head.
				if _, err := gitx.Run(ctx, c.Path, "merge-base", "--is-ancestor", b.Tip, pr.HeadRefOid); err == nil {
					b.Landed = "merged #" + strconv.Itoa(pr.Number)
					b.LandedHow = "merged-pr"
					break
				}
			}
		}
	}
}
