package tui

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"
	"github.com/schuettc/tackle/internal/notes"
)

// watchCmd blocks on the watcher until a relevant event for path arrives,
// then returns a DiskChangeMsg with the file's current contents. The model
// re-issues this command to keep listening. We watch the directory (not the
// file) so the watch survives atomic renames, and filter to our file.
// The path is read from the shared box on each event rather than captured
// once: the editor can adopt a different pad mid-run (see follow.go), and
// this goroutine outlives any single model value.
func watchCmd(w *fsnotify.Watcher, path *pathBox) tea.Cmd {
	return func() tea.Msg {
		for {
			select {
			case event, ok := <-w.Events:
				if !ok {
					return nil
				}
				cur := path.get()
				if filepath.Base(event.Name) != filepath.Base(cur) {
					continue
				}
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
					continue
				}
				content, err := notes.Read(cur)
				if err != nil {
					// A genuine read error (not a missing file, which notes.Read
					// treats as empty): don't blank the buffer with empty content —
					// keep waiting for a subsequent, readable change.
					continue
				}
				return DiskChangeMsg{Content: content}
			case _, ok := <-w.Errors:
				if !ok {
					return nil
				}
			}
		}
	}
}

// Run launches the editor on path. If the watcher cannot be created, the
// editor still runs — just without live auto-reload.
//
// resolve is polled so the editor can follow the coding-agent session (see
// follow.go). Pass nil to pin the editor to path.
func Run(path string, resolve func() string) int {
	m := New(path)
	m.Resolve = resolve

	if w, err := fsnotify.NewWatcher(); err == nil {
		defer w.Close()
		if err := w.Add(filepath.Dir(path)); err == nil {
			m.WatchCmd = watchCmd(w, m.pathRef)
		}
		// Adopting a pad in another directory needs that directory watched
		// too. Normally a no-op: every pad shares one store directory, and
		// fsnotify ignores a duplicate Add.
		m.AddDir = func(p string) tea.Cmd {
			_ = w.Add(filepath.Dir(p))
			return nil
		}
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
