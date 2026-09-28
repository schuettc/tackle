package rules

import (
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/item"
)

func TestNoteTemplateExpandsSpecPlaceholders(t *testing.T) {
	// Spec §4.1 example template.
	note := "landed ({how}); restore tip {tip}"

	f := engine.Fields{
		Kind:  "branch",
		Repo:  "schuettc/galley",
		Title: "feat/headcount-licensing",
		Age:   14 * 24 * time.Hour,
	}
	m := Match{
		Key:    "branch:schuettc/galley@feat/headcount-licensing",
		Reason: "in main",
	}
	// Also need to set Tip in Fields for {tip} expansion.
	f.Tip = "abc1234"

	got := Render(note, f, m)
	want := "landed (in main); restore tip abc1234"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestNoteTemplateExpandsAllPlaceholders(t *testing.T) {
	note := "{how} {tip} {age} {repo} {title}"
	f := engine.Fields{
		Kind:  "branch",
		Repo:  "schuettc/galley",
		Title: "feat/foo",
		Age:   7 * 24 * time.Hour,
		Tip:   "deadbeef",
	}
	m := Match{Key: "branch:schuettc/galley@feat/foo", Reason: "via merged PR"}

	got := Render(note, f, m)
	// age 7d = 1w in casebook's duration format
	want := "via merged PR deadbeef 1w schuettc/galley feat/foo"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestNoteTemplateUnknownPlaceholderIsLeftLiteral(t *testing.T) {
	note := "see {unknown} and {repo}"
	f := engine.Fields{Repo: "schuettc/galley"}
	m := Match{Reason: ""}

	got := Render(note, f, m)
	want := "see {unknown} and schuettc/galley"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestFieldsMethodDerivesBotFromAuthor(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	// Fix A: Bot is derived from AuthorIsBot (set by GraphQL __typename == "Bot"),
	// NOT from the [bot] login suffix. "dependabot" (no suffix) with AuthorIsBot=true
	// must give Bot=true. "dependabot[bot]" with AuthorIsBot=false must give Bot=false.
	botItem := engine.Item{
		ID:          "pr:schuettc/galley#1",
		Kind:        item.KindPR,
		Repo:        "schuettc/galley",
		Author:      "dependabot", // no [bot] suffix — GitHub GraphQL Bot type
		AuthorIsBot: true,         // set from __typename == "Bot"
		Status:      item.StatusNew,
	}
	f := botItem.Fields(now)
	if !f.Bot {
		t.Errorf("Bot = false, want true for author %q with AuthorIsBot=true", botItem.Author)
	}

	// Human author: AuthorIsBot=false (the default) → Bot=false.
	humanItem := engine.Item{
		ID:     "pr:schuettc/galley#2",
		Kind:   item.KindPR,
		Repo:   "schuettc/galley",
		Author: "court",
		Status: item.StatusNew,
	}
	fh := humanItem.Fields(now)
	if fh.Bot {
		t.Errorf("Bot = true, want false for author %q with AuthorIsBot=false", humanItem.Author)
	}

	// Suffix-only bot (dependabot[bot]) WITHOUT AuthorIsBot=true → must give Bot=false
	// (we no longer rely on the suffix).
	suffixOnlyItem := engine.Item{
		ID:     "pr:schuettc/galley#3",
		Kind:   item.KindPR,
		Repo:   "schuettc/galley",
		Author: "dependabot[bot]", // suffix present but AuthorIsBot not set
		Status: item.StatusNew,
	}
	fs := suffixOnlyItem.Fields(now)
	if fs.Bot {
		t.Errorf("Bot = true, want false: suffix [bot] without AuthorIsBot flag should NOT give Bot=true")
	}
}

func TestFieldsMethodDerivesFork(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	it := engine.Item{
		ID:       "repo:schuettc/pi-extensions",
		Kind:     item.KindRepo,
		Repo:     "schuettc/pi-extensions",
		Relation: "fork-of:schuettc/galley",
	}
	f := it.Fields(now)
	if !f.Fork {
		t.Errorf("Fork = false, want true for relation %q", it.Relation)
	}
}

// TestLandedHowFieldsAreDistinct verifies fix B: engine.Item.LandedHow holds
// the human-readable text ("in main", "merged #7") while engine.Item.LandedVia
// holds the machine codes ("default-branch", "merged-pr") and Fields.LandedHow
// is the []string of codes.
func TestLandedHowFieldsAreDistinct(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	it := engine.Item{
		ID:        "branch:schuettc/hail@feat",
		Kind:      item.KindBranch,
		Repo:      "schuettc/hail",
		LandedHow: "in main",                  // human text on Item
		LandedVia: []string{"default-branch"}, // codes on Item
	}
	f := it.Fields(now)
	// Fields.LandedHow must be the codes slice, not the human text.
	if len(f.LandedHow) != 1 || f.LandedHow[0] != "default-branch" {
		t.Errorf("Fields.LandedHow = %v, want [\"default-branch\"]", f.LandedHow)
	}
}

// TestLandedHowMultiValuedCondition verifies fix C: the landed-how condition
// is multi-valued \ u2014 "is" means "contains" when an item has two how codes.
func TestLandedHowMultiValuedCondition(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	// An item that landed via both routes on different machines.
	it := engine.Item{
		ID:        "branch:schuettc/hail@feat",
		Kind:      item.KindBranch,
		Repo:      "schuettc/hail",
		Landed:    "all-machines",
		LandedVia: []string{"default-branch", "merged-pr"},
	}
	f := it.Fields(now)

	// "landed-how is default-branch" must be true even though merged-pr also present.
	cDefault := Condition{Field: "landed-how", Op: "is", Value: "default-branch"}
	ok, err := cDefault.Eval(f)
	if err != nil {
		t.Fatalf("Eval(is default-branch): %v", err)
	}
	if !ok {
		t.Error("landed-how is default-branch: want true for item with both routes")
	}

	// "landed-how is merged-pr" must also be true.
	cPR := Condition{Field: "landed-how", Op: "is", Value: "merged-pr"}
	ok, err = cPR.Eval(f)
	if err != nil {
		t.Fatalf("Eval(is merged-pr): %v", err)
	}
	if !ok {
		t.Error("landed-how is merged-pr: want true for item with both routes")
	}
}
