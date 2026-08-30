// Package projtui is the Bubble Tea picker for proj: a polished,
// keyboard-driven, two-view selector over live tmux sessions and projects.
//
// The two views:
//   - entrance: live sessions first, then projects that have no live session.
//   - project:  a synthetic "+ new work…" (TOP), then "🏠 home base", then that
//     project's live sessions.
//
// The model never touches tmux directly beyond the proj package; it records the
// user's choice in Result and quits, and the caller (cmd/proj) executes it.
package projtui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/schuettc/tackle/internal/proj"
)

// refreshInterval is how often the live-refresh tick re-runs discovery.
const refreshInterval = 1500 * time.Millisecond

// tickMsg is delivered by the live-refresh tick command.
type tickMsg struct{}

// viewState selects which of the two views is on screen.
type viewState int

const (
	viewEntrance viewState = iota
	viewProject
)

// RowKind tags a row so the renderer and enter-handler know how to treat it.
type RowKind int

const (
	RowSession  RowKind = iota // a live tmux session
	RowProject                 // a project with no live session (entrance only)
	RowNewWork                 // synthetic "+ new work…" (project view, TOP)
	RowHomeBase                // synthetic "🏠 home base" (project view)
)

// Row is one selectable line. Sessions carry Socket/Name for jumping; projects
// carry Dir for the preview stub. Project is the owning project's name (used to
// group a session under its project in the project view).
type Row struct {
	Kind           RowKind
	Label          string
	Socket         string
	Name           string
	Dir            string
	Agent          string
	State          string
	Project        string
	Unread         int
	ActionRequired int
}

// Result is what the user chose. Kind is "" (cancel), "jump", or "new".
//   - jump: Socket+Name identify the session to attach/switch to.
//   - new:  Project+Work (Work=="" means the home session, name==project) plus
//     the chosen Agent and Sidebar; the caller runs EnsureSession then Goto.
type Result struct {
	Kind    string
	Project string
	Work    string
	Agent   string
	Sidebar bool
	Socket  string
	Name    string
}

// Model is the Bubble Tea model for the picker. It follows scratch's
// internal/tui model shape: a value type driven by Update, with View pure.
type Model struct {
	view   viewState
	filter string
	cursor int

	// sessions is every live session row, retained so drilling into a project
	// can regroup its sessions without re-querying tmux.
	sessions []Row
	// projects is the entrance project rows (no live session).
	projects []Row
	// rows is the unfiltered row set for the active view.
	rows []Row

	project string

	agentChoices  []string
	agentIndex    int
	sidebarChoice bool

	inputting bool
	input     textinput.Model

	footerHint string

	width  int
	height int

	// refresh re-runs discovery and returns fresh session/project rows. It
	// defaults to defaultRefresh (the real proj.* calls); tests inject a
	// fixture.
	refresh func() (sessions, projects []Row)

	Result Result
}

// newModel builds an entrance model from pre-split session and project rows,
// seeding the agent cycle from the config default and the sidebar toggle.
func newModel(sessions, projects []Row, defaultAgent string, sidebar bool) Model {
	m := Model{
		sessions:      sessions,
		projects:      projects,
		agentChoices:  agentChoicesFrom(defaultAgent),
		sidebarChoice: sidebar,
		refresh:       defaultRefresh,
	}
	return m.rebuildEntrance()
}

// buildRows converts live sessions and project dirs into session/project rows.
// Session rows are grouped under their project (name before the first '/');
// project rows are every project dir minus those that already have a session.
func buildRows(roots proj.Roots, live []proj.Session) (sessions, projects []Row) {
	hasSession := map[string]bool{}
	for _, s := range live {
		project := projectOf(s.Name)
		hasSession[project] = true
		sessions = append(sessions, Row{
			Kind:           RowSession,
			Label:          s.Name,
			Socket:         s.Socket,
			Name:           s.Name,
			Dir:            s.Dir,
			Agent:          s.Agent,
			State:          s.State,
			Project:        project,
			Unread:         s.Unread,
			ActionRequired: s.ActionRequired,
		})
	}

	seen := map[string]bool{}
	for _, dir := range roots.AllProjectDirs() {
		name := baseName(dir)
		if name == "" || seen[name] || hasSession[name] {
			continue
		}
		seen[name] = true
		projects = append(projects, Row{Kind: RowProject, Label: name, Dir: dir, Project: name})
	}
	return sessions, projects
}

// defaultRefresh re-runs discovery via the real proj package and converts the
// results into rows. On a roots load error it returns no rows.
func defaultRefresh() (sessions, projects []Row) {
	roots, err := proj.LoadRoots()
	if err != nil {
		return nil, nil
	}
	return buildRows(roots, proj.LiveSessions())
}

