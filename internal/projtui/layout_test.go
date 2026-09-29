package projtui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/schuettc/tackle/internal/proj"
)

// These pin the picker's fixed layout: nothing on screen moves when the
// operator tabs, moves the cursor, or a refresh or message arrives.

const testW, testH = 100, 24

func sized(m Model) Model {
	next, _ := m.Update(tea.WindowSizeMsg{Width: testW, Height: testH})
	return next.(Model)
}

func screen(m Model) []string {
	return strings.Split(ansi.Strip(m.View()), "\n")
}

func sessionRows(n int) []Row {
	var rows []Row
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("p/w%02d", i)
		rows = append(rows, Row{Kind: RowSession, Label: name, Name: name, Project: "p", Agent: "pi"})
	}
	return rows
}

func TestFooterPinnedToBottom(t *testing.T) {
	cases := map[string]Model{
		"folders, two rows":   newTestModel([]Row{{Kind: RowProject, Label: "a"}, {Kind: RowProject, Label: "b"}}),
		"sessions, none":      sessionsScope(newTestModel(nil)),
		"sessions, overflow":  sessionsScope(newTestModel(sessionRows(60))),
		"saved, loaded, none": func() Model { m, _ := newSavedModel(t, nil); return m }(),
	}
	for name, m := range cases {
		m = sized(m)
		lines := screen(m)
		if len(lines) != testH {
			t.Errorf("%s: %d lines want %d", name, len(lines), testH)
			continue
		}
		if !strings.Contains(lines[testH-1], "? keys") {
			t.Errorf("%s: last line is not the legend: %q", name, lines[testH-1])
		}
		// A message takes the reserved line above the legend; nothing moves.
		m.footerHint = "layout saved: 2 windows, 5 tabs"
		lines = screen(m)
		if len(lines) != testH || !strings.Contains(lines[testH-2], "layout saved") || !strings.Contains(lines[testH-1], "? keys") {
			t.Errorf("%s: message moved the footer:\n%s", name, strings.Join(lines, "\n"))
		}
	}
}

