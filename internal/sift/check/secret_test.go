package check

import (
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/discover"
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
