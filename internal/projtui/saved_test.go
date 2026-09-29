package projtui

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/schuettc/tackle/internal/proj"
)

// savedFixture is a, b, c across one layout window and the unplaced group:
// a is not running, b is running with a tab, c is running with no tab.
func savedFixture() []Row {
	return []Row{
		{Kind: RowSaved, Label: "x/a", Name: "x/a", Socket: "proj-x", Agent: "pi", Window: 1, Conversation: "id-a", Transcript: true},
		{Kind: RowSaved, Label: "x/b", Name: "x/b", Socket: "proj-x", Agent: "claude", Window: 1, Running: true, Attached: true, Conversation: "id-b", Transcript: true},
		{Kind: RowSaved, Label: "y/c", Name: "y/c", Socket: "proj-y", Agent: "pi", Window: 0, Running: true, Conversation: "gone"},
	}
}

type savedFakes struct {
	rows     []Row
	forgot   []string
	killed   []string
	layoutN  [2]int
	layoutEr error
}

// newSavedModel builds an entrance model already on the saved scope, backed
// by fakes for every saved-scope side effect.
func newSavedModel(t *testing.T, rows []Row) (Model, *savedFakes) {
	t.Helper()
	f := &savedFakes{rows: rows}
	m := newTestModel(nil)
	m.loadSaved = func() ([]Row, error) { return f.rows, nil }
	m.forget = func(name string) error { f.forgot = append(f.forgot, name); return nil }
	m.kill = func(socket, name string) error { f.killed = append(f.killed, name); return nil }
	m.saveLayout = func() (int, int, error) { return f.layoutN[0], f.layoutN[1], f.layoutEr }
	m.refresh = func() (sessions, projects []Row) { return nil, nil }
	m = refreshNow(t, m)
	m = press(m, "tab") // folders → sessions
	m = press(m, "tab") // sessions → saved
	if m.scope != scopeSaved {
		t.Fatalf("scope = %v want saved", m.scope)
	}
	return m, f
}

func pressKey(m Model, k tea.KeyType) Model {
	next, _ := m.Update(tea.KeyMsg{Type: k})
	return next.(Model)
}

func space(m Model) Model { return pressKey(m, tea.KeySpace) }

func TestTabCyclesThreeScopes(t *testing.T) {
	m := newTestModel(nil)
	m.width, m.height = 100, 30
	want := []entranceScope{scopeSessions, scopeSaved, scopeFolders}
	for _, w := range want {
		m = press(m, "tab")
		if m.scope != w {
			t.Fatalf("scope = %v want %v", m.scope, w)
		}
		if w == scopeSaved && !strings.Contains(m.View(), "· saved") {
			t.Fatal("title does not name the saved scope")
		}
	}
}

func TestSavedDefaultChecks(t *testing.T) {
	m, _ := newSavedModel(t, savedFixture())
	if !m.checked["x/a"] || !m.checked["y/c"] {
		t.Fatalf("needs-restore rows not checked: %v", m.checked)
	}
	if _, ok := m.checked["x/b"]; ok {
		t.Fatalf("running+attached row is checkable: %v", m.checked)
	}
}

func TestSpaceToggles(t *testing.T) {
	m, _ := newSavedModel(t, savedFixture())
	m = space(m)
	if m.checked["x/a"] {
		t.Fatal("space did not uncheck the highlighted row")
	}
	m = space(m)
	if !m.checked["x/a"] || m.filter != "" {
		t.Fatalf("space did not recheck (checked=%v filter=%q)", m.checked["x/a"], m.filter)
	}
	// Outside the saved scope, space is still a filter character.
	s := sessionsScope(newTestModel(nil))
	if s = space(s); s.filter != " " {
		t.Fatalf("sessions scope filter = %q want a space", s.filter)
	}
}

func TestCtrlRRestoresChecked(t *testing.T) {
	m, _ := newSavedModel(t, savedFixture())
	m = space(m) // uncheck x/a
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = next.(Model)
	if cmd == nil || m.Result.Kind != "restore" || !slices.Equal(m.Result.Names, []string{"y/c"}) || m.Result.Name != "" {
		t.Fatalf("result = %+v (quit=%v)", m.Result, cmd != nil)
	}

	m, _ = newSavedModel(t, savedFixture())
	m = space(m)
	m = press(m, "down")
	m = press(m, "down")
	m = space(m) // uncheck y/c too
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = next.(Model)
	if cmd != nil || m.Result.Kind != "" || !strings.Contains(m.footerHint, "nothing checked") {
		t.Fatalf("empty ^r: result=%+v hint=%q", m.Result, m.footerHint)
	}
}

