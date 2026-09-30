package serve

import (
	osexec "os/exec"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/casebook/app"
)

func git(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := osexec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func appOpts() app.DecideOptions { return app.DecideOptions{By: "court", NoPush: true} }
