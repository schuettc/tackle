package projtui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// --- test shims: drive Update the way scratch's tui tests do ---

// newTestModel builds an entrance model from injected rows (sessions first,
// then projects), with a fixed agent default and sidebar on.
func newTestModel(rows []Row) Model {
	var sessions, projects []Row
	for _, r := range rows {
		if r.Kind == RowSession {
			sessions = append(sessions, r)
		} else {
			projects = append(projects, r)
		}
	}
	return newModel(sessions, projects, "pi", true)
}

// newProjectModel builds a model already drilled into project, with the given
// session rows available to group under it.
func newProjectModel(project string, sessions []Row) Model {
	for i := range sessions {
		if sessions[i].Project == "" {
			sessions[i].Project = project
		}
	}
	return newModel(sessions, nil, "pi", true).drillInto(project)
}

func press(m Model, key string) Model {
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "backspace":
		msg = tea.KeyMsg{Type: tea.KeyBackspace}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

func typeString(m Model, s string) Model {
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return next.(Model)
}

func visibleCount(m Model) int { return len(m.visibleRows()) }

func firstRowKind(m Model) RowKind {
	vis := m.visibleRows()
	if len(vis) == 0 {
		return RowKind(-1)
	}
	return vis[0].Kind
}

// --- tests ---

func TestEntranceFilterAndDrillIn(t *testing.T) {
	m := newTestModel([]Row{
		{Kind: RowSession, Label: "bettor-help/data-lake", Socket: "proj-bettor-help", Name: "bettor-help/data-lake"},
		{Kind: RowProject, Label: "tools-workspace"},
	})
	m = typeString(m, "tools") // filter
	if visibleCount(m) != 1 {
		t.Fatalf("filter did not narrow: %d", visibleCount(m))
	}
	m = press(m, "enter") // drill into project
	if m.view != viewProject || m.project != "tools-workspace" {
		t.Fatalf("did not drill in: view=%v proj=%q", m.view, m.project)
	}
}

func TestProjectNewWorkAtTopProducesResult(t *testing.T) {
	m := newProjectModel("tools-workspace", nil)
	// cursor starts on the first row, which must be "+ new work…"
	if firstRowKind(m) != RowNewWork {
		t.Fatal("new work not at top")
	}
	m = press(m, "enter") // open inline input
	m = typeString(m, "nfl cutover")
	m = press(m, "enter") // submit
	r := m.Result
	if r.Kind != "new" || r.Project != "tools-workspace" || r.Work != "nfl-cutover" {
		t.Fatalf("result = %+v", r)
	}
}

func TestEntranceSessionJump(t *testing.T) {
	m := newTestModel([]Row{
		{Kind: RowSession, Label: "bettor-help/data-lake", Socket: "proj-bettor-help", Name: "bettor-help/data-lake"},
		{Kind: RowProject, Label: "tools-workspace"},
	})
	m = press(m, "enter") // cursor on the first row (the session)
	if m.Result.Kind != "jump" || m.Result.Name != "bettor-help/data-lake" || m.Result.Socket != "proj-bettor-help" {
		t.Fatalf("jump result = %+v", m.Result)
	}
}

func TestProjectHomeBaseProducesHomeResult(t *testing.T) {
	m := newProjectModel("tools-workspace", nil)
	m = press(m, "down")  // move off "+ new work…" onto "🏠 home base"
	m = press(m, "enter") // select home base
	r := m.Result
	if r.Kind != "new" || r.Project != "tools-workspace" || r.Work != "" || r.Name != "tools-workspace" {
		t.Fatalf("home result = %+v", r)
	}
}

func TestEscFromProjectReturnsToEntrance(t *testing.T) {
	m := newTestModel([]Row{{Kind: RowProject, Label: "tools-workspace"}})
	m = press(m, "enter") // drill in
	if m.view != viewProject {
		t.Fatal("expected project view")
	}
	m = press(m, "esc") // back out
	if m.view != viewEntrance {
		t.Fatalf("esc should return to entrance, view=%v", m.view)
	}
}

func TestTabCyclesAgentAndSTogglesSidebar(t *testing.T) {
	m := newTestModel(nil)
	first := m.agentChoice()
	m = press(m, "tab")
	if m.agentChoice() == first {
		t.Fatal("tab should cycle the agent choice")
	}
	before := m.sidebarChoice
	m = press(m, "s") // filter empty -> command
	if m.sidebarChoice == before {
		t.Fatal("s should toggle the sidebar choice")
	}
}

func TestInvalidWorkNameStaysInInput(t *testing.T) {
	m := newProjectModel("tools-workspace", nil)
	m = press(m, "enter") // open input
	m = typeString(m, "bad/name")
	m = press(m, "enter") // submit invalid
	if m.Result.Kind != "" {
		t.Fatalf("invalid work name must not produce a result, got %+v", m.Result)
	}
	if !m.inputting {
		t.Fatal("invalid submit should keep the input open")
	}
}

func TestViewRendersFooterHints(t *testing.T) {
	m := newTestModel(nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	v := m.View()
	if !strings.Contains(v, "agent:") || !strings.Contains(v, "sidebar:") {
		t.Fatalf("footer should show agent/sidebar state, got:\n%s", v)
	}
	if !strings.Contains(v, "proj") {
		t.Fatalf("title bar should name the picker, got:\n%s", v)
	}
}