// New loads roots, config and live sessions, builds the entrance rows, and
// returns a ready-to-run model. It surfaces proj.ErrNoRoots so the caller can
// print the zsh-parity guidance.
func New() (Model, error) {
	roots, err := proj.LoadRoots()
	if err != nil {
		return Model{}, err
	}
	cfg := proj.LoadConfig()

	sessions, projects := buildRows(roots, proj.LiveSessions())
	return newModel(sessions, projects, cfg.DefaultAgent, cfg.Sidebar), nil
}

// NewFor is like New but, when project is non-empty, starts drilled into that
// project's view, and, when agent is non-empty, preselects that agent in the
// cycle (used by the auto-join hook and `proj [--claude|--pi|--cursor] <project>`).
func NewFor(project, agent string) (Model, error) {
	roots, err := proj.LoadRoots()
	if err != nil {
		return Model{}, err
	}
	cfg := proj.LoadConfig()

	sessions, projects := buildRows(roots, proj.LiveSessions())
	m := newModel(sessions, projects, cfg.DefaultAgent, cfg.Sidebar)
	if agent != "" {
		m = m.selectAgent(agent)
	}
	if project != "" {
		m = m.drillInto(project)
	}
	return m, nil
}

// selectAgent moves the agent cycle to agent when it is one of the choices;
// otherwise the current selection is left unchanged.
func (m Model) selectAgent(agent string) Model {
	for i, a := range m.agentChoices {
		if a == agent {
			m.agentIndex = i
			break
		}
	}
	return m
}

// tickCmd schedules the next live-refresh tick.
func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) Init() tea.Cmd { return tickCmd() }

// rebuildEntrance sets rows to live sessions followed by projects and resets to
// the entrance view.
func (m Model) rebuildEntrance() Model {
	m.view = viewEntrance
	m.project = ""
	m.filter = ""
	m.cursor = 0
	rows := make([]Row, 0, len(m.sessions)+len(m.projects))
	rows = append(rows, m.sessions...)
	rows = append(rows, m.projects...)
	m.rows = rows
	return m
}

// drillInto switches to the project view for project: "+ new work…" first,
// then "🏠 home base", then that project's live sessions (excluding the home
// session, which "🏠 home base" already represents).
func (m Model) drillInto(project string) Model {
	m.view = viewProject
	m.project = project
	m.filter = ""
	m.cursor = 0
	rows := []Row{
		{Kind: RowNewWork, Label: "+ new work…", Project: project},
		{Kind: RowHomeBase, Label: "🏠 home base", Project: project},
	}
	for _, s := range m.sessions {
		if s.Project == project && s.Name != project {
			rows = append(rows, s)
		}
	}
	m.rows = rows
	return m
}

// visibleRows applies the fuzzy substring filter. The synthetic specials
// (new work / home base) are always visible so "+ new work…" stays at the top.
func (m Model) visibleRows() []Row {
	if m.filter == "" {
		return m.rows
	}
	var out []Row
	for _, r := range m.rows {
		if r.Kind == RowNewWork || r.Kind == RowHomeBase {
			out = append(out, r)
			continue
		}
		if fuzzyMatch(m.filter, r.Label) {
			out = append(out, r)
		}
	}
	return out
}

func (m Model) agentChoice() string {
	if len(m.agentChoices) == 0 {
		return ""
	}
	return m.agentChoices[m.agentIndex%len(m.agentChoices)]
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		return m.tick()

	case tea.KeyMsg:
		if m.inputting {
			return m.updateInput(msg)
		}
		return m.updateList(msg)
	}
	return m, nil
}

