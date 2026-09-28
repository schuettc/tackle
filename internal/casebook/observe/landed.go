package observe

import (
	"context"
	"encoding/json"
	"sort"
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

// cloneRemote picks the remote name whose value matches the clone's repo.
func cloneRemote(c *Clone) string {
	for name, val := range c.Remotes {
		if strings.EqualFold(val, c.Repo) {
			return name
		}
	}
	return "origin"
}

// isDefaultBranch reports whether name is the clone's default or a well-known default.
func isDefaultBranch(name, def string) bool {
	return name == def || name == "main" || name == "master"
}

// ComputeAncestorLanded runs the git merge-base ancestor check for every
// non-default branch in the snapshot. After this call:
//   - LandedState = "yes", Landed = "in <def>", LandedHow = "default-branch",
//     LandedTip = tip  — when the tip is an ancestor of the default branch.
//   - LandedState = "unknown", all others empty — when not an ancestor (the
//     merged-PR check has not yet run).
//
// A git failure also leaves the branch as "unknown".
func ComputeAncestorLanded(ctx context.Context, snap *Snapshot, repos map[string]RepoObs) {
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
		remote := cloneRemote(c)
		for bi := range c.Branches {
			b := &c.Branches[bi]
			if isDefaultBranch(b.Name, def) {
				continue
			}
			// Reset so re-computation is idempotent.
			b.Landed, b.LandedState, b.LandedTip, b.LandedHow = "", "", "", ""

			for _, ref := range []string{
				"refs/remotes/" + remote + "/" + def,
				"refs/heads/" + def,
			} {
				if _, err := gitx.Run(ctx, c.Path, "merge-base", "--is-ancestor", b.Tip, ref); err == nil {
					b.Landed = "in " + def
					b.LandedState = "yes"
					b.LandedTip = b.Tip
					b.LandedHow = "default-branch"
					break
				}
			}
			if b.LandedState == "" {
				b.LandedState = "unknown"
			}
		}
	}
}

// ReposWithUnlandedBranches returns the sorted set of repos (owner/name) that
// have at least one non-default local branch whose LandedState is not "yes".
// Call after ComputeAncestorLanded to find repos that still need a merged-PR
// check.
func ReposWithUnlandedBranches(snap Snapshot, repos map[string]RepoObs) []string {
	seen := map[string]bool{}
	for _, c := range snap.Clones {
		if c.Repo == "" {
			continue
		}
		def := "main"
		if r, ok := repos[strings.ToLower(c.Repo)]; ok && r.DefaultBranch != "" {
			def = r.DefaultBranch
		}
		for _, b := range c.Branches {
			if isDefaultBranch(b.Name, def) {
				continue
			}
			if b.LandedState != "yes" {
				seen[c.Repo] = true
				break
			}
		}
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// ComputeMergedPRLanded checks merged-PR data for every non-default branch
// whose LandedState is not yet "yes". After this call:
//   - LandedState = "yes", Landed = "merged #N", LandedHow = "merged-pr",
//     LandedTip = tip — when a merged PR's headRefOid matches (or is
//     squash-merge ancestor of) the tip.
//   - LandedState = "no"  — when merged list was fetched but no PR matched.
//   - LandedState = "unknown" — when MergedPRList.Fetched is false.
func ComputeMergedPRLanded(ctx context.Context, snap *Snapshot, merged map[string]MergedPRList) {
	for ci := range snap.Clones {
		c := &snap.Clones[ci]
		if c.Repo == "" {
			continue
		}
		ml := merged[strings.ToLower(c.Repo)]
		for bi := range c.Branches {
			b := &c.Branches[bi]
			if b.LandedState == "yes" {
				continue // already confirmed landed
			}
			if !ml.Fetched {
				b.LandedState = "unknown"
				continue
			}
			// Merged list available; search for a matching PR.
			for _, pr := range ml.PRs {
				if pr.HeadRefName != b.Name {
					continue
				}
				if pr.HeadRefOid == b.Tip {
					b.Landed = "merged #" + strconv.Itoa(pr.Number)
					b.LandedState = "yes"
					b.LandedTip = b.Tip
					b.LandedHow = "merged-pr"
					break
				}
				// Squash merge: our tip may be an ancestor of the merged head.
				if _, err := gitx.Run(ctx, c.Path, "merge-base", "--is-ancestor", b.Tip, pr.HeadRefOid); err == nil {
					b.Landed = "merged #" + strconv.Itoa(pr.Number)
					b.LandedState = "yes"
					b.LandedTip = b.Tip
					b.LandedHow = "merged-pr"
					break
				}
			}
			if b.LandedState != "yes" {
				b.LandedState = "no"
			}
		}
	}
}

// ComputeLanded is the combined two-phase landed check. It first runs the git
// ancestor check (ComputeAncestorLanded), then the merged-PR check
// (ComputeMergedPRLanded) for branches still not confirmed as landed.
//
// Use ComputeAncestorLanded + ReposWithUnlandedBranches + FetchMergedPRs +
// ComputeMergedPRLanded directly when you need to skip fetching for repos
// whose branches are all already confirmed as ancestor-landed.
func ComputeLanded(ctx context.Context, snap *Snapshot, repos map[string]RepoObs, merged map[string]MergedPRList) {
	ComputeAncestorLanded(ctx, snap, repos)
	ComputeMergedPRLanded(ctx, snap, merged)
}
