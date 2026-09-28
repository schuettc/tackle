package apply

import (
	"errors"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/item"
)

// epoch is a fixed clock used by all tests.
var epoch = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func decided(d item.Disposition, note string) *item.Decision {
	return &item.Decision{
		Disposition: d,
		Note:        note,
		DecidedBy:   "court",
		DecidedAt:   epoch.Add(-time.Hour),
	}
}

func branchItem(repo, branch, machine, clonePath string) engine.Item {
	k := item.BranchKey(repo, branch)
	return engine.Item{
		Key:       k,
		ID:        k.String(),
		Kind:      item.KindBranch,
		Repo:      repo,
		Status:    item.StatusToApply,
		Decision:  decided(item.Delete, ""),
		Landed:    "all-machines",
		Locations: []string{machine + ":" + clonePath},
	}
}

func TestPlanGroupsByActionAndLane(t *testing.T) {
	builtAt := epoch.Add(-10 * time.Minute)

	bk := item.BranchKey("schuettc/myrepo", "feat/thing")
	br := engine.Item{
		Key:       bk,
		ID:        bk.String(),
		Kind:      item.KindBranch,
		Status:    item.StatusToApply,
		Decision:  decided(item.Delete, ""),
		Landed:    "all-machines",
		Locations: []string{"mymachine:/Users/me/repos/myrepo"},
	}

	rk := item.RepoKey("schuettc/oldrepo")
	rp := engine.Item{
		Key:      rk,
		ID:       rk.String(),
		Kind:     item.KindRepo,
		Status:   item.StatusToApply,
		Decision: decided(item.Archive, ""),
	}

	plan, err := Build([]engine.Item{br, rp}, "mymachine", epoch, builtAt, 30*time.Minute)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	groups := plan.Groups()
	if len(groups) == 0 {
		t.Fatal("Groups() returned none")
	}

	seen := map[string]bool{}
	for _, g := range groups {
		seen[g.Action] = true
		if len(g.Steps) == 0 {
			t.Errorf("group %q has no steps", g.Action)
		}
	}
	for _, want := range []string{"branch-delete-local", "branch-delete-remote", "repo-archive"} {
		if !seen[want] {
			t.Errorf("no group for action %q; groups: %v", want, groups)
		}
	}

	// Groups should preserve first-seen order.
	if groups[0].Action != "branch-delete-local" {
		t.Errorf("groups[0].Action = %q, want branch-delete-local", groups[0].Action)
	}
}

func TestBranchDeleteIsCasebookLaneWithExactCommand(t *testing.T) {
	builtAt := epoch.Add(-5 * time.Minute)

	k := item.BranchKey("schuettc/myrepo", "feat/my-branch")
	it := engine.Item{
		Key:       k,
		ID:        k.String(),
		Kind:      item.KindBranch,
		Status:    item.StatusToApply,
		Decision:  decided(item.Delete, ""),
		Landed:    "all-machines",
		Locations: []string{"mymachine:/Users/me/repos/myrepo"},
	}

	plan, err := Build([]engine.Item{it}, "mymachine", epoch, builtAt, 30*time.Minute)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("want 2 steps (local + remote), got %d: %v", len(plan.Steps), plan.Steps)
	}

	local := plan.Steps[0]
	if local.Action != "branch-delete-local" {
		t.Errorf("Steps[0].Action = %q, want branch-delete-local", local.Action)
	}
	if local.Lane != LaneCasebook {
		t.Errorf("Steps[0].Lane = %q, want %q", local.Lane, LaneCasebook)
	}
	if local.Key != k.String() {
		t.Errorf("Steps[0].Key = %q, want %q", local.Key, k.String())
	}
	wantLocal := "git -C '/Users/me/repos/myrepo' branch -D 'feat/my-branch'"
	if local.Command != wantLocal {
		t.Errorf("Steps[0].Command =\n  %q\nwant\n  %q", local.Command, wantLocal)
	}
	if local.Precondition != "branch-tip-unchanged-and-landed" {
		t.Errorf("Steps[0].Precondition = %q, want branch-tip-unchanged-and-landed", local.Precondition)
	}
	if local.Posts {
		t.Error("Steps[0].Posts should be false for local branch delete")
	}

	remote := plan.Steps[1]
	if remote.Action != "branch-delete-remote" {
		t.Errorf("Steps[1].Action = %q, want branch-delete-remote", remote.Action)
	}
	if remote.Lane != LaneCasebook {
		t.Errorf("Steps[1].Lane = %q, want %q", remote.Lane, LaneCasebook)
	}
	wantRemote := "git -C '/Users/me/repos/myrepo' push 'origin' --delete 'feat/my-branch'"
	if remote.Command != wantRemote {
		t.Errorf("Steps[1].Command =\n  %q\nwant\n  %q", remote.Command, wantRemote)
	}
	if remote.Precondition != "remote-tip-unchanged-and-landed" {
		t.Errorf("Steps[1].Precondition = %q, want remote-tip-unchanged-and-landed", remote.Precondition)
	}
}

