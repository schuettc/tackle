package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/schuettc/tackle/internal/notes"
)

// runCmd executes a tea.Cmd (possibly a batch) and returns the messages it
// produces, so tests can drive the model deterministically.
func drainMsg(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

func TestTypingMarksDirtyAndSchedulesSave(t *testing.T) {
	m := New(filepath.Join(t.TempDir(), ".scratch.md"))
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = next.(Model)
	if !m.dirty {
		t.Fatal("typing should mark buffer dirty")
	}
	if cmd == nil {
		t.Fatal("typing should schedule a save (non-nil cmd)")
	}
}

func TestCtrlSWritesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".scratch.md")
	m := New(p)
	// type something first
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
	m = next.(Model)
	// ctrl+s returns a save command; execute it
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	msg := drainMsg(cmd)
	if _, ok := msg.(savedMsg); !ok {
		t.Fatalf("ctrl+s should yield savedMsg, got %T", msg)
	}
	got, _ := notes.Read(p)
	if got != "hi" {
		t.Fatalf("file after ctrl+s = %q, want %q", got, "hi")
	}
}

func TestDiskChangeWhileCleanReloads(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".scratch.md")
	m := New(p) // clean, empty buffer, lastWritten ""
	next, _ := m.Update(DiskChangeMsg{Content: "from claude"})
	m = next.(Model)
	if m.textarea.Value() != "from claude" {
		t.Fatalf("clean disk change should reload, got %q", m.textarea.Value())
	}
	if m.diskChanged {
		t.Fatal("reload should not set the changed-on-disk flag")
	}
}

func TestDiskChangeWhileDirtyFlags(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".scratch.md")
	m := New(p)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m', 'e'}})
	m = next.(Model) // now dirty with "me"
	next, _ = m.Update(DiskChangeMsg{Content: "from claude"})
	m = next.(Model)
	if !m.diskChanged {
		t.Fatal("dirty disk change should set the changed-on-disk flag")
	}
	if m.textarea.Value() != "me" {
		t.Fatalf("dirty disk change must not clobber, got %q", m.textarea.Value())
	}
}

func TestOverlappingSavesSerializeAndDrainLatest(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".scratch.md")
	m := New(p)

	// Type "a" and start a save via ctrl+s.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = next.(Model)
	next, saveCmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(Model)
	if !m.saving {
		t.Fatal("ctrl+s should mark a save in flight")
	}

	// While that save is in flight, type "b" (buffer becomes "ab").
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = next.(Model)

	// A debounce tick for the new content must NOT start a second concurrent
	// save while one is already in flight.
	next, tickCmd := m.Update(saveMsg{gen: m.gen})
	m = next.(Model)
	if tickCmd != nil {
		t.Fatal("no second save should be issued while one is in flight")
	}

	// The first save completes, having written the older content "a".
	saved := saveCmd()
	if _, ok := saved.(savedMsg); !ok {
		t.Fatalf("expected savedMsg, got %T", saved)
	}
	next, drainCmd := m.Update(saved)
	m = next.(Model)

	// Completion must drain: because the buffer changed to "ab" during the
	// in-flight save, a follow-up save is issued for the latest content.
	if drainCmd == nil {
		t.Fatal("completion should drain the newer content with a follow-up save")
	}
	drainCmd() // executes the follow-up write

	got, _ := notes.Read(p)
	if got != "ab" {
		t.Fatalf("disk should converge to latest content, got %q, want %q", got, "ab")
	}
}

func TestQuitWhenNotSavingFlushesAndQuits(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".scratch.md")
	m := New(p)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
	m = next.(Model)
	// ctrl+q while dirty and not saving: flush the buffer.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m = next.(Model)
	if !m.quitting {
		t.Fatal("ctrl+q should mark quitting")
	}
	if cmd == nil {
		t.Fatal("ctrl+q while dirty should flush")
	}
	saved := cmd()
	if _, ok := saved.(savedMsg); !ok {
		t.Fatalf("expected savedMsg from quit flush, got %T", saved)
	}
	next, quitCmd := m.Update(saved)
	m = next.(Model)
	if quitCmd == nil {
		t.Fatal("after flush, quit should issue tea.Quit")
	}
	if _, ok := quitCmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg, got %T", quitCmd())
	}
	got, _ := notes.Read(p)
	if got != "hi" {
		t.Fatalf("quit flush disk = %q, want %q", got, "hi")
	}
}

