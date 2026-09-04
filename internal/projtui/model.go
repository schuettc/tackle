// Package projtui is the Bubble Tea picker for proj: a polished,
// keyboard-driven, two-view selector over live tmux sessions and projects.
//
// The two views:
//   - entrance: live sessions first, then projects that have no live session.
//   - project:  a synthetic "+ new work…" (TOP), then that project's live
//     sessions.
//
// The model never touches tmux directly beyond the proj package; it records the
// user's choice in Result and quits, and the caller (cmd/proj) executes it.
package projtui

import (
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/schuettc/tackle/internal/proj"
)

// refreshInterval is how often the live-refresh tick re-runs discovery.
const refreshInterval = 1500 * time.Millisecond

// tickMsg is delivered by the live-refresh tick command.
type tickMsg struct{}

// rootsEditedMsg is delivered when the external $EDITOR (^e) exits.
type rootsEditedMsg struct{ err error }

// inputKind selects what the inline text input is capturing.
type inputKind int

const (
	inputNone    inputKind = iota // no input active
	inputNewWork                  // naming a new work session
	inputAddRoot                  // typing a path to add as a root (^a)
)

// viewState selects which of the two views is on screen.
type viewState int

const (
	viewEntrance viewState = iota
	viewProject
)

// entranceScope selects what the entrance view lists: folders (projects, the
// default launcher view) or live sessions. Toggled with tab at the entrance.
type entranceScope int

const (
	scopeFolders entranceScope = iota
	scopeSessions
)

// RowKind tags a row so the renderer and enter-handler know how to treat it.
type RowKind int

const (
	RowSession RowKind = iota // a live tmux session
	RowProject                // a project with no live session (entrance only)
	RowNewWork                // synthetic "+ new work…" (project view, TOP)
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
	scope         entranceScope

	inputKind inputKind
	input     textinput.Model

	help help.Model

	footerHint string

	width  int
	height int

	// refresh re-runs discovery and returns fresh session/project rows. It
	// defaults to defaultRefresh (the real proj.* calls); tests inject a
	// fixture.
	refresh func() (sessions, projects []Row)

	// kill terminates a session; defaults to proj.KillSession, injected in
	// tests. reapConfirm holds the highlighted session name awaiting a second
	// ^x to confirm the reap.
	kill        func(socket, name string) error
	reapConfirm string

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
		kill:          proj.KillSession,
		help:          newHelp(),
	}
	return m.rebuildEntrance()
}

