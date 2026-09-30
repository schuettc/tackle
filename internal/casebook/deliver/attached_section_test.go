package deliver

import (
	"strings"
	"testing"
	"time"
)

// Court asks from a section with nothing open in it ("or ask pi to draft
// one" on Rules): the message carries the section, and the agent reads
// "attached: section rules" — no rule, no keys, but where he was, in the
// same words as the page's attached line and the other kinds ("rule <id>",
// "job <n>").
func TestAttachedSectionReachesTheAgent(t *testing.T) {
	q, _ := newQueue(t)
	th := thread(t, q, "s1")
	a := Attached{Section: "rules"}
	if a.Empty() {
		t.Fatal("an attached section counts as nothing attached")
	}
	if _, err := q.Post(ctx, th.ID, "draft a rule for dependabot PRs", a, false); err != nil {
		t.Fatal(err)
	}
	d, err := q.Next(ctx, "s1")
	if err != nil || d == nil {
		t.Fatalf("Next: %v %v", d, err)
	}
	if got := d.Messages[0].Attached.Section; got != "rules" {
		t.Fatalf("the stored message lost its section: %q", got)
	}
	if got := Render(*d, "", "", time.UTC); !strings.Contains(got, "attached: section rules\n") {
		t.Fatalf("delivery text does not say \"attached: section rules\":\n%s", got)
	}
}
