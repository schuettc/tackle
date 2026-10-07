package apply

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/observe"
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

	snap := snapWith("mymachine", []observe.Clone{{
		Path:     "/Users/me/repos/myrepo",
		Repo:     "schuettc/myrepo",
		Remotes:  map[string]string{"origin": "schuettc/myrepo"},
		Branches: []observe.Branch{{Name: "feat/thing", Upstream: "origin/x", Tip: "aaa", RemoteTip: "aaa"}},
	}})
	plan, err := Build([]engine.Item{br, rp}, snap, epoch, builtAt, 30*time.Minute)
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

	snap := snapWith("mymachine", []observe.Clone{{
		Path:     "/Users/me/repos/myrepo",
		Repo:     "schuettc/myrepo",
		Remotes:  map[string]string{"origin": "schuettc/myrepo"},
		Branches: []observe.Branch{{Name: "feat/my-branch", Upstream: "origin/x", Tip: "tip123", RemoteTip: "tip123"}},
	}})
	plan, err := Build([]engine.Item{it}, snap, epoch, builtAt, 30*time.Minute)
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
	wantLocal := "git -C '/Users/me/repos/myrepo' update-ref -d 'refs/heads/feat/my-branch' tip123"
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
	wantRemote := "git -C '/Users/me/repos/myrepo' push '--force-with-lease=refs/heads/feat/my-branch:tip123' 'origin' ':refs/heads/feat/my-branch'"
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

	plan, err := Build([]engine.Item{archive, prClose}, snapWith("mymachine", nil), epoch, builtAt, 30*time.Minute)
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

	emptySnap := snapWith("mymachine", nil)
	_, err := Build(nil, emptySnap, epoch, builtAt, 30*time.Minute)
	if !errors.Is(err, ErrStale) {
		t.Fatalf("Build with stale observation: got %v, want ErrStale", err)
	}

	// Exactly at boundary: 30m → not stale.
	builtAt30 := epoch.Add(-30 * time.Minute)
	_, err = Build(nil, emptySnap, epoch, builtAt30, 30*time.Minute)
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

		plan, err := Build([]engine.Item{it}, snapWith("mymachine", nil), epoch, builtAt, 30*time.Minute)
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

	snapA := snapWith("machineA", []observe.Clone{{
		Path:     "/Users/A/repos/myrepo",
		Repo:     "schuettc/myrepo",
		Remotes:  map[string]string{"origin": "schuettc/myrepo"},
		Branches: []observe.Branch{{Name: "feat/shared", Upstream: "origin/x", Tip: "tipA", RemoteTip: "tipA"}},
	}})
	plan, err := Build([]engine.Item{branch, worktree}, snapA, epoch, builtAt, 30*time.Minute)
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
	wantLocal := "git -C '/Users/A/repos/myrepo' update-ref -d 'refs/heads/feat/shared' tipA"
	if local.Command != wantLocal {
		t.Errorf("Steps[0].Command =\n  %q\nwant\n  %q", local.Command, wantLocal)
	}

	remote := plan.Steps[1]
	if remote.Action != "branch-delete-remote" {
		t.Errorf("Steps[1].Action = %q, want branch-delete-remote", remote.Action)
	}
	wantRemote := "git -C '/Users/A/repos/myrepo' push '--force-with-lease=refs/heads/feat/shared:tipA' 'origin' ':refs/heads/feat/shared'"
	if remote.Command != wantRemote {
		t.Errorf("Steps[1].Command =\n  %q\nwant\n  %q", remote.Command, wantRemote)
	}

	// Verify no step uses machineB's clone path.
	for _, s := range plan.Steps {
		if strings.Contains(s.Command, "/Users/B/") {
			t.Errorf("step for machineB found in machineA plan: %v", s)
		}
	}
}

// --- Fix round 1 tests ---

// snapWith builds a minimal observe.Snapshot for tests.
func snapWith(machine string, clones []observe.Clone) observe.Snapshot {
	return observe.Snapshot{
		Version: 1,
		Machine: machine,
		Clones:  clones,
	}
}

// TestRemoteNameFromSnapshotUpstream checks fix 1: when the clone's Remotes
// map names the GitHub remote "upstream" (not "origin"), the push command
// uses "upstream".
func TestRemoteNameFromSnapshotUpstream(t *testing.T) {
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

	snap := snapWith("mymachine", []observe.Clone{{
		Path: "/Users/me/repos/myrepo",
		Repo: "schuettc/myrepo",
		Remotes: map[string]string{
			"upstream": "schuettc/myrepo",
		},
		Branches: []observe.Branch{{
			Name:      "feat/my-branch",
			Upstream:  "upstream/feat/my-branch",
			Tip:       "abc1234deadbeef",
			RemoteTip: "abc1234deadbeef",
		}},
	}})

	plan, err := Build([]engine.Item{it}, snap, epoch, builtAt, 30*time.Minute)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("want 2 steps (local+remote), got %d: %v", len(plan.Steps), plan.Steps)
	}

	remote := plan.Steps[1]
	if remote.Action != "branch-delete-remote" {
		t.Fatalf("Steps[1].Action = %q, want branch-delete-remote", remote.Action)
	}
	wantCmd := "git -C '/Users/me/repos/myrepo' push '--force-with-lease=refs/heads/feat/my-branch:abc1234deadbeef' 'upstream' ':refs/heads/feat/my-branch'"
	if remote.Command != wantCmd {
		t.Errorf("remote Command =\n  %q\nwant\n  %q", remote.Command, wantCmd)
	}
}

