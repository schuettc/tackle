package projtui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"

	"github.com/schuettc/tackle/internal/proj"
)

// Catppuccin Mocha palette, matching scratch's internal/tui chrome.
var (
	colMauve    = lipgloss.Color("#cba6f7")
	colGreen    = lipgloss.Color("#a6e3a1")
	colYellow   = lipgloss.Color("#f9e2af")
	colSubtext0 = lipgloss.Color("#a6adc8")
	colText     = lipgloss.Color("#cdd6f4")
	colSurface1 = lipgloss.Color("#45475a")
	colOverlay0 = lipgloss.Color("#6c7086")

	titleLabelStyle = lipgloss.NewStyle().Background(colSurface1).Foreground(colMauve).Bold(true)
	titleInfoStyle  = lipgloss.NewStyle().Background(colSurface1).Foreground(colText)
	titleBarStyle   = lipgloss.NewStyle().Background(colSurface1)

	// The selected row is a full-width bar (surface background + bright bold
	// text) so the cursor is unmistakable even in a long list.
	selectedStyle = lipgloss.NewStyle().Background(colSurface1).Foreground(colText).Bold(true)
	rowStyle      = lipgloss.NewStyle().Foreground(colText)
	dimStyle      = lipgloss.NewStyle().Foreground(colSubtext0)
	agentStyle    = lipgloss.NewStyle().Foreground(colGreen)
	hintStyle     = lipgloss.NewStyle().Foreground(colYellow)
	previewStyle  = lipgloss.NewStyle().Foreground(colText)
	previewHead   = lipgloss.NewStyle().Foreground(colMauve).Bold(true)
	attnStyle     = lipgloss.NewStyle().Foreground(colYellow).Bold(true)
	headerStyle   = lipgloss.NewStyle().Foreground(colOverlay0).Bold(true)

	// Tab bar: the current tab bright and underlined, the others dim, both on
	// the title bar's background.
	tabActiveStyle = lipgloss.NewStyle().Background(colSurface1).Foreground(colText).Bold(true).Underline(true)
	tabIdleStyle   = lipgloss.NewStyle().Background(colSurface1).Foreground(colOverlay0)

	// 1-col left/right breathing room around the whole picker.
	appStyle = lipgloss.NewStyle().Padding(0, 1)
	// A mauve left-edge accent on the selected row.
	accentStyle = lipgloss.NewStyle().Foreground(colMauve)
	// A rounded frame around the preview pane, giving the layout structure.
	previewFrame = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colOverlay0).
			Padding(0, 1)
)

// newHelp builds the help component themed to the Catppuccin palette.
func newHelp() help.Model {
	h := help.New()
	h.Styles.ShortKey = lipgloss.NewStyle().Foreground(colGreen)
	h.Styles.ShortDesc = lipgloss.NewStyle().Foreground(colSubtext0)
	h.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(colSurface1)
	h.Styles.FullKey = h.Styles.ShortKey
	h.Styles.FullDesc = h.Styles.ShortDesc
	h.Styles.FullSeparator = h.Styles.ShortSeparator
	h.Styles.Ellipsis = lipgloss.NewStyle().Foreground(colSurface1)
	return h
}

// The picker's layout is fixed: title bar on the first line, the footer's two
// lines (message, key legend) on the last two, and the list and preview at a
// constant height between them. Nothing moves when the operator tabs, moves
// the cursor, or a refresh or message arrives. When the terminal height is
// unknown (or tiny) the layout falls back to its natural height.

// minFixedHeight is the smallest terminal the fixed layout is used on.
const minFixedHeight = 10

// fixed reports whether the terminal is tall enough for the fixed layout.
func (m Model) fixed() bool { return m.height >= minFixedHeight }

// bodyHeight is the lines between the title (plus a blank) and the footer
// (a blank plus its two lines).
func (m Model) bodyHeight() int { return m.height - 5 }

// rowSlots is how many rows the list shows: the body minus the filter line,
// a blank, and the two reserved "more" lines.
func (m Model) rowSlots() int {
	if !m.fixed() {
		return 1 << 30
	}
	if n := m.bodyHeight() - 4; n > 1 {
		return n
	}
	return 1
}

