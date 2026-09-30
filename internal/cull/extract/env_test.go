package extract

import (
	"strings"
	"testing"
)

func TestHelperEnvDropsAPIKey(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "sk-secret")
	t.Setenv("CULL_ENV_PROBE", "kept")
	env := HelperEnv(123)
	var probe, ctx bool
	for _, kv := range env {
		if strings.HasPrefix(kv, "TYPESAFE_API_KEY=") || strings.Contains(kv, "sk-secret") {
			t.Errorf("helper env carries the API key: %q", kv)
		}
		probe = probe || kv == "CULL_ENV_PROBE=kept"
		ctx = ctx || kv == "CULL_MAX_CONTEXT_BYTES=123"
	}
	if !probe || !ctx {
		t.Errorf("env missing probe (%v) or CULL_MAX_CONTEXT_BYTES=123 (%v)", probe, ctx)
	}
}