func TestHelpOverlayDoesNotGrowFooter(t *testing.T) {
	m := sized(sessionsScope(newTestModel(sessionRows(3))))
	m = press(m, "?")
	lines := screen(m)
	if len(lines) != testH || !strings.Contains(lines[testH-1], "? keys") {
		t.Fatalf("? changed the layout:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "add root") {
		t.Fatal("full key list not shown")
	}
}

func frameBottom(lines []string) int {
	for i, l := range lines {
		if strings.Contains(l, "╰") {
			return i
		}
	}
	return -1
}

func TestPreviewFrameFixedHeight(t *testing.T) {
	dir := t.TempDir()
	rows := []Row{
		{Kind: RowSession, Label: "p/bare", Name: "p/bare"},
		{Kind: RowSession, Label: "p/full", Name: "p/full", Agent: "pi", State: "working", Unread: 2, ActionRequired: 1, Dir: dir},
	}
	m := sized(sessionsScope(newTestModel(rows)))
	first := frameBottom(screen(m))
	m = press(m, "down")
	if got := frameBottom(screen(m)); got != first || first < 0 {
		t.Fatalf("preview frame bottom moved from line %d to %d", first, got)
	}
	empty := sized(sessionsScope(newTestModel(nil)))
	if got := frameBottom(screen(empty)); got != first {
		t.Fatalf("empty preview frame bottom at %d want %d", got, first)
	}
}

// firstRowShown is the label of the top row drawn in the list.
func firstRowShown(m Model) string {
	start, _ := m.window(len(m.visibleRows()))
	return m.visibleRows()[start].Label
}

func TestEdgeScrolling(t *testing.T) {
	m := sized(sessionsScope(newTestModel(sessionRows(60))))
	slots := m.rowSlots()
	for i := 0; i < slots-1; i++ {
		m = press(m, "down")
		if got := firstRowShown(m); got != "p/w00" {
			t.Fatalf("list scrolled at cursor %d (top now %s); want still until the edge", m.cursor, got)
		}
	}
	m = press(m, "down") // past the bottom edge: exactly one row
	if got := firstRowShown(m); got != "p/w01" {
		t.Fatalf("top = %s want p/w01 after crossing the edge", got)
	}
	m = press(m, "up") // moving back inside the window scrolls nothing
	if got := firstRowShown(m); got != "p/w01" {
		t.Fatalf("top = %s want p/w01 after moving up inside the window", got)
	}
}

func TestCursorFollowsRowOnRefresh(t *testing.T) {
	a := Row{Kind: RowSession, Label: "p/a", Name: "p/a"}
	b := Row{Kind: RowSession, Label: "p/b", Name: "p/b"}
	m := sessionsScope(newTestModel([]Row{a, b}))
	m = press(m, "down") // on p/b
	next, _ := m.Update(refreshedMsg{live: true, sessions: []Row{{Kind: RowSession, Label: "p/0", Name: "p/0"}, a, b}})
	m = next.(Model)
	if got := m.visibleRows()[m.cursor].Name; got != "p/b" {
		t.Fatalf("highlight on %s after a row appeared above; want p/b", got)
	}
	// The highlighted session disappears: stay at the same position.
	next, _ = m.Update(refreshedMsg{live: true, sessions: []Row{{Kind: RowSession, Label: "p/0", Name: "p/0"}, a}})
	m = next.(Model)
	if got := m.visibleRows()[m.cursor].Name; got != "p/a" {
		t.Fatalf("highlight on %s after p/b vanished; want its neighbour p/a", got)
	}
}

func TestNamesAlignAcrossTabs(t *testing.T) {
	rows := []Row{
		{Kind: RowProject, Label: "name"},
		{Kind: RowSession, Label: "name", Agent: "pi", State: "idle"},
		{Kind: RowSaved, Label: "name", Agent: "pi", Checked: true},
		{Kind: RowSaved, Label: "name", Agent: "pi", Running: true},
	}
	want := -1
	for _, r := range rows {
		col := ansi.StringWidth(plainRow(r)[:strings.Index(plainRow(r), "name")])
		if want < 0 {
			want = col
		}
		if col != want {
			t.Errorf("%q: name at column %d want %d", plainRow(r), col, want)
		}
	}
}

func TestTabBarFixedAcrossTabs(t *testing.T) {
	m := sized(newTestModel(nil))
	first := screen(m)[0]
	for _, w := range []string{"folders", "sessions", "saved"} {
		if !strings.Contains(first, w) {
			t.Fatalf("tab bar %q missing %q", first, w)
		}
	}
	for i := 0; i < 2; i++ {
		m = press(m, "tab")
		if got := screen(m)[0]; strings.TrimRight(got, " ") != strings.TrimRight(first, " ") {
			t.Fatalf("title changed text on tab:\n%q\n%q", first, got)
		}
	}
}

func TestSavedGroupHeaders(t *testing.T) {
	m, _ := newSavedModel(t, savedFixture())
	vis := m.visibleRows()
	if vis[0].Kind != RowHeader || vis[0].Label != "window 1" {
		t.Fatalf("first row %+v; want the window 1 header", vis[0])
	}
	if vis[m.cursor].Kind == RowHeader {
		t.Fatal("cursor starts on a header")
	}
	for i := 0; i < 5; i++ {
		m = press(m, "down")
		if m.visibleRows()[m.cursor].Kind == RowHeader {
			t.Fatalf("cursor landed on a header at %d", m.cursor)
		}
	}
	// Filtering keeps a header only above a group that still has rows.
	m = typeString(m, "y/c")
	vis = m.visibleRows()
	if len(vis) != 2 || vis[0].Label != "unplaced" || vis[1].Name != "y/c" {
		t.Fatalf("filtered rows = %+v", vis)
	}
}

func TestFirstFrameNeedsNoDiscovery(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "code", "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "proj", "roots"), []byte("~/code\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("PROJ_HOME", t.TempDir())
	calls := 0
	orig := liveSessions
	t.Cleanup(func() { liveSessions = orig })
	liveSessions = func() []proj.Session { calls++; return nil }

	m, err := NewFor("", "")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("NewFor scanned tmux before the first frame")
	}
	if len(m.visibleRows()) != 1 || m.visibleRows()[0].Label != "alpha" {
		t.Fatalf("folders not shown on the first frame: %+v", m.visibleRows())
	}
	m = sized(press(m, "tab"))
	if !strings.Contains(strings.Join(screen(m), "\n"), "loading sessions") {
		t.Fatal("sessions tab before the first scan should say it is loading")
	}
}

// The saved tab has the longest key list; it must fit the fixed frame rather
// than push the screen up and scroll the title bar off.
func TestHelpOverlayFitsOnEveryTab(t *testing.T) {
	saved, _ := newSavedModel(t, savedFixture())
	for name, m := range map[string]Model{
		"folders":  newTestModel(nil),
		"sessions": sessionsScope(newTestModel(nil)),
		"saved":    saved,
		"project":  newProjectModel("p", nil),
	} {
		m = press(sized(m), "?")
		lines := screen(m)
		if len(lines) != testH || !strings.Contains(lines[0], "folders · sessions · saved") || !strings.Contains(lines[testH-1], "? keys") {
			t.Errorf("%s: ? moved the layout:\n%s", name, strings.Join(lines, "\n"))
		}
	}
}
