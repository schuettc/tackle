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
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/schuettc/tackle/internal/proj"
)

// refreshInterval is how often the live-refresh tick re-runs discovery. A var
// so tests can shorten it.
var refreshInterval = 1500 * time.Millisecond

// tickMsg is delivered by the live-refresh tick command.
type tickMsg struct{}

// refreshedMsg carries a background discovery's results back to Update.
// Discovery (every server's sessions, agents and muster counts; the saved
// record and its transcripts) takes around a second, so it never runs inside
// Update: a key pressed meanwhile is handled at once. fromTick says whether
// the live-refresh tick started it, and so whether applying it re-arms the
// tick (an action-started refresh must not start a second tick loop).
type refreshedMsg struct {
	live               bool
	sessions, projects []Row
	saved              bool
	savedRows          []Row
	savedErr           error
	fromTick           bool
}

// primedMsg reports that the launch-time prime (hook + snapshot every live
// server) has finished, so the saved rows are worth reloading.
type primedMsg struct{}

// rootsEditedMsg is delivered when the external $EDITOR (^e) exits.
type rootsEditedMsg struct{ err error }

// inputKind selects what the inline text input is capturing.
type inputKind int

const (
	inputNone        inputKind = iota // no input active
	inputNewWork                      // naming a new work session
	inputAddRoot                      // typing a path to add as a root (^a)
	inputSelectModel                  // choosing a model from a filterable list
)

// viewState selects which of the two views is on screen.
type viewState int

const (
	viewEntrance viewState = iota
	viewProject
)

// entranceScope selects what the entrance view lists: folders (projects, the
// default launcher view), live sessions, or saved sessions (the restore
// view). tab cycles through them at the entrance.
type entranceScope int

const (
	scopeFolders entranceScope = iota
	scopeSessions
	scopeSaved
)

// RowKind tags a row so the renderer and enter-handler know how to treat it.
type RowKind int

const (
	RowSession RowKind = iota // a live tmux session
	RowProject                // a project with no live session (entrance only)
	RowNewWork                // synthetic "+ new work…" (project view, TOP)
	RowSaved                  // a remembered session (saved scope)
	RowHeader                 // a saved-scope window group label (not selectable)
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

	// Saved-scope fields (RowSaved only). Window is the saved Ghostty window
	// (0 = unplaced); visibleRows turns changes of it into header rows.
	Window       int
	Conversation string
	Running      bool
	Attached     bool
	Transcript   bool
	Checked      bool
}

// Result is what the user chose. Kind is "" (cancel), "jump", "new", or
// "restore" (Names; Name set too means jump to it instead of rebuilding
// Ghostty windows).
//   - jump: Socket+Name identify the session to attach/switch to.
//   - new:  Project+Work (Work=="" means the home session, name==project) plus
//     the chosen Agent and Sidebar; the caller runs EnsureSession then Goto.
type Result struct {
	Kind    string
	Names   []string // restore: the saved sessions to bring back, in saved order
	Project string
	Work    string
	Agent   string
	Model   string
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

	agentChoices []string
	agentIndex   int
	modelChoices []string
	modelIndex   int
	// modelFilter/modelCursor drive the inputSelectModel overlay — a filterable
	// list of the current agent's models, opened with Shift-Tab while naming
	// new work. They are transient: reset each time the overlay opens.
	modelFilter   string
	modelCursor   int
	sidebarChoice bool
	scope         entranceScope

	// models resolves an agent to its model menu (index 0 = default). It
	// defaults to proj.ModelsForAgent closed over the loaded config; tests
	// inject a fixture so they never shell out to `pi --list-models`.
	models func(agent string) []string

	// defaultModel is the config's global default_model id, used to preselect
	// the picker on open and to mark the default row in the overlay. ^d in the
	// overlay updates it (live) and persists via saveDefault.
	defaultModel string
	// saveDefault persists a new global default_model to config.toml; defaults
	// to proj.SaveDefaultModel, injected in tests so they never touch real disk.
	saveDefault func(model string) error

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

	// saved is the saved scope's rows, checked the restore selection by name
	// (present = seen; false = the operator unchecked it). loadSaved, forget
	// and saveLayout are seams: newModel leaves them inert so tests never read
	// or write the real record; New/NewFor wire the real ones.
	saved       []Row
	savedLoaded bool
	checked     map[string]bool
	loadSaved   func() ([]Row, error)
	forget      func(name string) error
	saveLayout  func() (windows, tabs int, err error)

	// prime runs once in the background at launch (proj.PrimeAll in the real
	// picker, nil in tests). refreshing is true while a discovery is in
	// flight; at most one runs at a time.
	prime      func()
	refreshing bool

	// offset is the main list's scroll position and modelOffset the model
	// overlay's; each moves only when its cursor crosses an edge.
	// sessionsLoaded is false until the first background scan lands.
	offset         int
	modelOffset    int
	sessionsLoaded bool

	Result Result
}

