package creel

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/filepicker"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Result is the outcome of a capture session. On success Action is Added or
// Updated; a user cancel yields Cancelled; any failure sets Err. The captured
// value is deliberately absent — it never leaves creel.
type Result struct {
	Action Action
	Name   string
	Dest   string
	Err    error
}

type stage int

// Standalone order: destination (which .env) -> key name -> value. Fixed
// fields (harness mode passes name and dest as args) are skipped.
const (
	stageDest stage = iota
	stageName
	stageValue
	stageConfirm
	stagePicker
)

// RunTUI runs the interactive capture. When nameFixed/destFixed are set the
// corresponding fields are pre-filled and their input stages skipped (harness
// mode); otherwise they are prompted (standalone/keybind mode). destRaw is the
// unresolved destination ("" means default ".env").
func RunTUI(cwd, name, destRaw string, nameFixed, destFixed bool) Result {
	m := newModel(cwd, name, destRaw, nameFixed, destFixed)
	if m.fatal != nil {
		return Result{Err: m.fatal}
	}
	// AltScreen so the program owns the whole popup surface — without it the
	// inline renderer scrolls and the top border gets clipped.
	final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return Result{Err: err}
	}
	return final.(model).result
}

type model struct {
	cwd     string
	input   textinput.Model
	picker  filepicker.Model
	stage   stage
	return_ stage // stage to return to when the picker closes

	name    string
	destRaw string
	dest    string // resolved, once known
	confirm bool   // overwrite already confirmed

	nameFixed bool
	destFixed bool

	width  int
	height int

	errMsg string
	fatal  error
	result Result
}

func newModel(cwd, name, destRaw string, nameFixed, destFixed bool) model {
	m := model{
		cwd:       cwd,
		name:      name,
		destRaw:   destRaw,
		nameFixed: nameFixed,
		destFixed: destFixed,
	}

	if nameFixed && !ValidName(name) {
		m.fatal = fmt.Errorf("invalid env var name: %q", name)
		return m
	}
	if destFixed {
		resolved, err := ResolveDest(cwd, destRaw)
		if err != nil {
			m.fatal = err
			return m
		}
		m.dest = resolved
	}

	ti := textinput.New()
	ti.Prompt = "▸ "
	ti.Width = 46
	ti.Focus()
	m.input = ti

	fp := filepicker.New()
	fp.CurrentDirectory = cwd
	// Pure navigator: neither files nor dirs are "selectable" via enter (enter
	// only descends); the chosen folder is the one you're standing in, taken on
	// Tab. This sidesteps filepicker's select-and-descend ambiguity for dirs.
	fp.DirAllowed = false
	fp.FileAllowed = false
	fp.Height = 8
	m.picker = fp

	m.stage = m.firstStage()
	m.configureInput()
	return m
}

func (m model) firstStage() stage {
	if !m.destFixed {
		return stageDest
	}
	if !m.nameFixed {
		return stageName
	}
	return stageValue
}

