package projtui

import (
	"fmt"
	"strings"

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

	titleLabelStyle = lipgloss.NewStyle().Background(colSurface1).Foreground(colMauve).Bold(true)
	titleInfoStyle  = lipgloss.NewStyle().Background(colSurface1).Foreground(colText)
	titleBarStyle   = lipgloss.NewStyle().Background(colSurface1)

	// The selected row is a full-width bar (surface background + bright bold
	// text) so the cursor is unmistakable even in a long list.
	selectedStyle = lipgloss.NewStyle().Background(colSurface1).Foreground(colText).Bold(true)
	rowStyle      = lipgloss.NewStyle().Foreground(colText)
	dimStyle      = lipgloss.NewStyle().Foreground(colSubtext0)
	agentStyle    = lipgloss.NewStyle().Foreground(colGreen)
	footerStyle   = lipgloss.NewStyle().Foreground(colSubtext0)
	hintStyle     = lipgloss.NewStyle().Foreground(colYellow)
	previewStyle  = lipgloss.NewStyle().Foreground(colText)
	previewHead   = lipgloss.NewStyle().Foreground(colMauve).Bold(true)
	attnStyle     = lipgloss.NewStyle().Foreground(colYellow).Bold(true)
)

func (m Model) View() string {
	title := m.titleBar()
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.listPane(), "  ", m.previewPane())
	footer := m.footer()
	return lipgloss.JoinVertical(lipgloss.Left, title, "", body, "", footer)
}

func (m Model) titleBar() string {
	label := " proj "
	info := "· all projects "
	if m.view == viewProject {
		info = "· " + m.project + " "
	}
	title := titleLabelStyle.Render(label) + titleInfoStyle.Render(info)
	width := m.width
	if width < lipgloss.Width(title) {
		width = lipgloss.Width(title)
	}
	return titleBarStyle.Width(width).Render(title)
}

func (m Model) listPane() string {
	var b strings.Builder

	// Filter / input line.
	if m.inputting {
		b.WriteString(hintStyle.Render("new work: ") + m.input.View() + "\n")
	} else if m.filter != "" {
		b.WriteString(dimStyle.Render("/"+m.filter) + "\n")
	} else {
		b.WriteString(dimStyle.Render("type to filter") + "\n")
	}
	b.WriteString("\n")

	vis := m.visibleRows()
	if len(vis) == 0 {
		b.WriteString(dimStyle.Render("  (no matches)"))
		return b.String()
	}

	// Scroll a window around the cursor so a long list stays navigable and the
	// cursor is always framed.
	start, end := m.window(len(vis))

	// Width of the highlight bar = widest row in the window (+ prefix), capped.
	barW := 0
	for i := start; i < end; i++ {
		if w := lipgloss.Width(plainRow(vis[i])) + 2; w > barW {
			barW = w
		}
	}

	if start > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more", start)) + "\n")
	}
	for i := start; i < end; i++ {
		b.WriteString(m.renderRow(vis[i], i == m.cursor, barW))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	if end < len(vis) {
		b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("  ↓ %d more", len(vis)-end)))
	}
	return b.String()
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
// bar (uniform bright text on a surface background, prefixed ▸); unselected
// rows keep their per-token colors (● name  agent·state  ✉N).
func (m Model) renderRow(r Row, selected bool, barW int) string {
	if selected {
		return selectedStyle.Width(barW).Render("▸ " + plainRow(r))
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
	default: // RowNewWork, RowHomeBase
		line = r.Label
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
func (m Model) previewPane() string {
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
	b.WriteString(previewHead.Render("preview") + "\n\n")
	b.WriteString(previewStyle.Render(name) + "\n")

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
			b.WriteString(dimStyle.Render(fmt.Sprintf("git %s ↑%d ↓%d ●%d", g.Branch, g.Ahead, g.Behind, g.Dirty)) + "\n")
		}
		b.WriteString(dimStyle.Render(dir))
	} else {
		b.WriteString(dimStyle.Render("—"))
	}
	return b.String()
}

func (m Model) footer() string {
	var parts []string
	if m.inputting {
		parts = append(parts, "enter save", "esc cancel")
	} else {
		parts = append(parts,
			"enter select",
			"esc back",
			"tab agent:"+m.agentChoice(),
			"s sidebar:"+onOff(m.sidebarChoice),
			"a roots",
			"^e edit",
			"x reap",
			"q quit",
		)
	}
	footer := footerStyle.Render(strings.Join(parts, "  ·  "))
	if m.footerHint != "" {
		footer += "\n" + hintStyle.Render(m.footerHint)
	}
	return footer
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

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