func (m Model) View() string {
	iw := m.width - 2 // account for appStyle's 1-col side padding
	if iw < 20 {
		iw = m.width
	}
	listW, previewW, showPreview := m.layout(iw)
	if m.inputKind == inputSelectModel {
		showPreview = false // the model list wants the full width
	}

	listStyle := lipgloss.NewStyle().Width(listW)
	frame := previewFrame.Width(previewW)
	if m.fixed() {
		h := m.bodyHeight()
		listStyle = listStyle.Height(h).MaxHeight(h)
		frame = frame.Height(h - 2).MaxHeight(h) // the border adds the other two lines
	}
	var list string
	if m.help.ShowAll && !showPreview && m.inputKind == inputNone {
		list = listStyle.Render(m.keysPane(listW, m.bodyHeight()))
	} else {
		list = listStyle.Render(m.listPane(listW))
	}
	body := list
	if showPreview {
		// previewFrame adds a 1-col horizontal pad inside its Width, so the text
		// area is previewW-2.
		inner := m.previewPane(previewW - 2)
		if m.help.ShowAll && m.inputKind == inputNone {
			inner = m.keysPane(previewW-2, m.bodyHeight()-2)
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, list, "  ", frame.Render(inner))
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		m.titleBar(iw), "", body, "", m.footerView(iw))
	return appStyle.Render(content)
}

// layout splits the inner width into the list column and (when the terminal is
// wide enough) a preview column sized to ~42% of the width, hiding the preview
// on narrow terminals. previewW is the frame's Width value; its text area is
// previewW-2 (the frame's horizontal padding) and its outer footprint is
// previewW+2 (the border).
func (m Model) layout(iw int) (listW, previewW int, showPreview bool) {
	if iw < 80 {
		if iw <= 0 {
			iw = 40
		}
		return iw, 0, false
	}
	outer := iw * 42 / 100
	if outer < 36 {
		outer = 36
	}
	if outer > 66 {
		outer = 66
	}
	return iw - outer - 2, outer - 2, true
}

// scopeTabs are the entrance scopes in tab order, as the tab bar names them.
var scopeTabs = []struct {
	scope entranceScope
	label string
}{{scopeFolders, "folders"}, {scopeSessions, "sessions"}, {scopeSaved, "saved"}}

// titleBar always shows all three tabs, in the same place, with the current
// one highlighted; a drilled-in project is appended after them, so the tabs
// never shift.
func (m Model) titleBar(width int) string {
	title := titleLabelStyle.Render(" proj ") + titleInfoStyle.Render(" ")
	for i, t := range scopeTabs {
		if i > 0 {
			title += titleInfoStyle.Render(" · ")
		}
		if m.view == viewEntrance && m.scope == t.scope {
			title += tabActiveStyle.Render(t.label)
		} else {
			title += tabIdleStyle.Render(t.label)
		}
	}
	if m.view == viewProject {
		title += titleInfoStyle.Render("  › " + m.project)
	}
	title += titleInfoStyle.Render(" ")
	if width < lipgloss.Width(title) {
		width = lipgloss.Width(title)
	}
	return titleBarStyle.Width(width).Render(title)
}

// moreLine is a reserved "↑/↓ N more" line: blank when there is nothing more,
// so the rows never shift when it appears.
func moreLine(arrow string, n int) string {
	if n <= 0 {
		return ""
	}
	return dimStyle.Render(fmt.Sprintf("  %s %d more", arrow, n))
}

