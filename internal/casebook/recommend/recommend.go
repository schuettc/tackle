// Package recommend is the agent's guide to recommending a decision: the
// fixed text casebook_next hands out with every item (naming choices by the
// vocabulary's labels), the choices rendered from the decision vocabulary,
// and how to answer with casebook_propose.
// It is product text: it names no person, provider or model.
//
// It takes the vocabulary as plain values rather than serve's wire types so
// that serve, which builds the vocabulary, can import it.
package recommend

import (
	"fmt"
	"strings"
)

// Fixed is the guide's fixed text, naming choices by the vocabulary's
// labels; Guide appends the choices to it.
func Fixed(kinds []KindChoices) string {
	return fmt.Sprintf(fixed, label(kinds, "wait"), label(kinds, "ignore"))
}

// fixed is Fixed's text: %[1]s is wait's label (Not now), %[2]s ignore's.
const fixed = `Recommend one choice for this item, with a one-line reason the user can check.

- Read the evidence and history first. Recommend from what they show, not from the title alone.
- When asked to look into an item, read its CI and recent activity with gh, record what you find with casebook_evidence, then recommend.
- Leave it (keep) when it still needs a person: a human reviewer is waiting, the work is active, or you can't tell.
- A bot's PR (dependency or release automation) that is superseded by a newer one, or failing with no recent activity, can close.
- A branch whose commits are all on the default branch or a merged PR, with nothing unpushed, can be deleted. A branch with unpushed or unmerged commits is kept.
- A worktree with no uncommitted changes whose branch has landed can be removed.
- Use %[1]s when the item depends on something that hasn't happened yet, and pick the condition that names it (a PR merging, an issue closing, a date). Don't use it to postpone a choice you could make now.
- In a %[1]s condition, name only a PR, issue or repo you have seen in this item's evidence or history, or found with gh. Never guess a number.
- %[2]s only when the user will never act on it (someone else's repo, an archived experiment).
- Recommend one choice. Outward choices (close, merge, archive, delete) only go to To apply; the user approves them again there.`

// ProposeHow is how to answer, for casebook_next's output.
func ProposeHow(kinds []KindChoices) string {
	return fmt.Sprintf(proposeHow, label(kinds, "wait"))
}

// proposeHow is ProposeHow's text: %[1]s is wait's label.
const proposeHow = `casebook_propose with {"keys": [the item's key], "disposition": one of choices' dispositions, "note": "a one-line reason", "from_next": true}. For wait (%[1]s) add "until": a not_now template with its hole filled, for example merged(pr:owner/repo#12) or date(2026-12-01). Then call casebook_next again.`

// Choice is one answer a kind offers, as the vocabulary lists it.
type Choice struct {
	Disposition string
	Label       string
	Outward     bool
	NeedsUntil  bool
}

// KindChoices is one kind's choices, in the vocabulary's order.
type KindChoices struct {
	Kind    string
	Choices []Choice
}

// label is disposition's label in the vocabulary (the first kind that
// offers it), or the disposition itself when no kind does.
func label(kinds []KindChoices, disposition string) string {
	for _, k := range kinds {
		for _, c := range k.Choices {
			if c.Disposition == disposition {
				return c.Label
			}
		}
	}
	return disposition
}

// Guide is the fixed text followed by every kind's choices, each as its
// disposition and label, marking the outward ones and the one that needs a
// condition.
func Guide(kinds []KindChoices) string {
	var b strings.Builder
	b.WriteString(Fixed(kinds))
	b.WriteString("\n\nThe choices by kind (disposition: label):\n")
	for _, k := range kinds {
		parts := make([]string, 0, len(k.Choices))
		for _, c := range k.Choices {
			p := fmt.Sprintf("%s: %s", c.Disposition, c.Label)
			switch {
			case c.Outward:
				p += " (To apply)"
			case c.NeedsUntil:
				p += " (needs until)"
			}
			parts = append(parts, p)
		}
		fmt.Fprintf(&b, "- %s: %s\n", k.Kind, strings.Join(parts, "; "))
	}
	return b.String()
}
