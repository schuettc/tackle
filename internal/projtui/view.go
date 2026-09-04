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

func (m Model) View() string {
	iw := m.width - 2 // account for appStyle's 1-col side padding
	if iw < 20 {
		iw = m.width
	}
	listW, previewW, showPreview := m.layout(iw)

	list := lipgloss.NewStyle().Width(listW).Render(m.listPane(listW))
	body := list
	if showPreview {
		// previewFrame adds a 1-col horizontal pad inside its Width, so the text
		// area is previewW-2.
		preview := previewFrame.Width(previewW).Render(m.previewPane(previewW - 2))
		body = lipgloss.JoinHorizontal(lipgloss.Top, list, "  ", preview)
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

func (m Model) titleBar(width int) string {
	label := " proj "
	info := "· folders "
	if m.scope == scopeSessions {
		info = "· sessions "
	}
	if m.view == viewProject {
		info = "· " + m.project + " "
	}
	title := titleLabelStyle.Render(label) + titleInfoStyle.Render(info)
	if width < lipgloss.Width(title) {
		width = lipgloss.Width(title)
	}
	return titleBarStyle.Width(width).Render(title)
}

func (m Model) listPane(width int) string {
	var b strings.Builder

	// Filter / input line.
	if m.inputKind != inputNone {
		label := "new work: "
		if m.inputKind == inputAddRoot {
			label = "add root: "
		}
		b.WriteString(hintStyle.Render(label) + m.input.View() + "\n")
	} else if m.filter != "" {
		b.WriteString(dimStyle.Render("/"+m.filter) + "\n")
	} else {
		b.WriteString(dimStyle.Render("type to filter") + "\n")
	}
	b.WriteString("\n")

	vis := m.visibleRows()
	if len(vis) == 0 {
		b.WriteString(m.emptyMessage())
		return b.String()
	}

	// Scroll a window around the cursor so a long list stays navigable and the
	// cursor is always framed.
	start, end := m.window(len(vis))

	if start > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more", start)) + "\n")
	}
	for i := start; i < end; i++ {
		b.WriteString(m.renderRow(vis[i], i == m.cursor, width))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	if end < len(vis) {
		b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("  ↓ %d more", len(vis)-end)))
	}
	return b.String()
}

// emptyMessage returns the guidance shown when no rows are visible — tuned so a
// first-run user (no roots yet) is told exactly how to proceed.
func (m Model) emptyMessage() string {
	var msg string
	switch {
	case m.filter != "":
		msg = "no matches — esc to clear the filter"
	case m.scope == scopeSessions:
		msg = "no live sessions — tab for folders"
	case len(m.projects) == 0:
		msg = "no projects yet — ^a to add a root, ^e to edit roots"
	default:
		msg = "no folders match"
	}
	return dimStyle.Render("  " + msg)
}

// window returns the [start,end) slice of rows to render, scrolled to keep the
// cursor in view within the terminal height. Falls back to the whole list when
// the height is unknown or the list fits.
func (m Model) window(n int) (start, end int) {
	avail := m.height - 8 // title + blanks + filter line + footer + margins
	if avail < 4 {
		avail = 4
	}
	if m.height <= 0 || n <= avail {
		return 0, n
	}
	start = m.cursor - avail/2
	if start < 0 {
		start = 0
	}
	if start+avail > n {
		start = n - avail
	}
	end = start + avail
	if end > n {
		end = n
	}
	return start, end
}

// plainRow is the row's text with no per-token styling — used to measure the
// bar width and to fill the selected row's highlight bar uniformly.
func plainRow(r Row) string {
	switch r.Kind {
	case RowSession:
		s := "● " + r.Label
		if r.Agent != "" {
			s += "  " + r.Agent
			if r.State != "" {
				s += "·" + r.State
			}
		}
		if r.Unread > 0 {
			s += fmt.Sprintf("  ✉%d", r.Unread)
		}
		if r.ActionRequired > 0 {
			s += " !"
		}
		return s
	case RowProject:
		return "○ " + r.Label
	default:
		return r.Label
	}
}

