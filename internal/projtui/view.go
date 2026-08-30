package projtui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
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

	selectedStyle = lipgloss.NewStyle().Foreground(colMauve).Bold(true)
	rowStyle      = lipgloss.NewStyle().Foreground(colText)
	dimStyle      = lipgloss.NewStyle().Foreground(colSubtext0)
	agentStyle    = lipgloss.NewStyle().Foreground(colGreen)
	footerStyle   = lipgloss.NewStyle().Foreground(colSubtext0)
	hintStyle     = lipgloss.NewStyle().Foreground(colYellow)
	previewStyle  = lipgloss.NewStyle().Foreground(colText)
	previewHead   = lipgloss.NewStyle().Foreground(colMauve).Bold(true)
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
	for i, r := range vis {
		b.WriteString(m.renderRow(r, i == m.cursor))
		if i < len(vis)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderRow formats a single row: sessions as "● name  agent·state", projects
// as plain names, and the specials with their glyph labels.
func (m Model) renderRow(r Row, selected bool) string {
	prefix := "  "
	if selected {
		prefix = "▸ "
	}
	var line string
	switch r.Kind {
	case RowSession:
		dot := agentStyle.Render("●")
		meta := ""
		if r.Agent != "" {
			meta = "  " + dimStyle.Render(r.Agent)
			if r.State != "" {
				meta += dimStyle.Render("·" + r.State)
			}
		}
		line = dot + " " + r.Label + meta
	case RowProject:
		line = dimStyle.Render("○") + " " + r.Label
	default: // RowNewWork, RowHomeBase
		line = r.Label
	}
	if selected {
		return selectedStyle.Render(prefix + r.Label)
	}
	return rowStyle.Render(prefix) + line
}

// previewPane is a Phase-1 STUB: it shows only the highlighted row's name and
// directory. The rich preview (git status, agent transcript) is Phase 2.
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
	if dir == "" {
		dir = "—"
	}
	var b strings.Builder
	b.WriteString(previewHead.Render("preview") + "\n\n")
	b.WriteString(previewStyle.Render(name) + "\n")
	b.WriteString(dimStyle.Render(dir))
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

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