// TestNoRemoteStepWhenNoMatchingRemote checks fix 1: if no clone has a remote
// whose value equals the item's repo, no remote-delete step is planned.
func TestNoRemoteStepWhenNoMatchingRemote(t *testing.T) {
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

	// Clone has no remote matching the item's repo.
	snap := snapWith("mymachine", []observe.Clone{{
		Path: "/Users/me/repos/myrepo",
		Repo: "schuettc/myrepo",
		Remotes: map[string]string{
			"origin": "schuettc/DIFFERENT",
		},
		Branches: []observe.Branch{{
			Name: "feat/my-branch",
			Tip:  "abc1234",
		}},
	}})

	plan, err := Build([]engine.Item{it}, snap, epoch, builtAt, 30*time.Minute)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Only 1 local-delete step; no remote step.
	if len(plan.Steps) != 1 {
		t.Fatalf("want 1 step (local only), got %d: %v", len(plan.Steps), plan.Steps)
	}
	if plan.Steps[0].Action != "branch-delete-local" {
		t.Errorf("Steps[0].Action = %q, want branch-delete-local", plan.Steps[0].Action)
	}
}

// TestBranchStepsHaveExpectedTips checks fix 2: local-delete steps carry the
// snapshot branch tip; remote-delete step carries that same tip.
func TestBranchStepsHaveExpectedTips(t *testing.T) {
	builtAt := epoch.Add(-5 * time.Minute)

	k := item.BranchKey("schuettc/myrepo", "feat/tips")
	it := engine.Item{
		Key:       k,
		ID:        k.String(),
		Kind:      item.KindBranch,
		Status:    item.StatusToApply,
		Decision:  decided(item.Delete, ""),
		Landed:    "all-machines",
		Locations: []string{"mymachine:/Users/me/repos/myrepo"},
	}

	const wantTip = "cafebabe00000000"
	snap := snapWith("mymachine", []observe.Clone{{
		Path: "/Users/me/repos/myrepo",
		Repo: "schuettc/myrepo",
		Remotes: map[string]string{
			"origin": "schuettc/myrepo",
		},
		Branches: []observe.Branch{{
			Name:      "feat/tips",
			Upstream:  "origin/feat/tips",
			Tip:       wantTip,
			RemoteTip: wantTip,
		}},
	}})

	plan, err := Build([]engine.Item{it}, snap, epoch, builtAt, 30*time.Minute)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d: %v", len(plan.Steps), plan.Steps)
	}

	if got := plan.Steps[0].ExpectedTip; got != wantTip {
		t.Errorf("local step ExpectedTip = %q, want %q", got, wantTip)
	}
	if got := plan.Steps[1].ExpectedTip; got != wantTip {
		t.Errorf("remote step ExpectedTip = %q, want %q", got, wantTip)
	}
}

// TestWorktreeStepUsesSnapshotClone checks fix 3: the worktree remove command
// uses the clone path from the snapshot (matched by the worktree path), not
// the item's Locations entry which may be for a different machine.
func TestWorktreeStepUsesSnapshotClone(t *testing.T) {
	builtAt := epoch.Add(-5 * time.Minute)

	wk := item.WorktreeKey("mymachine", "/Users/me/worktrees/feat")
	it := engine.Item{
		Key:      wk,
		ID:       wk.String(),
		Kind:     item.KindWorktree,
		Status:   item.StatusToApply,
		Decision: decided(item.Delete, ""),
		// Locations comes from the engine; the clone is "/Users/me/repos/myrepo".
		Locations: []string{"mymachine:/Users/me/repos/myrepo"},
	}

	snap := snapWith("mymachine", []observe.Clone{{
		Path: "/Users/me/repos/myrepo",
		Repo: "schuettc/myrepo",
		Worktrees: []observe.Worktree{{
			Path:   "/Users/me/worktrees/feat",
			Branch: "feat/my-branch",
		}},
	}})

	plan, err := Build([]engine.Item{it}, snap, epoch, builtAt, 30*time.Minute)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("want 1 step, got %d: %v", len(plan.Steps), plan.Steps)
	}

	step := plan.Steps[0]
	wantCmd := "git -C '/Users/me/repos/myrepo' worktree remove '/Users/me/worktrees/feat'"
	if step.Command != wantCmd {
		t.Errorf("worktree step Command =\n  %q\nwant\n  %q", step.Command, wantCmd)
	}
}

