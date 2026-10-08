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
	if !strings.HasPrefix(g, Fixed(v)) {
		t.Error("the guide doesn't start with the fixed text")
	}
	re := regexp.MustCompile(`(?i)\b(court|schuettc|claude|codex|openai|anthropic|gpt|gemini|opus|sonnet|pi)\b`)
	if m := re.FindString(g); m != "" {
		t.Errorf("the guide names %q", m)
	}
	if strings.Contains(g, "\u2014") {
		t.Error("the guide has an em dash")
	}
	for _, want := range []string{"evidence", "reason", "Not now", "To apply", "- In a Not now condition, name only a PR, issue or repo you have seen in this item's evidence or history, or found with gh. Never guess a number.", "- When asked to look into an item, read its CI and recent activity with gh, record what you find with casebook_evidence, then recommend.", "- If an item is blocked by something you can check, such as a failing CI check, look into it with gh before recommending, and record what you find with casebook_evidence. Don't recommend leaving it open just because someone needs to look.", "- A proposal marked withdrawn was cleared to be made again: recommend afresh, don't repeat it."} {
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
	for _, s := range []string{ProposeHow(v)} {
		if m := re.FindString(s); m != "" {
			t.Errorf("ProposeHow names %q", m)
		}
	}
}

// TestGuideReadsItsLabelsFromTheVocabulary: the guide's prose names the
// choices by the vocabulary's labels, so renaming one renames it there.
func TestGuideReadsItsLabelsFromTheVocabulary(t *testing.T) {
	v := vocab()
	for i := range v {
		for j, c := range v[i].Choices {
			switch c.Disposition {
			case "wait":
				v[i].Choices[j].Label = "Later"
			case "ignore":
				v[i].Choices[j].Label = "Forget it"
			}
		}
	}
	g, how := Guide(v), ProposeHow(v)
	for _, old := range []string{"Not now", "Stop tracking"} {
		if strings.Contains(g, old) || strings.Contains(how, old) {
			t.Errorf("the guide still says %q after the label changed:\n%s\n%s", old, g, how)
		}
	}
	for _, want := range []string{"- Use Later when the item depends", "- In a Later condition, name only", "- Forget it only when the user will never act on it"} {
		if !strings.Contains(g, want) {
			t.Errorf("the guide doesn't say %q:\n%s", want, g)
		}
	}
	if !strings.Contains(how, "For wait (Later)") {
		t.Errorf("ProposeHow doesn't name the label: %s", how)
	}
}