func (m Model) listPane(width int) string {
	if m.inputKind == inputSelectModel {
		return m.modelSelectPane(width)
	}
	var b strings.Builder

	// Filter / input line.
	switch {
	case m.inputKind != inputNone:
		label := "new work: "
		if m.inputKind == inputAddRoot {
			label = "add root: "
		}
		b.WriteString(hintStyle.Render(label) + m.input.View() + "\n")
	case m.filter != "":
		b.WriteString(dimStyle.Render("/"+m.filter) + "\n")
	default:
		b.WriteString(dimStyle.Render("type to filter") + "\n")
	}
	b.WriteString("\n")

	vis := m.visibleRows()
	start, end := m.window(len(vis))
	b.WriteString(moreLine("↑", start) + "\n")
	if len(vis) == 0 {
		b.WriteString(m.emptyMessage())
		return b.String()
	}
	for i := start; i < end; i++ {
		b.WriteString(m.renderRow(vis[i], i == m.cursor, width) + "\n")
	}
	if m.fixed() {
		for i := end - start; i < m.rowSlots(); i++ {
			b.WriteString("\n")
		}
	}
	b.WriteString(moreLine("↓", len(vis)-end))
	return b.String()
}

// emptyMessage returns the guidance shown when no rows are visible, tuned so a
// first-run user (no roots yet) is told exactly how to proceed.
func (m Model) emptyMessage() string {
	var msg string
	switch {
	case m.filter != "":
		msg = "no matches: esc to clear the filter"
	case m.scope == scopeSessions && !m.sessionsLoaded:
		msg = "loading sessions…"
	case m.scope == scopeSessions:
		msg = "no live sessions (tab for saved)"
	case m.scope == scopeSaved && !m.savedLoaded:
		msg = "loading saved sessions…"
	case m.scope == scopeSaved:
		msg = "nothing to restore: every saved session is open"
	case len(m.projects) == 0:
		msg = "no projects yet: ^a to add a root, ^e to edit roots"
	default:
		msg = "no folders match"
	}
	return dimStyle.Render("  " + msg)
}

// window returns the [start,end) slice of the n visible rows on screen. The
// start is the model's scroll offset, which only moves when the cursor
// crosses an edge (see followCursor), so moving inside the window never
// shifts the list.
func (m Model) window(n int) (start, end int) { return windowAt(m.offset, m.rowSlots(), n) }

func windowAt(offset, slots, n int) (start, end int) {
	start = offset
	if start > n-slots {
		start = n - slots
	}
	if start < 0 {
		start = 0
	}
	end = start + slots
	if end > n {
		end = n
	}
	return start, end
}

