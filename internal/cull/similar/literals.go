package similar

import "regexp"

var (
	pyLiteralAny = regexp.MustCompile(`"[^"\n]*"|'[^'\n]*'|\b\d+(\.\d+)?\b`)
	goLiteralAny = regexp.MustCompile("\"[^\"\\n]*\"|'[^'\\n]*'|`[^`]*`|\\b\\d+(\\.\\d+)?\\b")
)

// Literals extracts, in order, the string literal contents (quotes
// stripped) and number tokens from body: the same tokenizing rules Norm
// uses to find S/N replacements, minus the comment/docstring stripping and
// signature-line drop, which happen first here too so a literal inside a
// comment, docstring, or the signature line is never returned.
func Literals(lang, body string) []string {
	var b string
	var re *regexp.Regexp
	switch lang {
	case "python":
		b = pyDocstring.ReplaceAllString(body, "")
		b = pyLineComment.ReplaceAllString(b, "")
		b = dropFirstLine(b)
		re = pyLiteralAny
	default: // "go", "typescript", and anything else use the go/ts rules
		b = goBlockComment.ReplaceAllString(body, "")
		b = goLineComment.ReplaceAllString(b, "")
		b = dropFirstLine(b)
		re = goLiteralAny
	}
	matches := re.FindAllString(b, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, unquote(m))
	}
	return out
}

// unquote strips a leading/trailing quote character (", ', or `) from a
// string literal match; a number match (no quotes) passes through as-is.
func unquote(m string) string {
	if len(m) >= 2 {
		switch m[0] {
		case '"', '\'', '`':
			return m[1 : len(m)-1]
		}
	}
	return m
}

// Distinguishing returns, per body, the literals (deduped, order kept) that
// are not present in every one of bodies: what a reader would need to look
// at to tell that body apart from its siblings. A literal present in every
// body (including one shared by all via an exact duplicate) is dropped from
// every row; two bodies with identical literal sets both get empty rows.
func Distinguishing(lang string, bodies []string) [][]string {
	perBody := make([][]string, len(bodies))
	for i, b := range bodies {
		perBody[i] = Literals(lang, b)
	}
	counts := map[string]int{}
	for _, lits := range perBody {
		seen := map[string]bool{}
		for _, l := range lits {
			if !seen[l] {
				seen[l] = true
				counts[l]++
			}
		}
	}
	n := len(bodies)
	out := make([][]string, len(bodies))
	for i, lits := range perBody {
		var row []string
		seen := map[string]bool{}
		for _, l := range lits {
			if seen[l] {
				continue
			}
			seen[l] = true
			if counts[l] < n {
				row = append(row, l)
			}
		}
		out[i] = row
	}
	return out
}
