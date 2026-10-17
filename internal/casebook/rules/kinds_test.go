package rules

import (
	"slices"
	"testing"
	"time"

	"github.com/subaud-tools/tackle/internal/casebook/engine"
	"github.com/subaud-tools/tackle/internal/casebook/item"
	"github.com/subaud-tools/tackle/internal/casebook/observe"
)

var kindsNow = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func ago(d int) time.Time { return kindsNow.Add(-time.Duration(d) * 24 * time.Hour) }

// everyKind is a build with one item of each kind, every field it can have
// set: a repo that is an archived fork with open PRs and issues, a bot's
// labelled PR, an issue, a landed branch with unpushed commits in a dirty
// worktree, and a branch whose upstream was deleted.
func everyKind() engine.Result {
	g := observe.NewGitHub()
	g.User = "court"
	pr := observe.PRObs{Repo: "court/hail", Number: 1, Title: "deps", Author: "renovate", AuthorIsBot: true,
		State: "OPEN", CreatedAt: ago(9), UpdatedAt: ago(3), Labels: []string{"deps"}}
	issue := observe.PRObs{Repo: "court/hail", Number: 2, Title: "bug", Author: "bob", State: "OPEN",
		CreatedAt: ago(8), UpdatedAt: ago(2), Labels: []string{"bug"}}
	g.Owners["court"] = &observe.Owner{Login: "court", Reachable: true, FetchedAt: kindsNow, Repos: []observe.RepoObs{
		{Repo: "court/hail", Archived: true, Fork: true, Parent: "up/hail", PushedAt: ago(1), DefaultBranch: "main",
			PRs: []observe.PRObs{pr}, Issues: []observe.PRObs{issue}},
	}}
	snap := observe.Snapshot{Version: 1, Machine: "mbp", Clones: []observe.Clone{{
		Path: "/c/hail", Repo: "court/hail",
		Branches: []observe.Branch{
			{Name: "main", Tip: "a"},
			{Name: "feat/x", Tip: "b", TipAt: ago(4), Unpushed: 2, OldestUnpushed: ago(5),
				Landed: "in main", LandedState: "yes", LandedTip: "b", LandedHow: "default-branch"},
			{Name: "fix/y", Tip: "c", TipAt: ago(6), Upstream: "origin/fix/y", Gone: true, LandedState: "no"},
		},
		Worktrees: []observe.Worktree{{Path: "/w/x", Branch: "feat/x", Dirty: true}},
	}}}
	return engine.Build(engine.Input{Now: kindsNow, GitHub: g, Snapshots: []observe.Snapshot{snap},
		Decisions: map[string]item.Decision{}, Seen: map[string]time.Time{}})
}

// fieldIsZero reports whether field name has no value in f.
func fieldIsZero(t *testing.T, f engine.Fields, name string) bool {
	t.Helper()
	switch name {
	case "kind":
		return f.Kind == ""
	case "repo":
		return f.Repo == ""
	case "owner":
		return f.Owner == ""
	case "relation":
		return f.Relation == ""
	case "status":
		return f.Status == ""
	case "author":
		return f.Author == ""
	case "bot":
		return !f.Bot
	case "title":
		return f.Title == ""
	case "label":
		return len(f.Labels) == 0
	case "age":
		return f.Age == 0
	case "updated":
		return f.Updated == 0
	case "landed":
		return f.Landed == ""
	case "landed-how":
		return len(f.LandedHow) == 0
	case "gone-upstream":
		return !f.GoneUpstream
	case "unpushed":
		return !f.Unpushed
	case "worktree":
		return f.Worktree == ""
	case "archived":
		return !f.Archived
	case "fork":
		return !f.Fork
	case "open-prs":
		return f.OpenPRs == 0
	case "open-issues":
		return f.OpenIssues == 0
	}
	t.Fatalf("field %q has no zero check", name)
	return false
}