// scrollTo moves offset the least needed to keep cursor on screen. Above the
// first row of a group the group's header stays visible with it.
func scrollTo(offset, cursor, slots, n int, headerAbove bool) int {
	if cursor < offset {
		offset = cursor
		if headerAbove && offset > 0 {
			offset--
		}
	}
	if cursor >= offset+slots {
		offset = cursor - slots + 1
	}
	if offset > n-slots {
		offset = n - slots
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}

// followCursor applies scrollTo to the main list and the model overlay.
func (m Model) followCursor() Model {
	vis := m.visibleRows()
	above := m.cursor > 0 && m.cursor < len(vis) && vis[m.cursor-1].Kind == RowHeader
	m.offset = scrollTo(m.offset, m.cursor, m.rowSlots(), len(vis), above)
	m.modelOffset = scrollTo(m.modelOffset, m.modelCursor, m.rowSlots(), len(m.visibleModels()), false)
	return m
}

// modelSelectPane renders the inputSelectModel overlay: a filter line and the
// current agent's models as a windowed list with the cursor framed, styled the
// same as the main picker's rows.
func (m Model) modelSelectPane(width int) string {
	var b strings.Builder
	b.WriteString(hintStyle.Render("select model: type to filter"))
	if m.modelFilter != "" {
		b.WriteString(dimStyle.Render("  /" + m.modelFilter))
	}
	b.WriteString("\n\n")

	vis := m.visibleModels()
	start, end := windowAt(m.modelOffset, m.rowSlots(), len(vis))
	b.WriteString(moreLine("↑", start) + "\n")
	if len(vis) == 0 {
		b.WriteString(dimStyle.Render("  no models match"))
		return b.String()
	}
	for i := start; i < end; i++ {
		label := vis[i]
		if label == m.defaultModel {
			label += "  ★ default"
		}
		if i == m.modelCursor {
			barW := width - 1
			if barW < 1 {
				barW = 1
			}
			bar := selectedStyle.Width(barW).MaxWidth(barW).Render(" " + label)
			b.WriteString(accentStyle.Render("▎") + bar)
		} else {
			b.WriteString(lipgloss.NewStyle().MaxWidth(width).Render("  " + label))
		}
		b.WriteString("\n")
	}
	if m.fixed() {
		for i := end - start; i < m.rowSlots(); i++ {
			b.WriteString("\n")
		}
	}
	b.WriteString(moreLine("↓", len(vis)-end))
	return b.String()
}

// Every row is laid out the same way on every tab: a 3-column status cell,
// a space, the name, then dim metadata. So names line up in one column no
// matter which tab is showing.
//
//	folders   ▸  name
//	sessions  ●  name  pi·working  ✉2 !
//	saved    [x] name  pi·saved  (no transcript)

// statusCell is a row's 3-column status: the folder glyph, the session dot,
// or the saved checkbox (blank when restoring would change nothing).
func statusCell(r Row) string {
	switch r.Kind {
	case RowSession:
		return " ● "
	case RowProject:
		return " ▸ "
	case RowSaved:
		if !needsRestore(r) {
			return "   "
		}
		if r.Checked {
			return "[x]"
		}
		return "[ ]"
	case RowNewWork:
		return "   "
	}
	return "   "
}

// rowMeta is the dim text after the name.
func rowMeta(r Row) string {
	var s string
	switch r.Kind {
	case RowSession:
		if r.Agent != "" {
			s = "  " + r.Agent
			if r.State != "" {
				s += "·" + r.State
			}
		}
	case RowSaved:
		state := "saved"
		if r.Running {
			state = "detached"
		}
		if r.Agent != "" {
			s = "  " + r.Agent + "·" + state
		} else {
			s = "  " + state
		}
		// Only for a session that is not running: a running pi can hold an id
		// whose file it has not written yet.
		if r.Conversation != "" && !r.Transcript && !r.Running {
			s += "  (no transcript)"
		}
	}
	return s
}

// plainRow is the row's text with no per-token styling, used to fill the
// selected row's highlight bar uniformly.
func plainRow(r Row) string {
	if r.Kind == RowHeader {
		return r.Label
	}
	s := statusCell(r) + " " + r.Label + rowMeta(r)
	if r.Kind == RowSession {
		if r.Unread > 0 {
			s += fmt.Sprintf("  ✉%d", r.Unread)
		}
		if r.ActionRequired > 0 {
			s += " !"
		}
	}
	return s
}

// renderRow formats a single row. The selected row is a full-width highlight
// bar (mauve left accent + uniform bright text on a surface background);
// unselected rows keep their per-token colors. Both are truncated to width so
// a long name never breaks the layout. A group header is a dim label.
func (m Model) renderRow(r Row, selected bool, width int) string {
	if r.Kind == RowHeader {
		return lipgloss.NewStyle().MaxWidth(width).Render(headerStyle.Render("  " + r.Label))
	}
	if selected {
		barW := width - 1 // 1 col for the accent edge
		if barW < 1 {
			barW = 1
		}
		bar := selectedStyle.Width(barW).MaxWidth(barW).Render(" " + plainRow(r))
		return accentStyle.Render("▎") + bar
	}
	var cell string
	switch r.Kind {
	case RowSession:
		cell = " " + lipgloss.NewStyle().Foreground(dotColor(r.State)).Render("●") + " "
	case RowProject:
		cell = dimStyle.Render(statusCell(r))
	default:
		cell = statusCell(r)
	}
	line := cell + " " + r.Label + dimStyle.Render(rowMeta(r))
	if r.Kind == RowSession {
		line += attentionMarkers(r)
	}
	if width > 0 {
		return lipgloss.NewStyle().MaxWidth(width).Render("  " + line)
	}
	return rowStyle.Render("  ") + line
}

// attentionMarkers renders the trailing "  ✉N !" markers for a session row.
// ✉N appears only when Unread>0; the amber "!" only when ActionRequired>0.
func attentionMarkers(r Row) string {
	var out string
	if r.Unread > 0 {
		out += "  " + attnStyle.Render(fmt.Sprintf("✉%d", r.Unread))
	}
	if r.ActionRequired > 0 {
		out += " " + attnStyle.Render("!")
	}
	return out
}

// previewPane renders the rich preview for the highlighted row: name, agent +
// state, an attention line (only when there is any unread/action-required), and
// a git line (only for a real repo). GitStatus is computed lazily HERE, only for
// the highlighted row, and skipped when Dir is empty.
func (m Model) previewPane(width int) string {
	head := previewHead.Render("preview") + "\n" + dimStyle.Render(strings.Repeat("─", width)) + "\n"
	vis := m.visibleRows()
	if len(vis) == 0 || m.cursor >= len(vis) || vis[m.cursor].Kind == RowHeader {
		return head + dimStyle.Render("nothing selected")
	}
	r := vis[m.cursor]
	name := r.Label
	if r.Name != "" {
		name = r.Name
	}
	dir := r.Dir

	var b strings.Builder
	b.WriteString(head)
	b.WriteString(previewStyle.Render(trunc(name, width)) + "\n")

	if r.Kind == RowSaved {
		state := "saved"
		if r.Running {
			state = "running, no tab"
		}
		b.WriteString(agentStyle.Render(r.Agent) + dimStyle.Render(" · "+state) + "\n")
		if r.Conversation != "" {
			b.WriteString(dimStyle.Render(trunc(r.Conversation, width)) + "\n")
		}
	}
	if r.Kind == RowSession {
		if r.Agent != "" {
			state := r.State
			if state == "" {
				state = "unknown"
			}
			b.WriteString(agentStyle.Render(r.Agent) + dimStyle.Render(" · "+state) + "\n")
		}
		if r.Unread > 0 || r.ActionRequired > 0 {
			var segs []string
			if r.Unread > 0 {
				segs = append(segs, fmt.Sprintf("✉%d unread", r.Unread))
			}
			if r.ActionRequired > 0 {
				segs = append(segs, fmt.Sprintf("%d action-required", r.ActionRequired))
			}
			b.WriteString(attnStyle.Render(strings.Join(segs, " · ")) + "\n")
		}
	}

	if dir != "" {
		if g := proj.GitStatus(dir); g.Repo {
			b.WriteString(dimStyle.Render(trunc(fmt.Sprintf("git %s ↑%d ↓%d ●%d", g.Branch, g.Ahead, g.Behind, g.Dirty), width)) + "\n")
		}
		b.WriteString(dimStyle.Render(trunc(dir, width)))
	} else {
		b.WriteString(dimStyle.Render("no directory"))
	}
	return b.String()
}

// footerView is always exactly two lines: a message line (blank when there
// is no message) and the key legend. The keys every browse view shares
// (tab, ?) are right-aligned so they stay put while the view's own keys sit
// on the left. ? shows the full key list in the preview pane rather than
// growing the footer.
func (m Model) footerView(width int) string {
	m.help.Width = 0 // never let the help component truncate or wrap
	var left, right []key.Binding
	switch m.inputKind {
	case inputAddRoot:
		left = []key.Binding{bind("enter", "save"), bind("esc", "cancel")}
	case inputNewWork:
		left = []key.Binding{bind("enter", "create"), bind("tab", "agent:"+m.agentChoice())}
		// The model cycle only appears for agents that offer one (claude, pi).
		if len(m.modelChoices) > 0 {
			left = append(left, bind("S-tab", "model:"+m.modelChoice()))
		}
		left = append(left, bind("^s", "sidebar:"+onOff(m.sidebarChoice)), bind("esc", "cancel"))
	case inputSelectModel:
		left = []key.Binding{bind("↑↓", "move"), bind("enter", "select"), bind("^d", "set default"), bind("esc", "cancel")}
	default:
		left, right = m.shortHelp(), m.commonHelp()
	}
	l := m.help.ShortHelpView(left)
	legend := l
	if len(right) > 0 {
		r := m.help.ShortHelpView(right)
		gap := width - lipgloss.Width(l) - lipgloss.Width(r)
		if gap < 2 {
			gap = 2
		}
		legend = l + strings.Repeat(" ", gap) + r
	}
	hint := ""
	if m.footerHint != "" {
		hint = hintStyle.Render(trunc(m.footerHint, width))
	}
	return hint + "\n" + legend
}

func bind(k, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(k), key.WithHelp(k, desc))
}

