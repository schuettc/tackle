package rules

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/casebook/engine"
)

// NoteToken is one placeholder a rule's note may use, and what it stands for.
type NoteToken struct {
	Token   string `json:"token"`
	Meaning string `json:"meaning"`
}

// noteTokens are the placeholders Render expands, in the order the page
// lists them.
var noteTokens = []NoteToken{
	{Token: "{how}", Meaning: "how it landed"},
	{Token: "{tip}", Meaning: "its tip commit"},
	{Token: "{age}", Meaning: "its age"},
	{Token: "{repo}", Meaning: "its repo"},
	{Token: "{title}", Meaning: "its title"},
}

// NoteTokens lists the placeholders a rule's note may use (Render's).
func NoteTokens() []NoteToken { return slices.Clone(noteTokens) }

// Render expands template placeholders in note using the item's field set f
// and the match m. Supported placeholders:
//   - {how}   — human-readable landing reason (m.Reason), e.g. "in main"
//   - {tip}   — tip SHA from the landed tips (f.Tip)
//   - {age}   — item age formatted as <n>h, <n>d or <n>w
//   - {repo}  — item repository ("owner/name")
//   - {title} — item title
//
// Unknown placeholders are left in the output verbatim (e.g. "{unknown}").
func Render(note string, f engine.Fields, m Match) string {
	replacements := map[string]string{
		"{how}":   m.Reason,
		"{tip}":   f.Tip,
		"{age}":   formatDuration(f.Age),
		"{repo}":  f.Repo,
		"{title}": f.Title,
	}

	// Walk through the string and replace known placeholders; unknown ones
	// are left unchanged.
	var b strings.Builder
	s := note
	for {
		start := strings.IndexByte(s, '{')
		if start < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:start])
		s = s[start:]
		end := strings.IndexByte(s, '}')
		if end < 0 {
			// No closing brace; emit the rest as-is.
			b.WriteString(s)
			break
		}
		placeholder := s[:end+1]
		if v, ok := replacements[placeholder]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(placeholder) // unknown — leave literal
		}
		s = s[end+1:]
	}
	return b.String()
}

// formatDuration formats a duration in casebook's compact syntax:
// hours, days or weeks (whichever is largest without a remainder).
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0d"
	}
	h := int(d / time.Hour)
	switch {
	case h%(24*7) == 0:
		return fmt.Sprintf("%dw", h/(24*7))
	case h%24 == 0:
		return fmt.Sprintf("%dd", h/24)
	default:
		return fmt.Sprintf("%dh", h)
	}
}