// buildRows converts live sessions and project dirs into session/project rows.
// Session rows are grouped under their project (name before the first '/').
// EVERY project (including one that already has a live session) gets a project
// row too, so you can always drill into a project's view — jump to a live
// session up top, or select the project itself to reach its new
// work. Duplicate dir basenames across roots are de-duped.
func buildRows(roots proj.Roots, live []proj.Session) (sessions, projects []Row) {
	for _, s := range live {
		sessions = append(sessions, Row{
			Kind:           RowSession,
			Label:          s.Name,
			Socket:         s.Socket,
			Name:           s.Name,
			Dir:            s.Dir,
			Agent:          s.Agent,
			State:          s.State,
			Project:        projectOf(s.Name),
			Unread:         s.Unread,
			ActionRequired: s.ActionRequired,
		})
	}

	seen := map[string]bool{}
	for _, dir := range roots.AllProjectDirs() {
		name := baseName(dir)
		if name == "" || seen[name] {
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

// rebuildEntrance sets the entrance rows from the active scope (folders by
// default, or live sessions) and resets to the entrance view.
func (m Model) rebuildEntrance() Model {
	m.view = viewEntrance
	m.project = ""
	m.filter = ""
	m.cursor = 0
	if m.scope == scopeSessions {
		m.rows = append([]Row(nil), m.sessions...)
	} else {
		m.rows = append([]Row(nil), m.projects...)
	}
	return m
}

// drillInto switches to the project view for project: "+ new work…" first, then
// that project's live sessions.
func (m Model) drillInto(project string) Model {
	m.view = viewProject
	m.project = project
	m.filter = ""
	m.cursor = 0
	rows := []Row{
		{Kind: RowNewWork, Label: "+ new work…", Project: project},
	}
	for _, s := range m.sessions {
		if s.Project == project {
			rows = append(rows, s)
		}
	}
	m.rows = rows
	return m
}

// visibleRows applies the fuzzy substring filter. The synthetic specials
// (new work) are always visible so "+ new work…" stays at the top.
func (m Model) visibleRows() []Row {
	if m.filter == "" {
		return m.rows
	}
	var out []Row
	for _, r := range m.rows {
		if r.Kind == RowNewWork {
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

	case tea.MouseMsg:
		if m.inputKind != inputNone {
			return m, nil
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.cursor > 0 {
				m.cursor--
			}
		case tea.MouseButtonWheelDown:
			if m.cursor < len(m.visibleRows())-1 {
				m.cursor++
			}
		}
		return m, nil

	case rootsEditedMsg:
		if msg.err != nil {
			m.footerHint = "edit roots: " + msg.err.Error()
		} else {
			m = m.reloadRoots()
		}
		return m, nil

	case tea.KeyMsg:
		if m.inputKind != inputNone {
			return m.updateInput(msg)
		}
		return m.updateList(msg)
	}
	return m, nil
}

// updateInput drives the inline text entry for new work ("+ new work…") and
// for adding a root (^a).
func (m Model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.inputKind = inputNone
		m.input.Blur()
		m.footerHint = ""
		return m, nil
	case "tab":
		// While naming new work, tab cycles the agent that will launch into it.
		if m.inputKind == inputNewWork && len(m.agentChoices) > 0 {
			m.agentIndex = (m.agentIndex + 1) % len(m.agentChoices)
		}
		return m, nil
	case "ctrl+s":
		// While naming new work, ^s toggles whether the sidebar is built.
		if m.inputKind == inputNewWork {
			m.sidebarChoice = !m.sidebarChoice
		}
		return m, nil
	case "enter":
		if m.inputKind == inputAddRoot {
			return m.submitAddRoot()
		}
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
	// A pending reap is confirmed only by a second ^x; any other key cancels it
	// and is consumed, so the cancelling key never also triggers its normal
	// action (e.g. esc dropping out of the picker).
	if m.reapConfirm != "" && msg.String() != "ctrl+x" {
		m.reapConfirm = ""
		m.footerHint = ""
		return m, nil
	}
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "tab":
		// tab toggles the entrance scope; agent/sidebar are new-session settings
		// and live in the new-work input, not the browse view.
		if m.view == viewEntrance {
			if m.scope == scopeFolders {
				m.scope = scopeSessions
			} else {
				m.scope = scopeFolders
			}
			return m.rebuildEntrance(), nil
		}
		return m, nil
	case "ctrl+a":
		ti := textinput.New()
		ti.Placeholder = "path whose children are projects (~ ok)"
		ti.Prompt = "› "
		ti.Focus()
		m.input = ti
		m.inputKind = inputAddRoot
		m.footerHint = ""
		return m, textinput.Blink
	case "ctrl+x":
		return m.reap()
	case "?":
		m.help.ShowAll = !m.help.ShowAll
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
		return m, editRootsCmd()
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

	// Every printable rune goes to the filter; commands live on ctrl+ chords
	// (above) so a search term can start with any letter, including s/a/x/q.
	if text := runeText(msg); text != "" {
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
		m.inputKind = inputNewWork
		m.footerHint = ""
		return m, textinput.Blink
	}
	return m, nil
}

// tick re-runs discovery and rebuilds the session/project rows, preserving the
// current view, filter and (clamped) cursor. While a name is being typed the
// refresh is skipped so the input is not disturbed; the tick is always re-armed.
func (m Model) tick() (tea.Model, tea.Cmd) {
	if m.inputKind != inputNone || m.refresh == nil {
		return m, tickCmd()
	}
	return m.refreshRows(), tickCmd()
}

// submitAddRoot validates the typed path, appends it to the roots file, and
// reloads the entrance so the new projects appear immediately.
func (m Model) submitAddRoot() (tea.Model, tea.Cmd) {
	path := strings.TrimSpace(m.input.Value())
	if err := proj.AddRoot(path); err != nil {
		m.footerHint = "add root: " + err.Error()
		return m, nil
	}
	m.inputKind = inputNone
	m.input.Blur()
	m = m.reloadRoots()
	m.footerHint = "added root: " + path
	return m, nil
}

// reloadRoots re-runs discovery and returns to the entrance view.
func (m Model) reloadRoots() Model {
	if m.refresh != nil {
		m.sessions, m.projects = m.refresh()
	}
	return m.rebuildEntrance()
}

// refreshRows re-runs discovery and rebuilds the ACTIVE view, preserving
// view/filter/project and clamping the cursor.
func (m Model) refreshRows() Model {
	if m.refresh == nil {
		return m
	}
	m.sessions, m.projects = m.refresh()
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
	return m
}

// reap kills the highlighted session. The first ^x arms a confirmation; a
// second ^x on the same session carries it out. It refuses non-session rows and
// the session hosting the picker.
func (m Model) reap() (tea.Model, tea.Cmd) {
	vis := m.visibleRows()
	if len(vis) == 0 || m.cursor >= len(vis) {
		return m, nil
	}
	row := vis[m.cursor]
	if row.Kind != RowSession {
		m.reapConfirm = ""
		m.footerHint = "nothing to reap here"
		return m, nil
	}
	if row.Name == proj.CurrentSessionName() {
		m.reapConfirm = ""
		m.footerHint = "can't reap the session you're in"
		return m, nil
	}
	if m.reapConfirm != row.Name {
		m.reapConfirm = row.Name
		m.footerHint = "reap " + row.Name + "? ^x to confirm · any key cancels"
		return m, nil
	}
	m.reapConfirm = ""
	if m.kill != nil {
		if err := m.kill(row.Socket, row.Name); err != nil {
			m.footerHint = "reap: " + err.Error()
			return m, nil
		}
	}
	m = m.refreshRows()
	m.footerHint = "reaped " + row.Name
	return m, nil
}

// editRootsCmd suspends the TUI, opens the roots file in $EDITOR (then $VISUAL,
// then vi), and reports completion via rootsEditedMsg.
func editRootsCmd() tea.Cmd {
	path, err := proj.EnsureRootsFile()
	if err != nil {
		return func() tea.Msg { return rootsEditedMsg{err} }
	}
	fields := editorFields()
	c := exec.Command(fields[0], append(fields[1:], path)...)
	return tea.ExecProcess(c, func(err error) tea.Msg { return rootsEditedMsg{err} })
}

// editorFields splits $EDITOR/$VISUAL into command + args, defaulting to vi.
func editorFields() []string {
	for _, env := range []string{"EDITOR", "VISUAL"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return strings.Fields(v)
		}
	}
	return []string{"vi"}
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
