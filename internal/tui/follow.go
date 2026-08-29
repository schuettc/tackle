package tui

import (
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/schuettc/tackle/internal/notes"
)

// How often the editor re-asks which pad it should be showing. The pad is
// keyed by the coding-agent session, and the editor usually STARTS BEFORE
// that session exists: the workspace builder opens the scratch pane and the
// agent's pane in the same breath, and the agent's SessionStart hook stamps
// the tmux option a moment later. Resolving once at startup would pin the
// editor to the directory-keyed pad it opened with and never move.
//
// A poll rather than a watch because the answer comes from a tmux option,
// which has no change notification.
const followInterval = time.Second

// followTickMsg asks the model to re-resolve its pad.
type followTickMsg struct{}

func followTick() tea.Cmd {
	return tea.Tick(followInterval, func(time.Time) tea.Msg { return followTickMsg{} })
}

// pathBox holds the pad path the watcher goroutine should filter on. The
// model is a value type that Bubble Tea copies on every Update, so the
// long-lived watcher cannot read a plain field and see a switch. Both sides
// share this instead.
type pathBox struct {
	mu sync.RWMutex
	p  string
}

func newPathBox(p string) *pathBox { return &pathBox{p: p} }

func (b *pathBox) get() string {
	if b == nil {
		return ""
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.p
}

func (b *pathBox) set(p string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.p = p
	b.mu.Unlock()
}

// switchTo moves the editor onto a different pad.
//
// Unsaved edits are flushed to the OLD pad first, synchronously. The whole
// point of switching is that the two pads belong to different conversations,
// so carrying an unsaved buffer across would file notes under the wrong one —
// and dropping them would lose work the operator typed.
func (m Model) switchTo(newPath string) (Model, tea.Cmd) {
	if newPath == "" || newPath == m.path {
		return m, nil
	}

	if m.dirty {
		// Ignore the error: a pad we can no longer write is not a reason to
		// refuse the switch, and the save-error surface belongs to the pad
		// currently on screen.
		_ = notes.Write(m.path, m.textarea.Value())
	}

	content, err := notes.Read(newPath)
	if err != nil {
		// Unreadable target: stay put rather than blank the buffer.
		return m, nil
	}

	m.path = newPath
	m.pathRef.set(newPath)
	m.textarea.SetValue(content)
	m.lastWritten = content
	m.dirty = false
	m.diskChanged = false
	m.saveErr = ""
	m.savedAt = time.Time{}
	// Any debounced save still in flight targets the old pad's generation.
	m.gen++

	var cmd tea.Cmd
	if m.AddDir != nil {
		cmd = m.AddDir(newPath)
	}
	return m, cmd
}
