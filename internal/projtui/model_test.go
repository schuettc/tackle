package projtui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/schuettc/tackle/internal/proj"
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

// newTestModelWithRefresh builds an entrance model from injected rows and
// installs a custom refresh function used by the live-refresh tick.
func newTestModelWithRefresh(rows []Row, refresh func() (sessions, projects []Row)) Model {
	m := newTestModel(rows)
	m.refresh = refresh
	return m
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
	case "ctrl+s":
		msg = tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+a":
		msg = tea.KeyMsg{Type: tea.KeyCtrlA}
	case "ctrl+x":
		msg = tea.KeyMsg{Type: tea.KeyCtrlX}
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

// sessionsScope flips the entrance to its live-sessions scope (the entrance
// defaults to folders), for tests that exercise session rows at the entrance.
func sessionsScope(m Model) Model {
	m.scope = scopeSessions
	return m.rebuildEntrance()
}

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
	m = sessionsScope(m)
	m = press(m, "enter") // cursor on the first row (the session)
	if m.Result.Kind != "jump" || m.Result.Name != "bettor-help/data-lake" || m.Result.Socket != "proj-bettor-help" {
		t.Fatalf("jump result = %+v", m.Result)
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

func TestTabTogglesEntranceScope(t *testing.T) {
	m := newTestModel(nil)
	if m.scope != scopeFolders {
		t.Fatal("entrance should default to the folders scope")
	}
	m = press(m, "tab")
	if m.scope != scopeSessions {
		t.Fatal("tab should switch the entrance to sessions")
	}
	m = press(m, "tab")
	if m.scope != scopeFolders {
		t.Fatal("tab should switch the entrance back to folders")
	}
}

func TestTabCyclesAgentInNewWorkInput(t *testing.T) {
	m := newProjectModel("tools-workspace", nil)
	m = press(m, "enter") // open the "+ new work…" input
	if m.inputKind != inputNewWork {
		t.Fatal("enter on + new work should open the input")
	}
	first := m.agentChoice()
	m = press(m, "tab")
	if m.agentChoice() == first {
		t.Fatal("tab should cycle the agent while naming new work")
	}
}

func TestCtrlSTogglesSidebarInNewWorkInput(t *testing.T) {
	m := newProjectModel("tools-workspace", nil)
	m = press(m, "enter") // open the "+ new work…" input
	before := m.sidebarChoice
	m = press(m, "ctrl+s")
	if m.sidebarChoice == before {
		t.Fatal("ctrl+s should toggle the sidebar while naming new work")
	}
}

func TestBrowseFooterOmitsAgentAndSidebar(t *testing.T) {
	m := newProjectModel("tools-workspace", nil)
	m.help.ShowAll = true
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)
	v := m.View()
	if strings.Contains(v, "agent:") || strings.Contains(v, "sidebar:") {
		t.Fatalf("browse footer must not show agent/sidebar, got:\n%s", v)
	}
}

func TestAddRootFlow(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "proj")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "roots"), []byte("~/code\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := os.MkdirAll(filepath.Join(home, "code", "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "more", "beta"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Inject a tmux-free refresh: rebuild project rows from roots alone.
	refresh := func() (sessions, projects []Row) {
		r, err := proj.LoadRoots()
		if err != nil {
			return nil, nil
		}
		return buildRows(r, nil)
	}
	m := newTestModelWithRefresh(nil, refresh)
	m = press(m, "ctrl+a")
	if m.inputKind != inputAddRoot {
		t.Fatal("ctrl+a should open the add-root input")
	}
	m = typeString(m, filepath.Join(home, "more"))
	m = press(m, "enter")
	if m.inputKind != inputNone {
		t.Fatalf("valid submit should close the input, kind=%d", m.inputKind)
	}

	found := false
	for _, r := range m.visibleRows() {
		if r.Label == "beta" {
			found = true
		}
	}
	if !found {
		t.Fatal("newly added root's project (beta) should appear in the entrance")
	}
}

func TestAddRootRejectsBadPath(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "proj")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "roots"), []byte("~/code\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	m := newTestModel(nil)
	m = press(m, "ctrl+a")
	m = typeString(m, filepath.Join(home, "does-not-exist"))
	m = press(m, "enter")
	if m.inputKind != inputAddRoot {
		t.Fatal("invalid path should keep the add-root input open")
	}
	if m.footerHint == "" {
		t.Fatal("invalid path should surface a footer hint")
	}
}