func TestEnterRestoresOne(t *testing.T) {
	m, _ := newSavedModel(t, savedFixture())
	m = press(m, "enter")
	want := Result{Kind: "restore", Names: []string{"x/a"}, Name: "x/a", Socket: "proj-x"}
	if m.Result.Kind != want.Kind || !slices.Equal(m.Result.Names, want.Names) || m.Result.Name != want.Name || m.Result.Socket != want.Socket {
		t.Fatalf("result = %+v want %+v", m.Result, want)
	}
}

func TestCtrlSSavesLayout(t *testing.T) {
	m, f := newSavedModel(t, savedFixture())
	f.layoutN = [2]int{2, 5}
	m = pressKey(m, tea.KeyCtrlS)
	if m.footerHint != "layout saved: 2 windows, 5 tabs" {
		t.Fatalf("hint = %q", m.footerHint)
	}
	f.layoutEr = errors.New("Ghostty is not running")
	m = pressKey(m, tea.KeyCtrlS)
	if m.footerHint != "save layout: Ghostty is not running" {
		t.Fatalf("hint = %q", m.footerHint)
	}
}

func TestCtrlXForgetsSaved(t *testing.T) {
	m, f := newSavedModel(t, savedFixture())
	m = press(m, "ctrl+x")
	m = press(m, "ctrl+x") // confirm on x/a (not running)
	if !slices.Equal(f.forgot, []string{"x/a"}) || len(f.killed) != 0 {
		t.Fatalf("forgot=%v killed=%v; want forget only", f.forgot, f.killed)
	}

	m, f = newSavedModel(t, savedFixture())
	m = press(m, "down") // x/b, running
	m = press(m, "ctrl+x")
	m = press(m, "ctrl+x")
	if !slices.Equal(f.killed, []string{"x/b"}) || !slices.Equal(f.forgot, []string{"x/b"}) {
		t.Fatalf("forgot=%v killed=%v; want both for a running row", f.forgot, f.killed)
	}
	_ = m
}

func TestReapForgetsInSessionsScope(t *testing.T) {
	m := sessionsScope(newTestModel([]Row{{Kind: RowSession, Label: "x/a", Name: "x/a", Socket: "proj-x"}}))
	var forgot []string
	m.kill = func(socket, name string) error { return nil }
	m.forget = func(name string) error { forgot = append(forgot, name); return nil }
	m.refresh = nil // no real tmux discovery after the reap
	m = press(m, "ctrl+x")
	m = press(m, "ctrl+x")
	if !slices.Equal(forgot, []string{"x/a"}) {
		t.Fatalf("reap did not forget: %v", forgot)
	}
}

func TestRefreshKeepsChecks(t *testing.T) {
	m, f := newSavedModel(t, savedFixture())
	m = space(m) // uncheck x/a
	f.rows = append(savedFixture(), Row{Kind: RowSaved, Label: "z/new", Name: "z/new", Socket: "proj-z"})
	m = refreshNow(t, m)
	if m.checked["x/a"] {
		t.Fatal("refresh re-checked a row the operator unchecked")
	}
	if !m.checked["z/new"] {
		t.Fatal("a newly saved row was not checked by default")
	}
}

func TestSavedEmpty(t *testing.T) {
	m, _ := newSavedModel(t, nil)
	m.width, m.height = 100, 30
	if !strings.Contains(m.View(), "nothing to restore") {
		t.Fatal("empty saved scope has no guidance")
	}
}

func TestSavedRowRender(t *testing.T) {
	m, _ := newSavedModel(t, savedFixture())
	rows := m.visibleRows()
	if rows[0].Kind != RowHeader || rows[0].Label != "window 1" {
		t.Fatalf("first row %+v; want the window 1 header", rows[0])
	}
	first := plainRow(rows[1])
	for _, want := range []string{"[x]", "x/a", "pi·saved"} {
		if !strings.Contains(first, want) {
			t.Errorf("row %q missing %q", first, want)
		}
	}
	// x/b is open (running with a tab): no checkbox.
	if second := plainRow(rows[2]); strings.Contains(second, "[") {
		t.Errorf("open row %q has a checkbox", second)
	}
	if rows[3].Kind != RowHeader || rows[3].Label != "unplaced" {
		t.Fatalf("row 3 %+v; want the unplaced header", rows[3])
	}
	// y/c is running (no tab): not flagged (no transcript), because a running
	// agent may not have written its file yet.
	if third := plainRow(rows[4]); !strings.Contains(third, "pi·detached") || strings.Contains(third, "(no transcript)") {
		t.Errorf("row %q: want pi·detached and no (no transcript)", third)
	}
}

