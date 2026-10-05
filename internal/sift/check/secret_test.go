package check

import (
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/row"
)

// The hit fixture's tokens are assembled here, so no token-shaped string is
// committed (push protection would rightly refuse it).
func TestSecret(t *testing.T) {
	hit := fixture(t, "secret", "hit.md", discover.ClassRepo)
	hit.Content = strings.NewReplacer(
		"{{GITHUB_TOKEN}}", "ghp_"+strings.Repeat("aB3", 12),
		"{{AWS_KEY}}", "AKIA"+strings.Repeat("Q7", 8),
	).Replace(hit.Content)
	near := fixture(t, "secret", "near.md", discover.ClassRepo)
	rows := only(t, "secret", input(hit, near))
	if got := lines(rows); len(got) != 3 || got[0] != 3 || got[1] != 4 || got[2] != 5 {
		t.Fatalf("lines %v %+v", got, rows)
	}
	if got := evidence(rows[0], "kind"); got != "GitHub token" {
		t.Errorf("kind %q", got)
	}
	for _, r := range rows {
		if r.Certain {
			t.Error("a secret is shown, never auto-edited")
		}
		for _, e := range r.Evidence {
			if strings.Contains(e.Value, "hunter2hunter2") || strings.Contains(e.Value, strings.Repeat("aB3", 12)) {
				t.Errorf("evidence shows the secret: %+v", e)
			}
		}
	}
}

// A secret's value is never in a row: the secret row's passage, and every
// other check's row on that line or passage, show it redacted. The ids hash
// the original text, so a rotated secret is a new row.
func TestSecretIsRedactedInEveryRow(t *testing.T) {
	tok := "ghp_" + strings.Repeat("aB3", 12)
	para := "Never paste the deploy token " + tok + " into a chat, an issue or a pull request description, ever."
	f := &discover.File{Path: "/r/CLAUDE.md", Class: discover.ClassRepo,
		Content: "- Never commit password = \"hunter2hunter2\" here.\n\n" + para + "\n\n" + para + "\n"}
	rows := Run(ctx, input(f))
	checks := map[string]int{}
	for _, r := range rows {
		checks[r.Check]++
		for _, s := range []string{"hunter2hunter2", tok} {
			if strings.Contains(r.Passage, s) || strings.Contains(r.Summary, s) {
				t.Errorf("%s row shows the secret: %q", r.Check, r.Passage)
			}
			for _, e := range r.Evidence {
				if strings.Contains(e.Value, s) {
					t.Errorf("%s evidence shows the secret: %+v", r.Check, e)
				}
			}
		}
	}
	if checks["secret"] != 3 || checks["negative-rule"] != 3 || checks["duplicate"] != 2 {
		t.Fatalf("checks %v", checks)
	}
	if rows[0].Check != "negative-rule" || rows[1].Check != "secret" ||
		rows[1].ID != row.ID(f.Path, "secret", "- Never commit password = \"hunter2hunter2\" here.") ||
		!strings.Contains(rows[1].Passage, "hunt… (14 chars)") || rows[0].Passage != rows[1].Passage {
		t.Fatalf("%+v\n%+v", rows[0], rows[1])
	}
}