func TestArchiveAndCloseAreAgentLane(t *testing.T) {
	builtAt := epoch.Add(-5 * time.Minute)

	rk := item.RepoKey("schuettc/oldrepo")
	archive := engine.Item{
		Key:      rk,
		ID:       rk.String(),
		Kind:     item.KindRepo,
		Status:   item.StatusToApply,
		Decision: decided(item.Archive, ""),
	}

	pk := item.PRKey("schuettc/myrepo", 42)
	prClose := engine.Item{
		Key:      pk,
		ID:       pk.String(),
		Kind:     item.KindPR,
		Status:   item.StatusToApply,
		Decision: decided(item.Close, "no activity"),
	}

	plan, err := Build([]engine.Item{archive, prClose}, "mymachine", epoch, builtAt, 30*time.Minute)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d: %v", len(plan.Steps), plan.Steps)
	}

	archiveStep := plan.Steps[0]
	if archiveStep.Lane != LaneAgent {
		t.Errorf("archive step Lane = %q, want %q", archiveStep.Lane, LaneAgent)
	}
	if archiveStep.Action != "repo-archive" {
		t.Errorf("archive step Action = %q, want repo-archive", archiveStep.Action)
	}
	if archiveStep.Posts {
		t.Error("archive step Posts should be false")
	}

	closeStep := plan.Steps[1]
	if closeStep.Lane != LaneAgent {
		t.Errorf("pr close step Lane = %q, want %q", closeStep.Lane, LaneAgent)
	}
	if closeStep.Action != "pr-close" {
		t.Errorf("pr close step Action = %q, want pr-close", closeStep.Action)
	}
	if !closeStep.Posts {
		t.Error("pr close step Posts should be true (posts public text)")
	}
	wantCmd := "gh pr close 42 -R 'schuettc/myrepo' --comment 'no activity'"
	if closeStep.Command != wantCmd {
		t.Errorf("pr close Command =\n  %q\nwant\n  %q", closeStep.Command, wantCmd)
	}
}

func TestPlanRefusesStaleObservation(t *testing.T) {
	// builtAt 31 minutes ago, syncInterval 30m → ErrStale.
	builtAt := epoch.Add(-31 * time.Minute)

	_, err := Build(nil, "mymachine", epoch, builtAt, 30*time.Minute)
	if !errors.Is(err, ErrStale) {
		t.Fatalf("Build with stale observation: got %v, want ErrStale", err)
	}

	// Exactly at boundary: 30m → not stale.
	builtAt30 := epoch.Add(-30 * time.Minute)
	_, err = Build(nil, "mymachine", epoch, builtAt30, 30*time.Minute)
	if err != nil {
		t.Fatalf("Build at exactly syncInterval: got %v, want nil", err)
	}
}

func TestKeepProducesNoStep(t *testing.T) {
	builtAt := epoch.Add(-5 * time.Minute)

	// Keep/wait/watch/ignore produce no steps even if status is manually set to-apply.
	for _, disp := range []item.Disposition{item.Keep, item.Wait, item.Watch, item.Ignore} {
		k := item.RepoKey("schuettc/somerepo")
		it := engine.Item{
			Key:      k,
			ID:       k.String(),
			Kind:     item.KindRepo,
			Status:   item.StatusToApply, // manually forced; not reachable through engine normally
			Decision: decided(disp, ""),
		}

		plan, err := Build([]engine.Item{it}, "mymachine", epoch, builtAt, 30*time.Minute)
		if err != nil {
			t.Fatalf("Build(%s): %v", disp, err)
		}
		if len(plan.Steps) != 0 {
			t.Errorf("disposition %s: want 0 steps, got %d: %v", disp, len(plan.Steps), plan.Steps)
		}
	}
}

func TestLocalStepsOnlyForThisMachine(t *testing.T) {
	builtAt := epoch.Add(-5 * time.Minute)

	// Branch present on machineA and machineB.
	bk := item.BranchKey("schuettc/myrepo", "feat/shared")
	branch := engine.Item{
		Key:      bk,
		ID:       bk.String(),
		Kind:     item.KindBranch,
		Status:   item.StatusToApply,
		Decision: decided(item.Delete, ""),
		Landed:   "all-machines",
		Locations: []string{
			"machineA:/Users/A/repos/myrepo",
			"machineB:/Users/B/repos/myrepo",
		},
	}

	// Worktree on machineB only.
	wk := item.WorktreeKey("machineB", "/Users/B/worktrees/feat-shared")
	worktree := engine.Item{
		Key:       wk,
		ID:        wk.String(),
		Kind:      item.KindWorktree,
		Status:    item.StatusToApply,
		Decision:  decided(item.Delete, ""),
		Locations: []string{"machineB:/Users/B/repos/myrepo"},
	}

	plan, err := Build([]engine.Item{branch, worktree}, "machineA", epoch, builtAt, 30*time.Minute)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Expect: 1 local delete (machineA's clone) + 1 remote delete (from machineA).
	// No step for machineB's clone. No worktree step (machineB worktree).
	if len(plan.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d: %v", len(plan.Steps), plan.Steps)
	}

	local := plan.Steps[0]
	if local.Action != "branch-delete-local" {
		t.Errorf("Steps[0].Action = %q, want branch-delete-local", local.Action)
	}
	wantLocal := "git -C '/Users/A/repos/myrepo' branch -D 'feat/shared'"
	if local.Command != wantLocal {
		t.Errorf("Steps[0].Command =\n  %q\nwant\n  %q", local.Command, wantLocal)
	}

	remote := plan.Steps[1]
	if remote.Action != "branch-delete-remote" {
		t.Errorf("Steps[1].Action = %q, want branch-delete-remote", remote.Action)
	}
	wantRemote := "git -C '/Users/A/repos/myrepo' push 'origin' --delete 'feat/shared'"
	if remote.Command != wantRemote {
		t.Errorf("Steps[1].Command =\n  %q\nwant\n  %q", remote.Command, wantRemote)
	}

	// Verify no step uses machineB's clone path.
	for _, s := range plan.Steps {
		if contains(s.Command, "/Users/B/") {
			t.Errorf("step for machineB found in machineA plan: %v", s)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