func TestReapConfirmAndKill(t *testing.T) {
	t.Setenv("TMUX", "") // CurrentSessionName() returns "" so nothing is guarded
	sess := Row{Kind: RowSession, Label: "alpha", Name: "alpha", Socket: "s1", Project: "alpha"}
	m := sessionsScope(newTestModel([]Row{sess}))
	killed := ""
	m.kill = func(socket, name string) error { killed = name; return nil }
	m.refresh = func() (sessions, projects []Row) { return nil, nil } // session gone after kill

	m = press(m, "ctrl+x")
	if m.reapConfirm != "alpha" {
		t.Fatalf("first ^x should arm confirm, got %q", m.reapConfirm)
	}
	if killed != "" {
		t.Fatal("first ^x must not kill")
	}

	m = press(m, "ctrl+x")
	if killed != "alpha" {
		t.Fatalf("second ^x should kill, killed=%q", killed)
	}
	if m.reapConfirm != "" {
		t.Fatal("confirm should clear after the kill")
	}
	for _, r := range m.visibleRows() {
		if r.Kind == RowSession {
			t.Fatal("session row should be gone after reap")
		}
	}
}

func TestReapCancelledByOtherKey(t *testing.T) {
	t.Setenv("TMUX", "")
	sess := Row{Kind: RowSession, Label: "alpha", Name: "alpha", Socket: "s1", Project: "alpha"}
	m := sessionsScope(newTestModel([]Row{sess}))
	killed := false
	m.kill = func(socket, name string) error { killed = true; return nil }

	m = press(m, "ctrl+x")
	if m.reapConfirm == "" {
		t.Fatal("^x should arm confirm")
	}
	m = press(m, "down") // any other key cancels
	if m.reapConfirm != "" {
		t.Fatal("a non-^x key should cancel the pending reap")
	}
	m = press(m, "ctrl+x") // re-arms, does not kill
	if killed {
		t.Fatal("reap must not execute without a confirming second ^x")
	}
}

func TestReapIgnoresNonSessionRow(t *testing.T) {
	t.Setenv("TMUX", "")
	m := newProjectModel("tools-workspace", nil) // cursor on "+ new work…"
	killed := false
	m.kill = func(socket, name string) error { killed = true; return nil }
	m = press(m, "ctrl+x")
	if killed || m.reapConfirm != "" {
		t.Fatal("reaping a non-session row should be a no-op")
	}
}

func TestSelectAgentPreselectsChoice(t *testing.T) {
	// config default "pi" is first; selecting "claude" should move the cycle.
	m := newTestModel(nil).selectAgent("claude")
	if got := m.agentChoice(); got != "claude" {
		t.Fatalf("agentChoice: got %q, want %q", got, "claude")
	}
	// an agent not in the cycle leaves the selection unchanged.
	m2 := newTestModel(nil)
	before := m2.agentChoice()
	if got := m2.selectAgent("nope").agentChoice(); got != before {
		t.Fatalf("unknown agent changed selection: got %q, want %q", got, before)
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
	if m.inputKind == inputNone {
		t.Fatal("invalid submit should keep the input open")
	}
}

// TestSessionRowCopiesAttentionCounts verifies that Unread and ActionRequired
// are preserved on Row when a session is built via newModel (the same code path
// New() uses). No live tmux/muster dependency: rows are injected directly.
func TestSessionRowCopiesAttentionCounts(t *testing.T) {
	input := []Row{
		{
			Kind:           RowSession,
			Label:          "bettor-help/data-lake",
			Socket:         "proj-bettor-help",
			Name:           "bettor-help/data-lake",
			Project:        "bettor-help",
			Unread:         3,
			ActionRequired: 1,
		},
	}
	m := sessionsScope(newTestModel(input))
	vis := m.visibleRows()
	if len(vis) == 0 {
		t.Fatal("expected at least one visible row")
	}
	row := vis[0]
	if row.Unread != 3 {
		t.Errorf("Unread: got %d, want 3", row.Unread)
	}
	if row.ActionRequired != 1 {
		t.Errorf("ActionRequired: got %d, want 1", row.ActionRequired)
	}
}

// TestRowShowsAttention verifies a session row renders the ✉ marker plus its
// agent and state in the list pane.
func TestRowShowsAttention(t *testing.T) {
	m := sessionsScope(newTestModel([]Row{
		{Kind: RowSession, Label: "p/w", Agent: "pi", State: "working", Unread: 2},
	}))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	v := m.View()
	for _, want := range []string{"✉2", "pi", "working"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view missing %q, got:\n%s", want, v)
		}
	}
}

