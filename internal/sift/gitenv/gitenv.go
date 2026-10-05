// Package gitenv names the environment variables that point git at a
// repository other than the one -C names, so sift's git calls and its test
// fixtures clear the same ones.
package gitenv

import "strings"

// Selectors are git rev-parse --local-env-vars, less the config ones, plus
// the namespace and a receive hook's quarantine. A git hook exports some of
// them, so sift run from a hook would read the hook's repo.
var Selectors = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_IMPLICIT_WORK_TREE", "GIT_INDEX_FILE",
	"GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_GRAFT_FILE", "GIT_SHALLOW_FILE", "GIT_NO_REPLACE_OBJECTS",
	"GIT_REPLACE_REF_BASE", "GIT_PREFIX", "GIT_NAMESPACE", "GIT_QUARANTINE_PATH",
}

// Clean returns env without the Selectors.
func Clean(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		selects := false
		for _, s := range Selectors {
			if k == s {
				selects = true
				break
			}
		}
		if !selects {
			out = append(out, kv)
		}
	}
	return out
}