// updateInput drives the inline "+ new work…" text entry.
func (m Model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.inputting = false
		m.input.Blur()
		m.footerHint = ""
		return m, nil
	case "enter":
		work := proj.SlugWork(m.input.Value())
		if work == "" || !proj.ValidWork(work) {
			m.footerHint = "invalid work name (use letters, digits, - _)"
			return m, nil
		}
		m.Result = Result{
			Kind:    "new",
			Project: m.project,
			Work:    work,
			Agent:   m.agentChoice(),
			Sidebar: m.sidebarChoice,
			Socket:  proj.SocketFor(m.project),
			Name:    proj.SessionName(m.project, work),
		}
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// updateList drives navigation, filtering and selection in both list views.
func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "tab":
		if len(m.agentChoices) > 0 {
			m.agentIndex = (m.agentIndex + 1) % len(m.agentChoices)
		}
		return m, nil
	case "up", "ctrl+p":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "down", "ctrl+n":
		if m.cursor < len(m.visibleRows())-1 {
			m.cursor++
		}
		return m, nil
	case "enter":
		return m.activate()
	case "ctrl+e":
		m.footerHint = "roots editing lands in a later phase"
		return m, nil
	case "esc", "left":
		if m.view == viewProject {
			return m.rebuildEntrance(), nil
		}
		return m, tea.Quit
	case "backspace":
		if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
			m.clampCursor()
		}
		return m, nil
	}

	// Command keys act only when the filter is empty; once the user starts
	// filtering, every printable rune goes to the filter so words containing
	// s/a/x/q still type through.
	text := runeText(msg)
	if m.filter == "" && len(text) == 1 {
		switch text {
		case "q":
			return m, tea.Quit
		case "s":
			m.sidebarChoice = !m.sidebarChoice
			return m, nil
		case "a":
			m.footerHint = "roots editing lands in a later phase"
			return m, nil
		case "x":
			m.footerHint = "reap lands in a later phase"
			return m, nil
		}
	}
	if text != "" {
		m.filter += text
		m.clampCursor()
	}
	return m, nil
}

// activate acts on the highlighted row (enter).
func (m Model) activate() (tea.Model, tea.Cmd) {
	vis := m.visibleRows()
	if len(vis) == 0 || m.cursor >= len(vis) {
		return m, nil
	}
	row := vis[m.cursor]
	switch row.Kind {
	case RowSession:
		m.Result = Result{Kind: "jump", Socket: row.Socket, Name: row.Name}
		return m, tea.Quit
	case RowProject:
		return m.drillInto(row.Label), nil
	case RowNewWork:
		ti := textinput.New()
		ti.Placeholder = "work name"
		ti.Prompt = "› "
		ti.Focus()
		m.input = ti
		m.inputting = true
		m.footerHint = ""
		return m, textinput.Blink
	case RowHomeBase:
		m.Result = Result{
			Kind:    "new",
			Project: m.project,
			Work:    "",
			Agent:   m.agentChoice(),
			Sidebar: m.sidebarChoice,
			Socket:  proj.SocketFor(m.project),
			Name:    m.project,
		}
		return m, tea.Quit
	}
	return m, nil
}

// tick re-runs discovery and rebuilds the session/project rows, preserving the
// current view, filter and (clamped) cursor. While a name is being typed the
// refresh is skipped so the input is not disturbed; the tick is always re-armed.
func (m Model) tick() (tea.Model, tea.Cmd) {
	if m.inputting || m.refresh == nil {
		return m, tickCmd()
	}

	sessions, projects := m.refresh()
	m.sessions = sessions
	m.projects = projects

	// Rebuild rows for the active view, preserving view/filter/cursor.
	view, filter, cursor, project := m.view, m.filter, m.cursor, m.project
	if view == viewProject {
		m = m.drillInto(project)
	} else {
		m = m.rebuildEntrance()
	}
	m.view = view
	m.filter = filter
	m.project = project
	m.cursor = cursor
	m.clampCursor()
	return m, tickCmd()
}

func (m *Model) clampCursor() {
	n := len(m.visibleRows())
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// agentChoicesFrom returns [configDefault, "claude","pi","cursor","none"] with
// empties and duplicates removed, config default first.
func agentChoicesFrom(def string) []string {
	base := []string{def, "claude", "pi", "cursor", "none"}
	seen := map[string]bool{}
	var out []string
	for _, a := range base {
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out
}

// runeText extracts the printable text of a key message (runes or a space).
func runeText(msg tea.KeyMsg) string {
	switch msg.Type {
	case tea.KeyRunes:
		return string(msg.Runes)
	case tea.KeySpace:
		return " "
	}
	return ""
}

// fuzzyMatch reports whether every rune of pattern appears in s in order
// (case-insensitive), a lightweight fuzzy-substring match.
func fuzzyMatch(pattern, s string) bool {
	p := strings.ToLower(pattern)
	t := strings.ToLower(s)
	i := 0
	for _, c := range t {
		if i < len(p) && rune(p[i]) == c {
			i++
		}
	}
	return i == len(p)
}

func projectOf(name string) string {
	if i := strings.Index(name, "/"); i >= 0 {
		return name[:i]
	}
	return name
}

func baseName(dir string) string {
	dir = strings.TrimRight(dir, "/")
	if i := strings.LastIndex(dir, "/"); i >= 0 {
		return dir[i+1:]
	}
	return dir
}
