package apply

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/observe"
)

// ObserveGh makes a targeted gh read to check the outcome of an agent-lane
// step (§5.5). It returns an item.Observed that Verify can use to promote a
// reported step to verified, or flag it as drift.
//
// For pr-close and pr-merge, a fresh gh pr view checks the state.
// For issue-close, a fresh gh issue view checks the state.
// For repo-archive, a fresh gh repo view checks isArchived.
// For repo-delete, a fresh gh repo view confirms the repo is gone.
// Any other action or a nil gh runner returns an inconclusive observation.
func ObserveGh(ctx context.Context, step JobStep, gh observe.Runner) item.Observed {
	if gh == nil {
		return item.Observed{}
	}
	k, err := item.ParseKey(step.Key)
	if err != nil {
		return item.Observed{}
	}
	switch step.Action {
	case "pr-close", "pr-merge":
		b, err := gh.Gh(ctx, "pr", "view", fmt.Sprint(k.Number), "-R", k.Repo(), "--json", "state")
		if err != nil {
			return item.Observed{}
		}
		var v struct {
			State string `json:"state"`
		}
		if json.Unmarshal(b, &v) != nil {
			return item.Observed{}
		}
		return item.Observed{Known: true, Exists: true, State: v.State}

	case "issue-close":
		b, err := gh.Gh(ctx, "issue", "view", fmt.Sprint(k.Number), "-R", k.Repo(), "--json", "state")
		if err != nil {
			return item.Observed{}
		}
		var v struct {
			State string `json:"state"`
		}
		if json.Unmarshal(b, &v) != nil {
			return item.Observed{}
		}
		return item.Observed{Known: true, Exists: true, State: v.State}

	case "repo-archive":
		b, err := gh.Gh(ctx, "repo", "view", k.Repo(), "--json", "isArchived")
		if err != nil {
			return item.Observed{}
		}
		var v struct {
			IsArchived bool `json:"isArchived"`
		}
		if json.Unmarshal(b, &v) != nil {
			return item.Observed{}
		}
		return item.Observed{Known: true, Exists: true, Archived: v.IsArchived}

	case "repo-delete":
		_, err := gh.Gh(ctx, "repo", "view", k.Repo())
		// err != nil means the repo doesn't exist (was deleted).
		return item.Observed{Known: true, Exists: err == nil}
	}
	return item.Observed{}
}