// TestFieldKindsMatchTheEngine: a field outside its Kinds has no value on
// that kind of item, so offering it for that kind could only match nothing
// (or everything, for is-not).
func TestFieldKindsMatchTheEngine(t *testing.T) {
	res := everyKind()
	seen := map[item.Kind]bool{}
	for _, it := range res.Items {
		seen[it.Kind] = true
		f := it.Fields(kindsNow)
		for _, fd := range Vocabulary() {
			if len(fd.Kinds) == 0 || slices.Contains(fd.Kinds, string(it.Kind)) {
				continue
			}
			if !fieldIsZero(t, f, fd.Name) {
				t.Errorf("%s: field %q has a value but its kinds are %v", it.ID, fd.Name, fd.Kinds)
			}
		}
	}
	for _, k := range allKinds {
		if !seen[k] {
			t.Errorf("the build has no %s", k)
		}
	}
}

// TestFieldKinds: each field names the item types it applies to.
func TestFieldKinds(t *testing.T) {
	want := map[string][]string{
		"kind": nil, "repo": nil, "owner": nil, "status": nil, "updated": nil,
		"relation": {"pr", "issue"}, "author": {"pr", "issue"}, "bot": {"pr", "issue"},
		"label": {"pr", "issue"}, "title": {"pr", "issue", "worktree"}, "age": {"pr", "issue"},
		"landed": {"branch"}, "landed-how": {"branch"}, "gone-upstream": {"branch"},
		"unpushed": {"branch", "worktree"}, "worktree": {"branch", "worktree"},
		"archived": {"repo"}, "fork": {"repo"}, "open-prs": {"repo"}, "open-issues": {"repo"},
	}
	for _, f := range Vocabulary() {
		if w, ok := want[f.Name]; !ok || !slices.Equal(f.Kinds, w) {
			t.Errorf("%s: kinds %v, want %v", f.Name, f.Kinds, w)
		}
	}
}

// TestOneConditionPerField: a field may appear once in a filter or rule.
func TestOneConditionPerField(t *testing.T) {
	err := ValidateConditions([]Condition{
		{Field: "kind", Op: "is", Value: "issue"},
		{Field: "kind", Op: "is", Value: "pr"},
	})
	if err == nil || err.Error() != "condition 2: type is already used by condition 1" {
		t.Errorf("two kind conditions: %v", err)
	}
	err = ValidateConditions([]Condition{
		{Field: "age", Op: "older-than", Value: "7d"},
		{Field: "status", Op: "is", Value: "new"},
		{Field: "age", Op: "newer-than", Value: "30d"},
	})
	if err == nil || err.Error() != "condition 3: opened is already used by condition 1" {
		t.Errorf("an age range: %v", err)
	}
}

// TestFieldMustApplyToType: a condition on a field the type condition's
// types never have can never hold with it.
func TestFieldMustApplyToType(t *testing.T) {
	for _, c := range []struct {
		match []Condition
		want  string
	}{
		{[]Condition{{Field: "kind", Op: "is", Value: "issue"}, {Field: "unpushed", Op: "is", Value: "true"}},
			"condition 2: unpushed commits doesn't apply to issues"},
		{[]Condition{{Field: "open-prs", Op: "gt", Value: "0"}, {Field: "kind", Op: "in", Value: "issue,pr"}},
			"condition 1: open PRs doesn't apply to pull requests or issues"},
		{[]Condition{{Field: "kind", Op: "is-not", Value: "repo"}, {Field: "archived", Op: "is", Value: "true"}},
			"condition 2: archived doesn't apply to pull requests, issues, branches or worktrees"},
	} {
		if err := ValidateConditions(c.match); err == nil || err.Error() != c.want {
			t.Errorf("%v: %v, want %q", c.match, err, c.want)
		}
	}
	for _, ok := range [][]Condition{
		{{Field: "kind", Op: "in", Value: "issue,branch"}, {Field: "unpushed", Op: "is", Value: "true"}},
		{{Field: "kind", Op: "is", Value: "worktree"}, {Field: "worktree", Op: "is", Value: "dirty"}},
		{{Field: "unpushed", Op: "is", Value: "true"}},
	} {
		if err := ValidateConditions(ok); err != nil {
			t.Errorf("%v: %v", ok, err)
		}
	}
}

// TestInWithOneValue: "in" with a single value is "is".
func TestInWithOneValue(t *testing.T) {
	res := everyKind()
	ms, err := Rule{Match: []Condition{{Field: "kind", Op: "in", Value: "issue"}}}.MatchAll(res, kindsNow)
	if err != nil || len(ms) != 1 || ms[0].Key != "issue:court/hail#2" {
		t.Errorf("kind in issue: %v %v", ms, err)
	}
}