// shortHelp is the current view's own keys, shown on the footer's left.
// Agent/sidebar are new-session settings and appear only in the new-work input,
// never while browsing or jumping to an existing session.
func (m Model) shortHelp() []key.Binding {
	if m.view == viewEntrance && m.scope == scopeSaved {
		return []key.Binding{
			bind("space", "toggle"),
			bind("^r", "restore"),
			bind("↵", "restore one"),
			bind("^s", "save layout"),
			bind("^x", "forget"),
		}
	}
	if m.view == viewEntrance {
		return []key.Binding{bind("↵", "open"), bind("^x", "reap")}
	}
	return []key.Binding{bind("↵", "open"), bind("esc", "back"), bind("^x", "reap")}
}

// commonHelp is the keys shared by every browse view, right-aligned.
func (m Model) commonHelp() []key.Binding {
	if m.view == viewEntrance {
		return []key.Binding{bind("tab", otherScopeLabel(m.scope)), bind("?", "keys")}
	}
	return []key.Binding{bind("?", "keys")}
}

func (m Model) fullHelp() [][]key.Binding {
	saved := m.view == viewEntrance && m.scope == scopeSaved
	nav := []key.Binding{bind("↑↓", "move")}
	if !saved {
		nav = append(nav, bind("↵", "open"))
	}
	nav = append(nav, bind("esc", "back"))
	if m.view == viewEntrance {
		nav = append(nav, bind("tab", otherScopeLabel(m.scope)))
	}
	groups := [][]key.Binding{nav}
	if saved {
		groups = append(groups, m.shortHelp())
	}
	groups = append(groups, []key.Binding{bind("^a", "add root"), bind("^e", "edit roots")})
	last := []key.Binding{bind("^c", "quit")}
	if !saved { // the saved tab's ^x forgets (listed above)
		last = append([]key.Binding{bind("^x", "reap")}, last...)
	}
	return append(groups, last)
}