// TestPreviewShowsGit highlights a session row whose Dir is a real git repo and
// asserts the preview pane names the branch. Skipped when git is absent.
func TestPreviewShowsGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	run := func(a ...string) { exec.Command("git", append([]string{"-C", dir}, a...)...).Run() }
	run("init", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644)
	run("add", ".")
	run("commit", "-m", "one")

	m := sessionsScope(newTestModel([]Row{
		{Kind: RowSession, Label: "p/w", Agent: "pi", State: "working", Dir: dir},
	}))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	v := m.previewPane(34)
	if !strings.Contains(v, "main") {
		t.Fatalf("preview missing branch name, got:\n%s", v)
	}
}

// TestTickRefreshPreservesSelection verifies the live-refresh tick re-runs
// discovery, rebuilds rows, and keeps the view/filter and a clamped cursor.
func TestTickRefreshPreservesSelection(t *testing.T) {
	m := newTestModelWithRefresh(
		[]Row{{Kind: RowSession, Label: "p/w1", Name: "p/w1"}},
		func() (s, p []Row) {
			return []Row{
				{Kind: RowSession, Label: "p/w2", Name: "p/w2"},
				{Kind: RowSession, Label: "p/w1", Name: "p/w1"},
			}, nil
		},
	)
	m = sessionsScope(m)
	m.cursor = 1

	next, cmd := m.Update(tickMsg{})
	m = next.(Model)

	if cmd == nil {
		t.Fatal("tick should re-arm the tick command")
	}
	if m.cursor < 0 || m.cursor >= len(m.visibleRows()) {
		t.Fatalf("cursor not clamped after refresh: %d of %d", m.cursor, len(m.visibleRows()))
	}
	if m.view != viewEntrance {
		t.Fatal("view changed on tick")
	}
	if m.filter != "" {
		t.Fatalf("filter changed on tick: %q", m.filter)
	}
	vis := m.visibleRows()
	if len(vis) != 2 || vis[0].Name != "p/w2" {
		t.Fatalf("rows not refreshed: %+v", vis)
	}
}

// TestPreviewAttentionLineOmitsZeroSegments verifies that when Unread==0 and
// ActionRequired>0, the preview attention line shows only "<N> action-required"
// and does not include a "✉0" segment.
func TestPreviewAttentionLineOmitsZeroSegments(t *testing.T) {
	m := sessionsScope(newTestModel([]Row{
		{Kind: RowSession, Label: "p/w", Agent: "pi", State: "working", Unread: 0, ActionRequired: 1},
	}))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24}) // wide enough to show preview
	m = next.(Model)
	v := m.View()
	if !strings.Contains(v, "1 action-required") {
		t.Fatalf("preview should contain '1 action-required', got:\n%s", v)
	}
	if strings.Contains(v, "✉0") {
		t.Fatalf("preview must not contain '✉0', got:\n%s", v)
	}
}

func TestViewRendersFooterHints(t *testing.T) {
	// Agent/sidebar are shown in the new-work input footer, where they apply.
	m := newProjectModel("tools-workspace", nil)
	m = press(m, "enter") // open "+ new work…"
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)
	v := m.View()
	if !strings.Contains(v, "agent:") || !strings.Contains(v, "sidebar:") {
		t.Fatalf("new-work footer should show agent/sidebar state, got:\n%s", v)
	}
	if !strings.Contains(v, "proj") {
		t.Fatalf("title bar should name the picker, got:\n%s", v)
	}
}

func TestBuildRowsShowsProjectsWithSessions(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tools-workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "changeword"), 0o755); err != nil {
		t.Fatal(err)
	}
	roots := proj.Roots{Roots: []string{root}}
	live := []proj.Session{{Name: "tools-workspace/tackle", Socket: "proj-tools-workspace"}}

	sessions, projects := buildRows(roots, live)
	if len(sessions) != 1 || sessions[0].Label != "tools-workspace/tackle" {
		t.Fatalf("sessions = %+v", sessions)
	}
	// The project that HAS a live session must still appear as a project row
	// (so you can drill into it for new work).
	var names []string
	for _, p := range projects {
		names = append(names, p.Label)
	}
	if !contains(names, "tools-workspace") || !contains(names, "changeword") {
		t.Fatalf("projects missing tools-workspace/changeword: %v", names)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
