package projtui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/schuettc/tackle/internal/proj"
)

// The saved scope is the restore view: every session in proj's record, in
// saved Ghostty window/tab order, each needing work checked by default. The
// operator unchecks with space and restores the rest with ^r; unchecked
// sessions stay in the record for next time.

// nextScope is the scope tab moves to: folders → sessions → saved → folders.
func nextScope(s entranceScope) entranceScope {
	switch s {
	case scopeFolders:
		return scopeSessions
	case scopeSessions:
		return scopeSaved
	default:
		return scopeFolders
	}
}

// withRecord wires the saved scope to the real record, Ghostty and tmux.
func (m Model) withRecord() Model {
	m.loadSaved = defaultLoadSaved
	m.forget = proj.Forget
	m.saveLayout = func() (int, int, error) { return proj.SaveLayout(proj.CurrentLiveState().Running) }
	return m
}

// defaultLoadSaved reads the record and the live state into saved rows.
func defaultLoadSaved() ([]Row, error) {
	rec, err := proj.LoadRecord()
	if err != nil {
		return nil, err
	}
	return rowsFromSaved(proj.SavedSessions(rec, proj.CurrentLiveState(), proj.DefaultTranscripts())), nil
}

// rowsFromSaved converts saved sessions into rows, labelling the first row
// of each window group. A session that is open (running with a tab) is not
// listed: it has nothing to restore, and a row that cannot be selected only
// makes the view look broken.
func rowsFromSaved(ss []proj.SavedSession) []Row {
	rows := make([]Row, 0, len(ss))
	for _, s := range ss {
		if !s.NeedsRestore() {
			continue
		}
		group := ""
		if len(rows) == 0 || rows[len(rows)-1].Window != s.Window {
			group = "unplaced"
			if s.Window > 0 {
				group = fmt.Sprintf("window %d", s.Window)
			}
		}
		rows = append(rows, Row{
			Kind: RowSaved, Label: s.Name, Name: s.Name, Socket: s.Socket,
			Dir: s.Dir, Agent: s.Agent, Project: s.Project,
			Window: s.Window, Group: group, Conversation: s.Conversation,
			Running: s.Running, Attached: s.Attached, Transcript: s.Transcript,
		})
	}
	return rows
}

// needsRestore mirrors proj.SavedSession.NeedsRestore for a row.
func needsRestore(r Row) bool { return !r.Running || !r.Attached }

// reloadSaved re-reads the saved rows, keeping the operator's selection by
// name: a row seen before keeps its check, a new row that needs restoring
// starts checked, and a row that needs nothing is not checkable.
func (m Model) reloadSaved() Model {
	if m.loadSaved == nil {
		return m
	}
	rows, err := m.loadSaved()
	if err != nil {
		m.footerHint = err.Error()
		rows = nil
	}
	for _, r := range rows {
		if !needsRestore(r) {
			delete(m.checked, r.Name)
		} else if _, seen := m.checked[r.Name]; !seen {
			m.checked[r.Name] = true
		}
	}
	m.saved = rows
	return m.applyChecks()
}

// applyChecks copies the selection onto the saved and visible rows.
func (m Model) applyChecks() Model {
	for _, rows := range [][]Row{m.saved, m.rows} {
		for i := range rows {
			if rows[i].Kind == RowSaved {
				rows[i].Checked = m.checked[rows[i].Name]
			}
		}
	}
	return m
}

// rebuildKeepCursor rebuilds the entrance rows without losing the filter or
// (clamped) cursor.
func (m Model) rebuildKeepCursor() Model {
	filter, cursor := m.filter, m.cursor
	m = m.rebuildEntrance()
	m.filter, m.cursor = filter, cursor
	m.clampCursor()
	return m
}

// updateSaved handles the saved scope's own keys. ok=false hands the key on
// to the shared list handling (navigation, enter, ^x, filtering).
func (m Model) updateSaved(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	switch msg.Type {
	case tea.KeySpace:
		vis := m.visibleRows()
		if m.cursor < len(vis) && needsRestore(vis[m.cursor]) {
			name := vis[m.cursor].Name
			m.checked[name] = !m.checked[name]
			m = m.applyChecks()
		}
		return m, nil, true
	case tea.KeyCtrlR:
		var names []string
		for _, r := range m.saved {
			if m.checked[r.Name] {
				names = append(names, r.Name)
			}
		}
		if len(names) == 0 {
			m.footerHint = "nothing checked"
			return m, nil, true
		}
		m.Result = Result{Kind: "restore", Names: names}
		return m, tea.Quit, true
	case tea.KeyCtrlS:
		if m.saveLayout == nil {
			return m, nil, true
		}
		w, n, err := m.saveLayout()
		if err != nil {
			m.footerHint = "save layout: " + err.Error()
			return m, nil, true
		}
		m = m.reloadSaved().rebuildKeepCursor()
		m.footerHint = fmt.Sprintf("layout saved: %d windows, %d tabs", w, n)
		return m, nil, true
	}
	return m, nil, false
}