func TestQuitWhileSavingFlushesLatestThenQuits(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".scratch.md")
	m := New(p)
	// Type "a" and start a save (saving=true).
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = next.(Model)
	next, saveCmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(Model)
	if !m.saving {
		t.Fatal("ctrl+s should mark a save in flight")
	}
	// Type more while the save is in flight (buffer "ab").
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = next.(Model)
	// Quit while a save is in flight: must NOT start a concurrent write and
	// must NOT quit yet.
	next, quitCmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m = next.(Model)
	if !m.quitting {
		t.Fatal("ctrl+q should mark quitting")
	}
	if quitCmd != nil {
		t.Fatal("must not quit or start a write while a save is in flight")
	}
	// First save completes (wrote "a"). Buffer is "ab", so completion drains.
	saved := saveCmd()
	next, drainCmd := m.Update(saved)
	m = next.(Model)
	if drainCmd == nil {
		t.Fatal("completion should drain the newer content before quitting")
	}
	// Drain save completes (wrote "ab") -> now quit.
	saved2 := drainCmd()
	next, finalCmd := m.Update(saved2)
	m = next.(Model)
	if finalCmd == nil {
		t.Fatal("once latest content is flushed, quitting must issue tea.Quit")
	}
	if _, ok := finalCmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg on quit, got %T", finalCmd())
	}
	got, _ := notes.Read(p)
	if got != "ab" {
		t.Fatalf("quit must flush latest content, disk = %q, want %q", got, "ab")
	}
}

func TestCtrlRReloadsFromDiskWhenFlagged(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".scratch.md")
	if err := notes.Write(p, "original"); err != nil {
		t.Fatal(err)
	}
	m := New(p) // buffer "original", clean, lastWritten "original"
	// Local edit -> dirty.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	m = next.(Model)
	dirtyVal := m.textarea.Value()
	// Simulate an external writer changing the file on disk.
	if err := notes.Write(p, "external-edit"); err != nil {
		t.Fatal(err)
	}
	// The watcher would deliver this; feed the DiskChangeMsg with disk content.
	next, _ = m.Update(DiskChangeMsg{Content: "external-edit"})
	m = next.(Model)
	if !m.diskChanged {
		t.Fatal("dirty external change should flag, not clobber")
	}
	if m.textarea.Value() != dirtyVal {
		t.Fatalf("flag path must not clobber local edits, got %q want %q", m.textarea.Value(), dirtyVal)
	}
	// ctrl+r discards local edits, loads disk version.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = next.(Model)
	if m.textarea.Value() != "external-edit" {
		t.Fatalf("ctrl+r should load disk content, got %q", m.textarea.Value())
	}
	if m.diskChanged || m.dirty {
		t.Fatal("ctrl+r should clear flag and dirty")
	}
	if m.lastWritten != "external-edit" {
		t.Fatalf("ctrl+r should update lastWritten, got %q", m.lastWritten)
	}
}

func TestViewChrome(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".scratch.md")
	m := New(p)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m = next.(Model)

	v := m.View()
	if !strings.Contains(v, "scratch") {
		t.Fatalf("title bar should name the pane 'scratch', got:\n%s", v)
	}
	if !strings.Contains(v, "not saved yet") {
		t.Fatalf("empty status should read 'not saved yet', got:\n%s", v)
	}
	if strings.Contains(v, "notes…") {
		t.Fatal("the 'notes…' placeholder should be gone")
	}
	if strings.Contains(v, "ctrl+s") || strings.Contains(v, "ctrl+r") {
		t.Fatal("command hints should be gone from the chrome")
	}

	// After a successful save the status shows a saved-at time (HH:MM).
	next, _ = m.Update(savedMsg{content: ""})
	m = next.(Model)
	v = m.View()
	if !strings.Contains(v, "saved ") {
		t.Fatalf("status should show 'saved <time>' after a save, got:\n%s", v)
	}
}

func TestClearWithConfirm(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".scratch.md")
	if err := notes.Write(p, "keep me"); err != nil {
		t.Fatal(err)
	}
	m := New(p) // buffer "keep me"

	// ctrl+x arms the confirmation but does NOT wipe yet.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m = next.(Model)
	if !m.confirmingClear {
		t.Fatal("ctrl+x should arm the clear confirmation")
	}
	if m.textarea.Value() != "keep me" {
		t.Fatal("arming must not wipe the buffer")
	}
	if !strings.Contains(m.View(), "clear all?") {
		t.Fatalf("status line should show the confirm prompt, got:\n%s", m.View())
	}

	// A non-'y' key cancels; content survives.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = next.(Model)
	if m.confirmingClear {
		t.Fatal("a non-'y' key should cancel the confirmation")
	}
	if m.textarea.Value() != "keep me" {
		t.Fatalf("cancel must keep content, got %q", m.textarea.Value())
	}

	// Re-arm and confirm with 'y' → wipes the buffer and saves empty to disk.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m = next.(Model)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = next.(Model)
	if m.textarea.Value() != "" {
		t.Fatalf("'y' should clear the buffer, got %q", m.textarea.Value())
	}
	if cmd == nil {
		t.Fatal("clearing should trigger a save")
	}
	cmd() // execute the write
	got, _ := notes.Read(p)
	if got != "" {
		t.Fatalf("cleared file should be empty on disk, got %q", got)
	}
}
