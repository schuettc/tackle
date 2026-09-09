package proj

import (
	"reflect"
	"testing"
)

// A captured `pi --list-models` table: header, blank line, then rows across
// several providers. parsePiModels must skip the chrome, keep only the asked-for
// providers, and preserve provider order then catalog order within each.
const piListModelsFixture = `provider       model                                   context  max-out  thinking  images
anthropic      claude-opus-4-8                          1M       128K     yes       yes
anthropic      claude-sonnet-4-6                        1M       128K     yes       yes
claude-bridge  claude-opus-4-8                          1M       128K     yes       yes
claude-bridge  claude-sonnet-5                          1M       128K     yes       yes
llama.cpp      unsloth/Qwen3.8-27B-GGUF:Q8_0           262.1K   32.8K    yes       yes
openai-codex   gpt-5.6-sol                             272K     128K     yes       yes
openai-codex   gpt-6-astra                             272K     128K     yes       yes
`

func TestParsePiModelsFiltersAndOrders(t *testing.T) {
	got := parsePiModels(piListModelsFixture, []string{"claude-bridge", "openai-codex"})
	want := []string{
		"claude-bridge/claude-opus-4-8",
		"claude-bridge/claude-sonnet-5",
		"openai-codex/gpt-5.6-sol",
		"openai-codex/gpt-6-astra",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parsePiModels = %v, want %v", got, want)
	}
}

func TestParsePiModelsUnknownProviderYieldsNothing(t *testing.T) {
	if got := parsePiModels(piListModelsFixture, []string{"nope"}); len(got) != 0 {
		t.Errorf("unknown provider = %v, want empty", got)
	}
	if got := parsePiModels(piListModelsFixture, nil); len(got) != 0 {
		t.Errorf("nil providers = %v, want empty", got)
	}
}

func TestModelProvidersForAndModelFor(t *testing.T) {
	pin := "openai-codex/gpt-5.6-sol"
	cfg := Config{
		ModelProviders: map[string][]string{"pi": {"claude-bridge", "openai-codex"}},
		Projects: map[string]ProjectOverride{
			"repo-a": {DefaultModel: &pin},
		},
	}
	if got := cfg.ModelProvidersFor("pi"); !reflect.DeepEqual(got, []string{"claude-bridge", "openai-codex"}) {
		t.Errorf("ModelProvidersFor(pi) = %v", got)
	}
	if got := cfg.ModelProvidersFor("claude"); got != nil {
		t.Errorf("ModelProvidersFor(claude) = %v, want nil", got)
	}
	if got := cfg.ModelFor("repo-a"); got != pin {
		t.Errorf("ModelFor(repo-a) = %q, want %q", got, pin)
	}
	if got := cfg.ModelFor("repo-b"); got != "" {
		t.Errorf("ModelFor(repo-b) = %q, want empty", got)
	}
}

// claude's menu is built in and non-empty; cursor/none take no model.
func TestModelsForAgentClaudeAndNone(t *testing.T) {
	cfg := Config{}
	if got := ModelsForAgent(cfg, "claude"); len(got) == 0 {
		t.Fatal("claude models should be non-empty")
	}
	if got := ModelsForAgent(cfg, "cursor"); got != nil {
		t.Errorf("cursor models = %v, want nil", got)
	}
	if got := ModelsForAgent(cfg, "none"); got != nil {
		t.Errorf("none models = %v, want nil", got)
	}
	// pi with no configured providers offers nothing (never shells out).
	if got := ModelsForAgent(cfg, "pi"); got != nil {
		t.Errorf("pi models with no providers = %v, want nil", got)
	}
}
