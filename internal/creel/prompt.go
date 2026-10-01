package creel

import (
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// ErrPromptCancelled is returned by PromptSecret when the user cancels or
// submits nothing.
var ErrPromptCancelled = errors.New("cancelled")

// PromptSecret runs the same masked single-field prompt creel uses for a
// value and returns what was typed. It writes nothing anywhere: the caller
// decides where the value goes. Used by cull init for its own key file.
func PromptSecret(title string) (string, error) {
	ti := textinput.New()
	ti.Prompt = "▸ "
	ti.Width = 46
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.Placeholder = "paste secret"
	ti.Focus()
	final, err := tea.NewProgram(promptModel{title: title, input: ti}, tea.WithAltScreen()).Run()
	if err != nil {
		return "", err
	}
	m := final.(promptModel)
	if m.cancelled || m.value == "" {
		return "", ErrPromptCancelled
	}
	return m.value, nil
}

type promptModel struct {
	title     string
	input     textinput.Model
	value     string
	cancelled bool
}

func (m promptModel) Init() tea.Cmd { return textinput.Blink }

func (m promptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			m.value = strings.TrimSpace(m.input.Value())
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m promptModel) View() string {
	return boxStyle.Render(titleStyle.Render("🔑 "+m.title) + "\n\n" +
		labelStyle.Render("paste secret") + "\n" + m.input.View() + "\n\n" +
		hintStyle.Render("enter submit · esc cancel · value never leaves this popup"))
}
