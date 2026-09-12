package projtui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// stubModels is an injected resolver so these tests never shell out to pi.
func stubModels(agent string) []string {
	switch agent {
	case "pi":
		return []string{
			"claude-bridge/opus", "openai-codex/gpt-5.6-sol", "openai-codex/gpt-6-astra",
		}
	case "claude":
		return []string{"claude-opus-4-8", "claude-sonnet-4-6"}
	default: // cursor, none
		return nil
	}
}

// newModelProjectModel drills into project with the stub model resolver, so the
// model menu is populated the way it would be from real config.
func newModelProjectModel(t *testing.T, defaultAgent, project string) Model {
	t.Helper()
	return newModel(nil, nil, defaultAgent, true, stubModels).drillInto(project)
}

// openNewWork opens the "+ new work…" inline input.
func openNewWork(t *testing.T, m Model) Model {
	t.Helper()
	m = press(m, "enter")
	if m.inputKind != inputNewWork {
		t.Fatal("enter should open the new-work input")
	}
	return m
}

func TestShiftTabOpensModelOverlay(t *testing.T) {
	m := openNewWork(t, newModelProjectModel(t, "pi", "repo-a"))
	if m.modelChoice() != "claude-bridge/opus" {
		t.Fatalf("default model = %q", m.modelChoice())
	}
	m = press(m, "shift+tab")
	if m.inputKind != inputSelectModel {
		t.Fatal("shift+tab should open the model overlay")
	}
	// Overlay opens framed on the current choice; move down and select.
	m = press(m, "down")
	m = press(m, "enter")
	if m.inputKind != inputNewWork {
		t.Fatal("enter in the overlay should return to naming")
	}
	if got := m.modelChoice(); got != "openai-codex/gpt-5.6-sol" {
		t.Fatalf("selected model = %q, want openai-codex/gpt-5.6-sol", got)
	}
}

func TestModelOverlayEscKeepsCurrent(t *testing.T) {
	m := openNewWork(t, newModelProjectModel(t, "pi", "repo-a"))
	m = press(m, "shift+tab")
	m = press(m, "down") // move, but…
	m = press(m, "esc")  // …cancel
	if m.inputKind != inputNewWork {
		t.Fatal("esc should return to naming")
	}
	if got := m.modelChoice(); got != "claude-bridge/opus" {
		t.Fatalf("esc must keep the current model, got %q", got)
	}
}

func TestModelOverlayFilters(t *testing.T) {
	m := openNewWork(t, newModelProjectModel(t, "pi", "repo-a"))
	m = press(m, "shift+tab")
	m = typeString(m, "astra") // matches only openai-codex/gpt-6-astra
	if vis := m.visibleModels(); len(vis) != 1 || vis[0] != "openai-codex/gpt-6-astra" {
		t.Fatalf("filter 'astra' → %v, want [openai-codex/gpt-6-astra]", m.visibleModels())
	}
	m = press(m, "enter")
	if got := m.modelChoice(); got != "openai-codex/gpt-6-astra" {
		t.Fatalf("selected filtered model = %q", got)
	}
}

// Changing the agent re-seeds the model menu to that agent's default, and an
// agent with no models clears the choice.
func TestTabReseedsModelForNewAgent(t *testing.T) {
	m := openNewWork(t, newModelProjectModel(t, "pi", "repo-a"))
	if m.agentChoice() != "pi" || m.modelChoice() != "claude-bridge/opus" {
		t.Fatalf("seed = %q/%q", m.agentChoice(), m.modelChoice())
	}
	m = press(m, "tab") // pi → claude
	if m.agentChoice() != "claude" || m.modelChoice() != "claude-opus-4-8" {
		t.Fatalf("after tab = %q/%q, want claude/claude-opus-4-8", m.agentChoice(), m.modelChoice())
	}
	m = press(m, "tab") // claude → cursor (no models)
	if m.agentChoice() != "cursor" || m.modelChoice() != "" {
		t.Fatalf("cursor should have no model, got %q/%q", m.agentChoice(), m.modelChoice())
	}
}

