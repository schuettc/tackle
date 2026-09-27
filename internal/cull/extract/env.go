package extract

import (
	"fmt"
	"os"
	"strings"
)

// HelperEnv is the environment for a language helper subprocess (the Python
// and TypeScript extractors): the current environment minus
// TYPESAFE_API_KEY, plus CULL_MAX_CONTEXT_BYTES. The TS helper loads the
// project's own node_modules/typescript, which is third-party code, so the
// key must not reach it.
func HelperEnv(maxContext int) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TYPESAFE_API_KEY=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, fmt.Sprintf("CULL_MAX_CONTEXT_BYTES=%d", maxContext))
}
