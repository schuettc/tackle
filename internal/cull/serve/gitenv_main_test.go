package serve

import (
	"os"
	"testing"

	"github.com/schuettc/tackle/internal/sift/gitenv"
)

// TestMain clears the variables that point git at another repository. A git
// hook exports them (the pre-push hook from a linked worktree sets GIT_DIR),
// and without this the tests' git calls would write into that repository.
func TestMain(m *testing.M) {
	for _, k := range gitenv.Selectors {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
