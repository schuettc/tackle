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
	botItem := engine.Item{
		ID:     "pr:schuettc/galley#1",
		Kind:   item.KindPR,
		Repo:   "schuettc/galley",
		Author: "dependabot[bot]",
		Status: item.StatusNew,
	}
	f := botItem.Fields(now)
	if !f.Bot {
		t.Errorf("Bot = false, want true for author %q", botItem.Author)
	}

	humanItem := engine.Item{
		ID:     "pr:schuettc/galley#2",
		Kind:   item.KindPR,
		Repo:   "schuettc/galley",
		Author: "court",
		Status: item.StatusNew,
	}
	fh := humanItem.Fields(now)
	if fh.Bot {
		t.Errorf("Bot = true, want false for author %q", humanItem.Author)
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
