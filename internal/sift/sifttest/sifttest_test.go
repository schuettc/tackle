package sifttest_test

import (
	"os"
	"reflect"
	"testing"

	"github.com/schuettc/tackle/internal/sift/discover"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
)

// Env clears every variable sift's own git calls drop, so fixtures and
// production pick the same repository.
func TestEnvClearsWhatGitEnvDrops(t *testing.T) {
	for _, k := range []string{"GIT_DIR", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_PREFIX", "GIT_NAMESPACE", "GIT_QUARANTINE_PATH"} {
		t.Setenv(k, "/elsewhere")
	}
	st.Env(t)
	if env := os.Environ(); !reflect.DeepEqual(discover.GitEnv(env), env) {
		t.Fatal("a repository-selecting variable is still set")
	}
}