// configureInput adapts the single text input to the active stage: masked for
// the value, plain otherwise, with the dest field pre-seeded to its default.
func (m *model) configureInput() {
	m.input.SetValue("")
	m.input.EchoMode = textinput.EchoNormal
	m.input.Placeholder = ""
	switch m.stage {
	case stageName:
		m.input.Placeholder = "OPENAI_API_KEY"
	case stageDest:
		def := m.destRaw
		if def == "" {
			def = ".env"
		}
		m.input.SetValue(def)
		m.input.CursorEnd()
	case stageValue:
		m.input.EchoMode = textinput.EchoPassword
		m.input.EchoCharacter = '•'
		m.input.Placeholder = "paste secret"
	}
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = ws.Width
		m.height = ws.Height
		if ph := m.height - 8; ph > 3 {
			m.picker.SetHeight(ph)
		}
		return m, nil
	}

	key, isKey := msg.(tea.KeyMsg)

	// The folder picker owns its own keys; handle it before the global cancel so
	// esc returns to the dest field rather than quitting the whole capture.
	if m.stage == stagePicker {
		if isKey {
			switch key.String() {
			case "esc", "ctrl+c":
				m.stage = m.return_
				return m, nil
			case "tab", "enter":
				// Enter still descends into the highlighted dir first (handled by
				// the picker); Tab takes the folder we're standing in.
				if key.String() == "tab" {
					base := filepath.Base(strings.TrimSpace(m.input.Value()))
					if base == "" || base == "." || base == string(filepath.Separator) {
						base = ".env"
					}
					m.input.SetValue(filepath.Join(m.picker.CurrentDirectory, base))
					m.input.CursorEnd()
					m.stage = m.return_
					return m, nil
				}
			}
		}
		var cmd tea.Cmd
		m.picker, cmd = m.picker.Update(msg)
		return m, cmd
	}

	if isKey {
		switch key.String() {
		case "ctrl+c", "esc":
			m.result = Result{Action: Cancelled}
			return m, tea.Quit
		}
	}

	if m.stage == stageConfirm {
		if isKey {
			switch strings.ToLower(key.String()) {
			case "y":
				return m.write()
			case "n", "enter":
				m.result = Result{Action: Cancelled}
				return m, tea.Quit
			}
		}
		return m, nil
	}

	// Tab on the dest field opens the folder picker.
	if m.stage == stageDest && isKey && key.String() == "tab" {
		m.return_ = stageDest
		m.stage = stagePicker
		m.picker.CurrentDirectory = m.pickerStart()
		return m, m.picker.Init()
	}

	if isKey && key.String() == "enter" {
		return m.advance()
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// pickerStart is the directory the picker opens in: the directory portion of
// whatever is currently in the dest field (so re-opening resumes there), or cwd.
func (m model) pickerStart() string {
	if resolved, err := resolveInteractive(m.cwd, m.input.Value()); err == nil {
		if dir := filepath.Dir(resolved); dir != "" {
			return dir
		}
	}
	return m.cwd
}

// advance validates the current field and moves to the next stage, performing
// the write once the value is in hand.
func (m model) advance() (tea.Model, tea.Cmd) {
	m.errMsg = ""
	val := strings.TrimRight(m.input.Value(), "\r\n")

	switch m.stage {
	case stageDest:
		resolved, err := resolveInteractive(m.cwd, val)
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.dest = resolved
		m.stage = stageValue
		if !m.nameFixed {
			m.stage = stageName
		}
		m.configureInput()
		return m, nil

	case stageName:
		if !ValidName(val) {
			m.errMsg = "not a valid env var name"
			return m, nil
		}
		m.name = val
		m.stage = stageValue
		m.configureInput()
		return m, nil

	case stageValue:
		if val == "" {
			m.result = Result{Action: Cancelled}
			return m, tea.Quit
		}
		exists, err := HasKey(m.dest, m.name)
		if err != nil {
			m.result = Result{Err: err}
			return m, tea.Quit
		}
		if exists && !m.confirm {
			m.stage = stageConfirm
			return m, nil
		}
		return m.write()
	}
	return m, nil
}

func (m model) write() (tea.Model, tea.Cmd) {
	value := strings.TrimRight(m.input.Value(), "\r\n")
	action, err := Upsert(m.dest, m.name, value)
	if err != nil {
		m.result = Result{Err: err}
		return m, tea.Quit
	}
	// Best-effort: a gitignore failure must not lose an already-written key.
	_ = EnsureGitignored(m.dest)
	m.result = Result{Action: action, Name: m.name, Dest: m.dest}
	return m, tea.Quit
}

var (
	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 2).
			Width(56)
	titleStyle = lipgloss.NewStyle().Bold(true)
	labelStyle = lipgloss.NewStyle().Faint(true)
	fieldStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	hintStyle  = lipgloss.NewStyle().Faint(true)
)

func (m model) View() string {
	if m.stage == stagePicker {
		return m.place(m.pickerView())
	}

	var b strings.Builder

	title := "creel · request_secret"
	if m.name != "" {
		title = "creel · " + m.name
	}
	b.WriteString(titleStyle.Render("🔑 " + title))
	b.WriteString("\n\n")

	if m.name != "" {
		b.WriteString(labelStyle.Render("name  ") + fieldStyle.Render(m.name) + "\n")
	}
	if m.dest != "" {
		b.WriteString(labelStyle.Render("dest  ") + fieldStyle.Render(m.dest) + "\n")
	}
	if m.name != "" || m.dest != "" {
		b.WriteString("\n")
	}

	switch m.stage {
	case stageName:
		b.WriteString(labelStyle.Render("env var name") + "\n" + m.input.View())
	case stageDest:
		b.WriteString(labelStyle.Render("destination") + "\n" + m.input.View() + "\n")
		if resolved, err := resolveInteractive(m.cwd, m.input.Value()); err == nil {
			status := "new file"
			if fileExists(resolved) {
				status = "existing file"
			}
			b.WriteString(hintStyle.Render("→ " + resolved + "  (" + status + ")"))
		}
	case stageValue:
		b.WriteString(labelStyle.Render("paste secret") + "\n" + m.input.View() + "\n")
		n := len([]rune(m.input.Value()))
		line := fmt.Sprintf("%d chars", n)
		if h := Detect(m.input.Value()); h != "" {
			line += "  ·  " + h
		}
		b.WriteString(hintStyle.Render(line))
	case stageConfirm:
		b.WriteString(errStyle.Render(m.name+" already set — overwrite? ") + labelStyle.Render("[y/N]"))
	}

	if m.errMsg != "" {
		b.WriteString("\n" + errStyle.Render("✗ "+m.errMsg))
	}

	footer := "enter submit · esc cancel · value never leaves this popup"
	if m.stage == stageDest {
		footer = "enter submit · tab pick folder · esc cancel"
	}
	b.WriteString("\n\n" + hintStyle.Render(footer))

	_ = okStyle
	return m.place(boxStyle.Render(b.String()))
}

func (m model) pickerView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("📁 pick a folder") + "\n")
	b.WriteString(hintStyle.Render(m.picker.CurrentDirectory) + "\n\n")
	b.WriteString(m.picker.View())
	b.WriteString("\n" + hintStyle.Render("↑/↓ move · enter open · tab use this folder · esc back"))
	return boxStyle.Render(b.String())
}

func (m model) place(box string) string {
	if m.width == 0 || m.height == 0 {
		return box
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
