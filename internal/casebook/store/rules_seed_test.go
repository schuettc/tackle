package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/subaud-tools/tackle/internal/casebook/item"
	"github.com/subaud-tools/tackle/internal/casebook/rules"
	"github.com/subaud-tools/tackle/internal/casebook/testgit"
)

// TestInitSeedsExampleRules: a new casebook-data has the starter rules, on.
func TestInitSeedsExampleRules(t *testing.T) {
	r, _ := newStore(t)
	got, errs := r.Rules()
	if len(errs) != 0 || len(got) != len(rules.Examples()) {
		t.Fatalf("rules %d %v, want the %d examples", len(got), errs, len(rules.Examples()))
	}
	for _, ru := range got {
		if ru.Status != rules.StatusActive {
			t.Errorf("%s is %q, want active", ru.ID, ru.Status)
		}
	}
}

// TestTidySeedsExampleRulesOnce: an older repo gets the examples with its
// next write, in their own commit, without touching a rule it already has
// under an example's id; a deleted example never comes back.
func TestTidySeedsExampleRulesOnce(t *testing.T) {
	r, _ := newStore(t)
	testgit.Git(t, r.Dir, "rm", "-r", "-q", "rules")
	mine := rules.Rule{ID: "landed-branches", Name: "Mine", Status: rules.StatusDraft, Match: []rules.Condition{{Field: "kind", Op: "is", Value: "branch"}}, Propose: rules.RuleAction{Disposition: "keep"}}
	b, err := rules.Encode(mine)
	if err != nil {
		t.Fatal(err)
	}
	testgit.Commit(t, r.Dir, "rules/landed-branches.toml", string(b))
	if err := r.Decide(ctx, item.IssueKey("a/b", 1), dec(item.Keep, "court", time.Now())); err != nil {
		t.Fatal(err)
	}
	subj := strings.Split(testgit.Git(t, r.Dir, "log", "-2", "--format=%s"), "\n")
	if len(subj) != 2 || subj[1] != "add example rules" {
		t.Fatalf("log %q", subj)
	}
	kept, _ := r.ReadRule("landed-branches")
	if kept == nil || kept.Name != "Mine" {
		t.Fatalf("an existing rule under an example's id was replaced: %+v", kept)
	}
	if err := os.Remove(filepath.Join(r.Dir, "rules", "quiet-bot-prs.toml")); err != nil {
		t.Fatal(err)
	}
	testgit.Git(t, r.Dir, "commit", "-q", "-am", "delete quiet-bot-prs")
	if err := r.Decide(ctx, item.IssueKey("a/b", 2), dec(item.Keep, "court", time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.Dir, "rules", "quiet-bot-prs.toml")); !os.IsNotExist(err) {
		t.Fatalf("a deleted example came back: %v", err)
	}
}
