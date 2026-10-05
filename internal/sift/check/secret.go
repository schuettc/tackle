package check

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/schuettc/tackle/internal/sift/discover"
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
// passage and evidence show each value redacted, never the value, and the id
// hashes the original line.
func secret(_ context.Context, in *Input) []row.Row {
	var rows []row.Row
	for _, f := range in.Files {
		for _, l := range split(f.Content) {
			ev, values := secretsIn(l.Text)
			if len(ev) == 0 {
				continue
			}
			rows = append(rows, newRow(f, "secret", l.N, l.N, redacter(values).Replace(l.Text), l.Text, "looks like a secret", false, ev...))
		}
	}
	return rows
}

// secretsIn returns the evidence for each secret in text and the values.
func secretsIn(text string) ([]row.Fact, []string) {
	var ev []row.Fact
	var values []string
	found := map[string]bool{}
	for _, p := range secretPatterns {
		for _, m := range p.re.FindAllString(text, -1) {
			found[m] = true
			values = append(values, m)
			ev = append(ev, fact("kind", "%s", p.kind), fact("value", "%s", redact(m)))
		}
	}
	for _, m := range assignRE.FindAllStringSubmatch(text, -1) {
		v := m[2]
		if found[v] || !literalRE.MatchString(v) || !hasLetter.MatchString(v) || !hasDigit.MatchString(v) {
			continue
		}
		values = append(values, v)
		ev = append(ev, fact("kind", "%s assignment", strings.ToLower(m[1])), fact("value", "%s", redact(v)))
	}
	return ev, values
}

// redacter replaces each value with its redacted form, longest first.
func redacter(values []string) *strings.Replacer {
	vs := append([]string(nil), values...)
	sort.Slice(vs, func(i, j int) bool { return len(vs[i]) > len(vs[j]) })
	var pairs []string
	for _, v := range vs {
		pairs = append(pairs, v, redact(v))
	}
	return strings.NewReplacer(pairs...)
}

// redactRows redacts every secret value in the files' rows: the passage and
// the evidence of each row about a file that holds one, whatever its check.
func redactRows(files []*discover.File, rows []row.Row) {
	byFile := map[string]*strings.Replacer{}
	for _, f := range files {
		var values []string
		for _, l := range split(f.Content) {
			_, vs := secretsIn(l.Text)
			values = append(values, vs...)
		}
		if len(values) > 0 {
			byFile[f.Path] = redacter(values)
		}
	}
	for i := range rows {
		r := byFile[rows[i].Source.File]
		if r == nil {
			continue
		}
		rows[i].Passage = r.Replace(rows[i].Passage)
		ev := make([]row.Fact, len(rows[i].Evidence))
		for j, e := range rows[i].Evidence {
			ev[j] = row.Fact{Name: e.Name, Value: r.Replace(e.Value)}
		}
		rows[i].Evidence = ev
	}
}

// redact keeps the first four characters and the length.
func redact(s string) string {
	if len(s) <= 4 {
		return fmt.Sprintf("… (%d chars)", len(s))
	}
	return fmt.Sprintf("%s… (%d chars)", s[:4], len(s))
}