func TestRowsFromSavedHidesOpenSessions(t *testing.T) {
	ss := []proj.SavedSession{
		{Name: "a/open", Window: 1, Running: true, Attached: true},
		{Name: "a/closed", Window: 1},
		{Name: "b/detached", Window: 2, Running: true},
		{Name: "c/open", Window: 0, Running: true, Attached: true},
	}
	var got []string
	for _, r := range rowsFromSaved(ss) {
		got = append(got, r.Name)
	}
	// An open session has nothing to restore, so it is not listed.
	if want := []string{"a/closed", "b/detached"}; !slices.Equal(got, want) {
		t.Fatalf("rows = %v want %v", got, want)
	}
}

func TestNoTranscriptOnlyWhenNotRunning(t *testing.T) {
	// A freshly started pi has an id but has not written its file yet.
	running := Row{Kind: RowSaved, Label: "a/1", Running: true, Conversation: "id", Transcript: false}
	if strings.Contains(plainRow(running), "no transcript") {
		t.Fatalf("running row %q flagged (no transcript)", plainRow(running))
	}
	closed := running
	closed.Running = false
	if !strings.Contains(plainRow(closed), "(no transcript)") {
		t.Fatalf("closed row %q missing (no transcript)", plainRow(closed))
	}
}

// refreshNow runs one background refresh to completion, as the Bubble Tea
// runtime would: start it, execute its command, feed the result back.
func refreshNow(t *testing.T, m Model) Model {
	t.Helper()
	m, cmd := m.startRefresh(false)
	if cmd == nil {
		t.Fatal("no refresh started")
	}
	next, _ := m.Update(cmd())
	return next.(Model)
}

func TestTickDoesNotBlockOnDiscovery(t *testing.T) {
	calls := 0
	m := newTestModelWithRefresh(nil, func() (s, p []Row) { calls++; return nil, nil })
	next, cmd := m.Update(tickMsg{})
	m = next.(Model)
	if calls != 0 {
		t.Fatal("tick ran discovery inside Update; it must run in the command")
	}
	if !m.refreshing || cmd == nil {
		t.Fatalf("refreshing=%v cmd=%v; want a refresh in flight", m.refreshing, cmd != nil)
	}
	cmd()
	if calls != 1 {
		t.Fatalf("discovery ran %d times want 1", calls)
	}
}

func TestTickWhileRefreshingSkips(t *testing.T) {
	orig := refreshInterval
	t.Cleanup(func() { refreshInterval = orig })
	refreshInterval = time.Millisecond
	m := newTestModelWithRefresh(nil, func() (s, p []Row) { return nil, nil })
	m, _ = m.startRefresh(false) // an action-started refresh is in flight
	next, cmd := m.Update(tickMsg{})
	if cmd == nil {
		t.Fatal("a skipped tick must re-arm itself")
	}
	if _, ok := cmd().(tickMsg); !ok {
		t.Fatal("tick during a refresh started a second discovery")
	}
	// The action-started refresh's result must not re-arm a second tick loop.
	_, cmd = next.(Model).Update(refreshedMsg{})
	if cmd != nil {
		t.Fatal("a non-tick refresh result re-armed the tick (duplicate loop)")
	}
}

func TestPrimeRunsInBackground(t *testing.T) {
	primed := 0
	m := newTestModel(nil)
	m.prime = func() { primed++ }
	if cmd := m.Init(); cmd == nil || primed != 0 {
		t.Fatalf("Init ran prime synchronously (primed=%d) or returned no command", primed)
	}
	msg := primeCmd(m.prime)()
	if _, ok := msg.(primedMsg); !ok || primed != 1 {
		t.Fatalf("primeCmd: msg=%T primed=%d; want primedMsg and one call", msg, primed)
	}
	if _, cmd := m.Update(msg); cmd == nil {
		t.Fatal("primedMsg should start a refresh so saved rows load")
	}
}

func TestSavedScopeShowsLoading(t *testing.T) {
	m := newTestModel(nil)
	m.width, m.height = 100, 30
	m = press(m, "tab")
	m = press(m, "tab")
	if !strings.Contains(m.View(), "loading") {
		t.Fatal("saved scope before the first load should say it is loading")
	}
}