func TestNoRemoteStepForABranchWithNoRemoteTip(t *testing.T) {
	now := time.Now()
	// No upstream: the snapshot records no RemoteTip, so no remote step.
	it, snap := remoteFixture(t, "never-pushed", "", false)
	p, err := Build([]engine.Item{it}, snap, now, now, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range p.Steps {
		if s.Action == "branch-delete-remote" {
			t.Fatalf("remote step planned for a branch with no remote tip: %+v", s)
		}
	}
}

// TestRemoteStepUsesRemoteTip: the remote step is planned only when the
// snapshot recorded RemoteTip, and its ExpectedTip is exactly that RemoteTip
// (replacing the earlier "tip only when nothing is unpushed" rule).
func TestRemoteStepUsesRemoteTip(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name     string
		upstream string
		gone     bool
		want     string // "" means no remote step
	}{
		{"has upstream", "origin/feat", false, "abc123"},
		{"upstream gone", "origin/feat", true, ""},
		{"no upstream", "", false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			it, snap := remoteFixture(t, "feat", c.upstream, c.gone)
			p, err := Build([]engine.Item{it}, snap, now, now, 30*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			var got *Step
			for i := range p.Steps {
				if p.Steps[i].Action == "branch-delete-remote" {
					got = &p.Steps[i]
				}
			}
			if c.want == "" {
				if got != nil {
					t.Fatalf("unexpected remote step %+v", got)
				}
				return
			}
			if got == nil || got.ExpectedTip != c.want {
				t.Fatalf("remote step %+v, want ExpectedTip %q", got, c.want)
			}
		})
	}
}

// remoteFixture is a landed branch in one clone on machine "m" with the given
// upstream and gone flag; its tip is "abc123". RemoteTip mirrors what observe
// records: the tip when an upstream exists and is not gone, else empty.
func remoteFixture(t *testing.T, branch, upstream string, gone bool) (engine.Item, observe.Snapshot) {
	t.Helper()
	it := branchItem("schuettc/repo", branch, "m", "/src/repo")
	rt := ""
	if upstream != "" && !gone {
		rt = "abc123"
	}
	snap := snapWith("m", []observe.Clone{{
		Path:     "/src/repo",
		Repo:     "schuettc/repo",
		Remotes:  map[string]string{"origin": "schuettc/repo"},
		Branches: []observe.Branch{{Name: branch, Tip: "abc123", Upstream: upstream, Gone: gone, RemoteTip: rt}},
	}})
	return it, snap
}

// TestCloseCommentIsTheDecisionsNote: a close posts the decision's note as
// its closing comment, and nothing else. A note left empty (or blank) means
// no comment: the command has no --comment and the step posts nothing (no
// "Closing." stand-in).
func TestCloseCommentIsTheDecisionsNote(t *testing.T) {
	builtAt := epoch.Add(-5 * time.Minute)
	closing := func(k item.Key, note string) engine.Item {
		return engine.Item{
			Key:      k,
			ID:       k.String(),
			Kind:     k.Kind,
			Status:   item.StatusToApply,
			Decision: decided(item.Close, note),
		}
	}
	for _, tc := range []struct {
		name, note, wantCmd string
		posts               bool
	}{
		{"pr with a comment", "Thanks! Superseded by #9.", "gh pr close 42 -R 'schuettc/myrepo' --comment 'Thanks! Superseded by #9.'", true},
		{"pr without comment", "", "gh pr close 42 -R 'schuettc/myrepo'", false},
		{"pr with a blank note", "  \n", "gh pr close 42 -R 'schuettc/myrepo'", false},
		{"issue with a comment", "Fixed in v2; it's in the release notes.", `gh issue close 13 -R 'schuettc/myrepo' --comment 'Fixed in v2; it'"'"'s in the release notes.'`, true},
		{"issue without comment", "", "gh issue close 13 -R 'schuettc/myrepo'", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := item.PRKey("schuettc/myrepo", 42)
			if strings.HasPrefix(tc.name, "issue") {
				k = item.IssueKey("schuettc/myrepo", 13)
			}
			plan, err := Build([]engine.Item{closing(k, tc.note)}, snapWith("mymachine", nil), epoch, builtAt, 30*time.Minute)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if len(plan.Steps) != 1 {
				t.Fatalf("want 1 step, got %d: %v", len(plan.Steps), plan.Steps)
			}
			st := plan.Steps[0]
			if st.Command != tc.wantCmd {
				t.Errorf("Command =\n  %q\nwant\n  %q", st.Command, tc.wantCmd)
			}
			if st.Posts != tc.posts {
				t.Errorf("Posts = %v, want %v", st.Posts, tc.posts)
			}
			if strings.Contains(st.Command, "Closing.") {
				t.Errorf("Command %q posts the old stand-in comment", st.Command)
			}
		})
	}
}
