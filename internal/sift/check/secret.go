package check

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/schuettc/tackle/internal/sift/row"
)

type secretPattern struct {
	kind string
	re   *regexp.Regexp
}

var secretPatterns = []secretPattern{
	{"GitHub token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{40,})\b`)},
	{"AWS access key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"Slack token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}\b`)},
	{"API key", regexp.MustCompile(`\bsk-(?:[a-z]+-)?[A-Za-z0-9_-]{20,}\b`)},
	{"JWT", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)},
	{"private key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
}

// assignRE is a password, token, secret or API key assigned a literal.
var assignRE = regexp.MustCompile(`(?i)\b(password|passwd|token|secret|api[_-]?key)\b["']?\s*[:=]\s*["']?([^\s"'` + "`" + `,;]+)`)

// literalRE is what a real value looks like: no code, no placeholder.
var literalRE = regexp.MustCompile(`^[A-Za-z0-9+/=_.@!%^&*~-]{8,}$`)
var hasLetter, hasDigit = regexp.MustCompile(`[A-Za-z]`), regexp.MustCompile(`[0-9]`)

// secret flags token, key, JWT and private-key patterns, and passwords,
// tokens and secrets assigned a literal. Shown, never auto-edited; the
// evidence names the kind and a redacted value, never the value.
func secret(_ context.Context, in *Input) []row.Row {
	var rows []row.Row
	for _, f := range in.Files {
		for _, l := range split(f.Content) {
			var ev []row.Fact
			found := map[string]bool{}
			for _, p := range secretPatterns {
				for _, m := range p.re.FindAllString(l.Text, -1) {
					found[m] = true
					ev = append(ev, fact("kind", "%s", p.kind), fact("value", "%s", redact(m)))
				}
			}
			for _, m := range assignRE.FindAllStringSubmatch(l.Text, -1) {
				v := m[2]
				if found[v] || !literalRE.MatchString(v) || !hasLetter.MatchString(v) || !hasDigit.MatchString(v) {
					continue
				}
				ev = append(ev, fact("kind", "%s assignment", strings.ToLower(m[1])), fact("value", "%s", redact(v)))
			}
			if len(ev) == 0 {
				continue
			}
			rows = append(rows, newRow(f, "secret", l.N, l.N, l.Text, "", "looks like a secret", false, ev...))
		}
	}
	return rows
}

// redact keeps the first four characters and the length.
func redact(s string) string {
	if len(s) <= 4 {
		return fmt.Sprintf("… (%d chars)", len(s))
	}
	return fmt.Sprintf("%s… (%d chars)", s[:4], len(s))
}
