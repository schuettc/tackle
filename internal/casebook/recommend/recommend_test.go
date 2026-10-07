package recommend

import (
	"regexp"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/casebook/item"
)

// vocab is the decision vocabulary as serve hands it over: every kind's
// choices from the item package's wording table.
func vocab() []KindChoices {
	var out []KindChoices
	for _, k := range []item.Kind{item.KindRepo, item.KindPR, item.KindIssue, item.KindBranch, item.KindWorktree} {
		kc := KindChoices{Kind: string(k)}
		for _, c := range item.Choices(k) {
			kc.Choices = append(kc.Choices, Choice{Disposition: string(c.Disposition), Label: c.Label, Outward: c.Outward, NeedsUntil: c.NeedsUntil})
		}
		out = append(out, kc)
	}
	return out
}

// TestGuideIsShortAndGeneric: the guide is short, names no one, says what
// matters, and renders every choice from the vocabulary.
func TestGuideIsShortAndGeneric(t *testing.T) {
	v := vocab()
	g := Guide(v)
	if len(g) >= 2500 {
		t.Errorf("guide is %d bytes, want under 2,500:\n%s", len(g), g)
	}
	if !strings.HasPrefix(g, Fixed) {
		t.Error("the guide doesn't start with the fixed text")
	}
	re := regexp.MustCompile(`(?i)\b(court|schuettc|claude|codex|openai|anthropic|gpt|gemini|opus|sonnet|pi)\b`)
	if m := re.FindString(g); m != "" {
		t.Errorf("the guide names %q", m)
	}
	if strings.Contains(g, "\u2014") {
		t.Error("the guide has an em dash")
	}
	for _, want := range []string{"evidence", "reason", "Not now", "To apply"} {
		if !strings.Contains(g, want) {
			t.Errorf("the guide doesn't mention %q", want)
		}
	}
	for _, kc := range v {
		for _, c := range kc.Choices {
			if !strings.Contains(g, c.Label) {
				t.Errorf("the guide misses %s's choice %q", kc.Kind, c.Label)
			}
		}
	}
	for _, s := range []string{ProposeHow} {
		if m := re.FindString(s); m != "" {
			t.Errorf("ProposeHow names %q", m)
		}
	}
}