// keysPane lists every key, one per line, where the preview normally sits.
// It fits maxLines (<= 0: no limit): blank separators between groups go first,
// then the list is cut with an ellipsis, so it never grows the layout.
func (m Model) keysPane(width, maxLines int) string {
	lines := []string{previewHead.Render("keys"), dimStyle.Render(strings.Repeat("─", width))}
	var body []string
	for gi, g := range m.fullHelp() {
		if gi > 0 {
			body = append(body, "")
		}
		for _, k := range g {
			h := k.Help()
			// Truncate the plain text, then style it: cutting a styled string
			// would split its escape codes.
			body = append(body, agentStyle.Render(fmt.Sprintf("%-6s", h.Key))+" "+dimStyle.Render(trunc(h.Desc, width-7)))
		}
	}
	if maxLines > 0 && len(lines)+len(body) > maxLines {
		var dense []string
		for _, l := range body {
			if l != "" {
				dense = append(dense, l)
			}
		}
		body = dense
		if room := maxLines - len(lines); len(body) > room && room > 0 {
			body = append(body[:room-1], dimStyle.Render("…"))
		}
	}
	return strings.Join(append(lines, body...), "\n")
}

// trunc shortens s to at most width display columns, adding an ellipsis.
func trunc(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > width {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// dotColor encodes a session's agent state in its ●: green working, amber
// waiting (bell/attention), dim idle or no detected agent.
func dotColor(state string) lipgloss.Color {
	switch state {
	case "working":
		return colGreen
	case "waiting":
		return colYellow
	default:
		return colSubtext0
	}
}

// otherScopeLabel names the scope tab will switch TO (an action label).
func otherScopeLabel(s entranceScope) string {
	switch nextScope(s) {
	case scopeSessions:
		return "sessions"
	case scopeSaved:
		return "saved"
	default:
		return "folders"
	}
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