func TestResultCarriesModel(t *testing.T) {
	m := openNewWork(t, newModelProjectModel(t, "pi", "repo-a"))
	m = press(m, "shift+tab")
	m = press(m, "down") // second model
	m = press(m, "enter")
	m = typeString(m, "nfl cutover")
	m = press(m, "enter") // submit
	if m.Result.Kind != "new" || m.Result.Model != "openai-codex/gpt-5.6-sol" {
		t.Fatalf("result = %+v", m.Result)
	}
}

// selectModel is how NewFor preselects a project's default_model pin.
func TestSelectModelPreselects(t *testing.T) {
	m := newModelProjectModel(t, "pi", "repo-a").selectModel("openai-codex/gpt-6-astra")
	if got := m.modelChoice(); got != "openai-codex/gpt-6-astra" {
		t.Fatalf("selectModel = %q", got)
	}
	m2 := newModelProjectModel(t, "pi", "repo-a").selectModel("nope/x")
	if got := m2.modelChoice(); got != "claude-bridge/opus" {
		t.Fatalf("unknown pin changed model to %q", got)
	}
}

func TestFooterShowsModelPerAgentAndOverlay(t *testing.T) {
	m := newModelProjectModel(t, "pi", "repo-a")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = next.(Model)
	m = openNewWork(t, m)
	if v := m.View(); !strings.Contains(v, "model:claude-bridge/opus") {
		t.Fatalf("pi new-work footer should show the model, got:\n%s", v)
	}
	// The overlay shows its own legend and header.
	m = press(m, "shift+tab")
	if v := m.View(); !strings.Contains(v, "select model") || !strings.Contains(v, "select") {
		t.Fatalf("overlay should show the model list, got:\n%s", v)
	}
	m = press(m, "esc")
	// cursor has no models → new-work footer omits the model.
	m = press(m, "tab") // pi → claude
	m = press(m, "tab") // claude → cursor
	if v := m.View(); strings.Contains(v, "model:") {
		t.Fatalf("cursor footer must omit the model, got:\n%s", v)
	}
}

// Ctrl-D in the model overlay sets the highlighted model as the global default:
// it persists via the injected saveDefault, selects it, updates the live
// default marker, and confirms in the footer.
func TestCtrlDSetsGlobalDefault(t *testing.T) {
	m := openNewWork(t, newModelProjectModel(t, "pi", "repo-a"))
	var saved string
	m.saveDefault = func(model string) error { saved = model; return nil }
	m = press(m, "shift+tab")
	m = press(m, "down") // highlight openai-codex/gpt-5.6-sol
	m = press(m, "ctrl+d")

	if saved != "openai-codex/gpt-5.6-sol" {
		t.Fatalf("saveDefault got %q", saved)
	}
	if m.defaultModel != "openai-codex/gpt-5.6-sol" {
		t.Fatalf("live default = %q", m.defaultModel)
	}
	if m.modelChoice() != "openai-codex/gpt-5.6-sol" {
		t.Fatalf("^d should also select it, got %q", m.modelChoice())
	}
	if m.inputKind != inputSelectModel {
		t.Fatal("^d should keep the overlay open")
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	if v := next.(Model).View(); !strings.Contains(v, "default model set") || !strings.Contains(v, "★ default") {
		t.Fatalf("overlay should confirm and mark the default, got:\n%s", v)
	}
}

// A failing saveDefault surfaces the error and does not move the live default.
func TestCtrlDSaveErrorSurfaces(t *testing.T) {
	m := openNewWork(t, newModelProjectModel(t, "pi", "repo-a"))
	m.saveDefault = func(string) error { return errors.New("boom") }
	m = press(m, "shift+tab")
	m = press(m, "ctrl+d")
	if m.defaultModel != "" {
		t.Fatalf("default must not move on save error, got %q", m.defaultModel)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	if v := next.(Model).View(); !strings.Contains(v, "could not save default") {
		t.Fatalf("save error should surface, got:\n%s", v)
	}
}