// newModel builds an entrance model from pre-split session and project rows,
// seeding the agent cycle from the config default and the sidebar toggle. The
// models resolver maps an agent to its model menu; a nil resolver means no
// agent offers models.
func newModel(sessions, projects []Row, defaultAgent string, sidebar bool, models func(string) []string) Model {
	if models == nil {
		models = func(string) []string { return nil }
	}
	m := Model{
		sessions:       sessions,
		projects:       projects,
		agentChoices:   agentChoicesFrom(defaultAgent),
		sidebarChoice:  sidebar,
		models:         models,
		refresh:        defaultRefresh,
		kill:           proj.KillSession,
		saveDefault:    proj.SaveDefaultModel,
		checked:        map[string]bool{},
		sessionsLoaded: true,
		forget:         func(string) error { return nil },
		help:           newHelp(),
	}
	m = m.reseedModels()
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

// liveSessions scans every proj server; a seam so tests never touch tmux.
var liveSessions = proj.LiveSessions

// defaultRefresh re-runs discovery via the real proj package and converts the
// results into rows. On a roots load error it returns no rows.
func defaultRefresh() (sessions, projects []Row) {
	roots, err := proj.LoadRoots()
	if err != nil {
		return nil, nil
	}
	return buildRows(roots, liveSessions())
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

	// Folders come from the roots file alone, so the first frame draws at
	// once; the tmux scan (about a second across many servers) runs in the
	// background from Init and fills in the sessions.
	sessions, projects := buildRows(roots, nil)
	m := newModel(sessions, projects, cfg.DefaultAgent, cfg.Sidebar, modelsResolver(cfg)).withRecord()
	m.sessionsLoaded = false
	m.defaultModel = cfg.DefaultModel
	if cfg.DefaultModel != "" {
		m = m.selectModel(cfg.DefaultModel)
	}
	return m, nil
}

// modelsResolver closes ModelsForAgent over cfg so the picker can look up any
// agent's model menu on demand.
func modelsResolver(cfg proj.Config) func(string) []string {
	return func(agent string) []string { return proj.ModelsForAgent(cfg, agent) }
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

	// Folders come from the roots file alone, so the first frame draws at
	// once; the tmux scan (about a second across many servers) runs in the
	// background from Init and fills in the sessions.
	sessions, projects := buildRows(roots, nil)
	m := newModel(sessions, projects, cfg.DefaultAgent, cfg.Sidebar, modelsResolver(cfg)).withRecord()
	m.sessionsLoaded = false
	m.defaultModel = cfg.DefaultModel
	if agent != "" {
		m = m.selectAgent(agent)
	}
	// A per-project pin wins; otherwise fall back to the global default.
	if mdl := cfg.ModelFor(project); mdl != "" {
		m = m.selectModel(mdl)
	} else if cfg.DefaultModel != "" {
		m = m.selectModel(cfg.DefaultModel)
	}
	if project != "" {
		m = m.drillInto(project)
	}
	return m, nil
}

// selectAgent moves the agent cycle to agent when it is one of the choices and
// reseeds the model menu for it; otherwise the current selection is unchanged.
func (m Model) selectAgent(agent string) Model {
	for i, a := range m.agentChoices {
		if a == agent {
			m.agentIndex = i
			return m.reseedModels()
		}
	}
	return m
}

// reseedModels replaces the model menu with the current agent's models and
// resets the selection to that agent's default (index 0). Called whenever the
// agent changes so the model always belongs to the agent it will launch.
func (m Model) reseedModels() Model {
	m.modelChoices = m.models(m.agentChoice())
	m.modelIndex = 0
	return m
}

// selectModel moves the model cycle to model when it is one of the current
// choices; otherwise the selection is left unchanged.
func (m Model) selectModel(model string) Model {
	for i, x := range m.modelChoices {
		if x == model {
			m.modelIndex = i
			break
		}
	}
	return m
}

// tickCmd schedules the next live-refresh tick.
func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

// Init starts the first discovery at once (its result arms the live-refresh
// tick) and, in the real picker, the background prime.
func (m Model) Init() tea.Cmd {
	first := tickCmd()
	if m.refresh != nil || m.loadSaved != nil {
		first = refreshCmd(m.refresh, m.loadSaved, true)
	}
	if m.prime == nil {
		return first
	}
	return tea.Batch(first, primeCmd(m.prime))
}

// primeCmd runs prime off the update loop and reports when it is done.
func primeCmd(prime func()) tea.Cmd {
	return func() tea.Msg {
		prime()
		return primedMsg{}
	}
}

// startRefresh launches a background discovery unless one is already in
// flight (or there is nothing to discover). fromTick marks a refresh started
// by the live-refresh tick, whose result re-arms the tick.
func (m Model) startRefresh(fromTick bool) (Model, tea.Cmd) {
	if m.refreshing || (m.refresh == nil && m.loadSaved == nil) {
		return m, nil
	}
	m.refreshing = true
	return m, refreshCmd(m.refresh, m.loadSaved, fromTick)
}

// refreshCmd runs discovery off the update loop.
func refreshCmd(refresh func() (sessions, projects []Row), load func() ([]Row, error), fromTick bool) tea.Cmd {
	return func() tea.Msg {
		msg := refreshedMsg{fromTick: fromTick}
		if refresh != nil {
			msg.live = true
			msg.sessions, msg.projects = refresh()
		}
		if load != nil {
			msg.saved = true
			msg.savedRows, msg.savedErr = load()
		}
		return msg
	}
}

// applyRefreshed installs a discovery's results, preserving the view.
func (m Model) applyRefreshed(msg refreshedMsg) (tea.Model, tea.Cmd) {
	m.refreshing = false
	if msg.live {
		m.sessions, m.projects = msg.sessions, msg.projects
		m.sessionsLoaded = true
	}
	if msg.saved {
		m = m.applySaved(msg.savedRows, msg.savedErr)
	}
	m = m.rebuildPreserving()
	if msg.fromTick {
		return m, tickCmd()
	}
	return m, nil
}

// rebuildEntrance sets the entrance rows from the active scope (folders by
// default, or live sessions) and resets to the entrance view.
func (m Model) rebuildEntrance() Model {
	m.view = viewEntrance
	m.project = ""
	m.filter = ""
	m.cursor = 0
	m.offset = 0
	switch m.scope {
	case scopeSessions:
		m.rows = append([]Row(nil), m.sessions...)
	case scopeSaved:
		m.rows = append([]Row(nil), m.saved...)
	default:
		m.rows = append([]Row(nil), m.projects...)
	}
	m.clampCursor() // off a leading group header
	return m
}

// drillInto switches to the project view for project: "+ new work…" first, then
// that project's live sessions.
func (m Model) drillInto(project string) Model {
	m.view = viewProject
	m.project = project
	m.filter = ""
	m.cursor = 0
	m.offset = 0
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
// In the saved scope a header row precedes each window group that still has
// a visible row, so a filter never leaves an empty group's label behind.
func (m Model) visibleRows() []Row {
	rows := m.rows
	if m.filter != "" {
		rows = nil
		for _, r := range m.rows {
			if r.Kind == RowNewWork || fuzzyMatch(m.filter, r.Label) {
				rows = append(rows, r)
			}
		}
	}
	if m.view != viewEntrance || m.scope != scopeSaved {
		return rows
	}
	var out []Row
	for i, r := range rows {
		if i == 0 || rows[i-1].Window != r.Window {
			label := "unplaced"
			if r.Window > 0 {
				label = fmt.Sprintf("window %d", r.Window)
			}
			out = append(out, Row{Kind: RowHeader, Label: label, Window: r.Window})
		}
		out = append(out, r)
	}
	return out
}

// move steps the cursor by delta (±1), skipping group headers; at either end
// it stays put.
func (m *Model) move(delta int) {
	vis := m.visibleRows()
	for i := m.cursor + delta; i >= 0 && i < len(vis); i += delta {
		if vis[i].Kind != RowHeader {
			m.cursor = i
			return
		}
	}
}

func (m Model) agentChoice() string {
	if len(m.agentChoices) == 0 {
		return ""
	}
	return m.agentChoices[m.agentIndex%len(m.agentChoices)]
}

func (m Model) modelChoice() string {
	if len(m.modelChoices) == 0 {
		return ""
	}
	return m.modelChoices[m.modelIndex%len(m.modelChoices)]
}

// Update handles msg, then scrolls the list only as far as needed to keep the
// cursor on screen.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	if nm, ok := next.(Model); ok {
		return nm.followCursor(), cmd
	}
	return next, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		return m.tick()

	case refreshedMsg:
		return m.applyRefreshed(msg)

	case primedMsg:
		return m.startRefresh(false)

	case tea.MouseMsg:
		if m.inputKind != inputNone {
			return m, nil
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.move(-1)
		case tea.MouseButtonWheelDown:
			m.move(1)
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
		if m.inputKind == inputSelectModel {
			return m.updateModelSelect(msg)
		}
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
		// While naming new work, tab cycles the agent that will launch into it,
		// reseeding the model menu to the new agent's models.
		if m.inputKind == inputNewWork && len(m.agentChoices) > 0 {
			m.agentIndex = (m.agentIndex + 1) % len(m.agentChoices)
			m = m.reseedModels()
		}
		return m, nil
	case "shift+tab":
		// Shift-tab opens the model list for the current agent (a cycle would not
		// scale to pi's dozens of models). No-op for agents with no models.
		if m.inputKind == inputNewWork && len(m.modelChoices) > 0 {
			m.inputKind = inputSelectModel
			m.modelFilter = ""
			m.modelCursor = m.modelIndex // start on the current choice
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
			Model:   m.modelChoice(),
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

// visibleModels applies the overlay's substring filter to the current agent's
// model menu.
func (m Model) visibleModels() []string {
	if m.modelFilter == "" {
		return m.modelChoices
	}
	var out []string
	for _, x := range m.modelChoices {
		if fuzzyMatch(m.modelFilter, x) {
			out = append(out, x)
		}
	}
	return out
}

// updateModelSelect drives the inputSelectModel overlay: type to filter, arrows
// to move, enter to choose (returns to naming with that model), esc to cancel.
func (m Model) updateModelSelect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "shift+tab":
		m.inputKind = inputNewWork // back to naming, model unchanged
		return m, nil
	case "up", "ctrl+p":
		if m.modelCursor > 0 {
			m.modelCursor--
		}
		return m, nil
	case "down", "ctrl+n":
		if m.modelCursor < len(m.visibleModels())-1 {
			m.modelCursor++
		}
		return m, nil
	case "enter":
		vis := m.visibleModels()
		if len(vis) > 0 && m.modelCursor < len(vis) {
			m = m.selectModel(vis[m.modelCursor])
		}
		m.inputKind = inputNewWork
		return m, nil
	case "ctrl+d":
		// Set the highlighted model as the global default: select it for this
		// launch and persist it to config.toml. The overlay stays open so the
		// confirmation (and the updated ★ marker) is visible.
		vis := m.visibleModels()
		if len(vis) == 0 || m.modelCursor >= len(vis) {
			return m, nil
		}
		id := vis[m.modelCursor]
		m = m.selectModel(id)
		if err := m.saveDefault(id); err != nil {
			m.footerHint = "could not save default: " + err.Error()
			return m, nil
		}
		m.defaultModel = id
		m.footerHint = "default model set: " + id
		return m, nil
	case "backspace":
		if r := []rune(m.modelFilter); len(r) > 0 {
			m.modelFilter = string(r[:len(r)-1])
			m.modelCursor = 0
		}
		return m, nil
	}
	if text := runeText(msg); text != "" {
		m.modelFilter += text
		m.modelCursor = 0
	}
	return m, nil
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
	if m.view == viewEntrance && m.scope == scopeSaved {
		if next, cmd, ok := m.updateSaved(msg); ok {
			return next, cmd
		}
	}
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "tab":
		// tab cycles the entrance scope; agent/sidebar are new-session settings
		// and live in the new-work input, not the browse view.
		if m.view == viewEntrance {
			m.scope = nextScope(m.scope)
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
		m.move(-1)
		return m, nil
	case "down", "ctrl+n":
		m.move(1)
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
	case RowSaved:
		m.Result = Result{Kind: "restore", Names: []string{row.Name}, Name: row.Name, Socket: row.Socket}
		return m, tea.Quit
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

// tick starts a background discovery. While a name is being typed, or while
// another discovery is still in flight, it skips this round and re-arms
// itself; otherwise the discovery's result re-arms it (see applyRefreshed),
// so exactly one tick loop ever runs.
func (m Model) tick() (tea.Model, tea.Cmd) {
	if m.inputKind != inputNone || m.refreshing {
		return m, tickCmd()
	}
	m, cmd := m.startRefresh(true)
	if cmd == nil {
		return m, tickCmd()
	}
	return m, cmd
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

// rebuildPreserving rebuilds the ACTIVE view from the current rows,
// preserving view/filter/project/scroll. The cursor stays on the same row by
// identity; if that row is gone it keeps its position (clamped).
func (m Model) rebuildPreserving() Model {
	view, filter, cursor, project, offset := m.view, m.filter, m.cursor, m.project, m.offset
	selected := ""
	if vis := m.visibleRows(); cursor < len(vis) {
		selected = rowKey(vis[cursor])
	}
	if view == viewProject {
		m = m.drillInto(project)
	} else {
		m = m.rebuildEntrance()
	}
	m.view = view
	m.filter = filter
	m.project = project
	m.offset = offset
	m.cursor = cursor
	if selected != "" {
		for i, r := range m.visibleRows() {
			if rowKey(r) == selected {
				m.cursor = i
				break
			}
		}
	}
	m.clampCursor()
	return m
}

// rowKey identifies a row across refreshes.
func rowKey(r Row) string {
	name := r.Name
	if name == "" {
		name = r.Label
	}
	return fmt.Sprint(int(r.Kind), "\x1f", name)
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
	if row.Kind != RowSession && row.Kind != RowSaved {
		m.reapConfirm = ""
		m.footerHint = "nothing to reap here"
		return m, nil
	}
	// A saved session that is not running has nothing to kill: ^x forgets it.
	live := row.Kind == RowSession || row.Running
	if live && row.Name == proj.CurrentSessionName() {
		m.reapConfirm = ""
		m.footerHint = "can't reap the session you're in"
		return m, nil
	}
	verb := "reap"
	if !live {
		verb = "forget"
	}
	if m.reapConfirm != row.Name {
		m.reapConfirm = row.Name
		m.footerHint = verb + " " + row.Name + "? ^x to confirm · any key cancels"
		return m, nil
	}
	m.reapConfirm = ""
	if live && m.kill != nil {
		if err := m.kill(row.Socket, row.Name); err != nil {
			m.footerHint = "reap: " + err.Error()
			return m, nil
		}
	}
	// Reaping is the one way a session leaves the saved record (a plain tmux
	// kill or a reboot keeps it restorable).
	if err := m.forget(row.Name); err != nil {
		m.footerHint = "forget: " + err.Error()
		return m, nil
	}
	// Drop the row now and confirm with a background discovery, rather than
	// freezing the picker for a synchronous one.
	m.sessions = withoutRow(m.sessions, row.Name)
	m.saved = withoutRow(m.saved, row.Name)
	delete(m.checked, row.Name)
	m = m.rebuildPreserving()
	m.footerHint = verb + "ed " + row.Name
	if !live {
		m.footerHint = "forgot " + row.Name
	}
	return m.startRefresh(false)
}

// withoutRow returns rows minus any row named name.
func withoutRow(rows []Row, name string) []Row {
	out := rows[:0:0]
	for _, r := range rows {
		if r.Name != name {
			out = append(out, r)
		}
	}
	return out
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
	vis := m.visibleRows()
	n := len(vis)
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	// Never rest on a group header: the next row down, else the next up.
	if n > 0 && vis[m.cursor].Kind == RowHeader {
		for i := m.cursor; i < n; i++ {
			if vis[i].Kind != RowHeader {
				m.cursor = i
				return
			}
		}
		for i := m.cursor; i >= 0; i-- {
			if vis[i].Kind != RowHeader {
				m.cursor = i
				return
			}
		}
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