// renderRow formats a single row. The selected row is a full-width highlight
// bar (mauve left accent + uniform bright text on a surface background);
// unselected rows keep their per-token colors (● name  agent·state  ✉N). Both
// are truncated to width so a long name/path never breaks the layout.
func (m Model) renderRow(r Row, selected bool, width int) string {
	if selected {
		barW := width - 1 // 1 col for the accent edge
		if barW < 1 {
			barW = 1
		}
		bar := selectedStyle.Width(barW).MaxWidth(barW).Render(" " + plainRow(r))
		return accentStyle.Render("▎") + bar
	}
	var line string
	switch r.Kind {
	case RowSession:
		dot := lipgloss.NewStyle().Foreground(dotColor(r.State)).Render("●")
		meta := ""
		if r.Agent != "" {
			meta = "  " + dimStyle.Render(r.Agent)
			if r.State != "" {
				meta += dimStyle.Render("·" + r.State)
			}
		}
		line = dot + " " + r.Label + meta + attentionMarkers(r)
	case RowProject:
		line = dimStyle.Render("○") + " " + r.Label
	default: // RowNewWork
		line = r.Label
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
	vis := m.visibleRows()
	if len(vis) == 0 || m.cursor >= len(vis) {
		return previewStyle.Render("")
	}
	r := vis[m.cursor]
	name := r.Label
	if r.Name != "" {
		name = r.Name
	}
	dir := r.Dir

	var b strings.Builder
	b.WriteString(previewHead.Render("preview") + "\n")
	b.WriteString(dimStyle.Render(strings.Repeat("─", width)) + "\n")
	b.WriteString(previewStyle.Render(trunc(name, width)) + "\n")

	if r.Kind == RowSession {
		if r.Agent != "" {
			state := r.State
			if state == "" {
				state = "—"
			}
			b.WriteString(agentStyle.Render(r.Agent) + dimStyle.Render(" — "+state) + "\n")
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
		b.WriteString(dimStyle.Render("—"))
	}
	return b.String()
}

// footerView renders the key legend via the help component (“?” toggles the full
// legend) plus any transient hint. Descriptions carry live state (scope, agent,
// sidebar) so the footer stays informative.
func (m Model) footerView(width int) string {
	m.help.Width = width
	var legend string
	if m.inputKind == inputAddRoot {
		legend = m.help.ShortHelpView([]key.Binding{
			bind("enter", "save"),
			bind("esc", "cancel"),
		})
	} else if m.inputKind == inputNewWork {
		legend = m.help.ShortHelpView([]key.Binding{
			bind("enter", "create"),
			bind("tab", "agent:"+m.agentChoice()),
			bind("^s", "sidebar:"+onOff(m.sidebarChoice)),
			bind("esc", "cancel"),
		})
	} else if m.help.ShowAll {
		legend = m.help.FullHelpView(m.fullHelp())
	} else {
		legend = m.help.ShortHelpView(m.shortHelp())
	}
	if m.footerHint == "" {
		return legend
	}
	return hintStyle.Render(m.footerHint) + "\n" + legend
}

func bind(k, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(k), key.WithHelp(k, desc))
}

// shortHelp is the compact browse legend; fullHelp (“?”) is the grouped one.
// Agent/sidebar are new-session settings and appear only in the new-work input,
// never while browsing or jumping to an existing session.
func (m Model) shortHelp() []key.Binding {
	if m.view == viewEntrance {
		return []key.Binding{
			bind("↵", "open"),
			bind("tab", otherScopeLabel(m.scope)),
			bind("^x", "reap"),
			bind("?", "keys"),
		}
	}
	return []key.Binding{
		bind("↵", "open"),
		bind("esc", "back"),
		bind("^x", "reap"),
		bind("?", "keys"),
	}
}

func (m Model) fullHelp() [][]key.Binding {
	nav := []key.Binding{
		bind("↑↓", "move"),
		bind("↵", "open"),
		bind("esc", "back"),
	}
	if m.view == viewEntrance {
		nav = append(nav, bind("tab", otherScopeLabel(m.scope)))
	}
	return [][]key.Binding{
		nav,
		{
			bind("^a", "add root"),
			bind("^e", "edit roots"),
		},
		{
			bind("^x", "reap"),
			bind("^c", "quit"),
		},
	}
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
	if s == scopeSessions {
		return "folders"
	}
	return "sessions"
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
