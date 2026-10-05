package cli

import (
	"errors"
	"testing"

	st "github.com/schuettc/tackle/internal/sift/sifttest"
)

// siftEnv gives a test its own home, config and state, hermetic git, and no
// gh (so nothing reaches the network); it returns the home.
func siftEnv(t *testing.T) string {
	t.Helper()
	st.Env(t)
	home := st.Home(t)
	t.Setenv("SIFT_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	old := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookPath = old })
	return home
}
