package similar

import "regexp"

var (
	pyLiteralAny = regexp.MustCompile(`"[^"\n]*"|'[^'\n]*'|\b\d+(\.\d+)?\b`)
	goLiteralAny = regexp.MustCompile("\"[^\"\\n]*\"|'[^'\\n]*'|`[^`]*`|\\b\\d+(\\.\\d+)?\\b")
)

// Literals extracts, in order, the string literal contents (quotes
// stripped) and number tokens from body: the same tokenizing rules Norm
// uses to find S/N replacements, after stripping comments/docstrings and
// the signature, so a literal inside a comment, docstring, or signature
// is never returned. Unlike Norm (which drops the whole first line and is
// calibrated that way), only the signature goes: Python's def header up
// to its colon (decorators such as parametrize tables stay), Go's
// `func …{`, and a TS test call's title string (an inline test.each
// table stays).
func Literals(lang, body string) []string {
	var b string
	var re *regexp.Regexp
	switch lang {
	case "python":
		b = pyDocstring.ReplaceAllString(body, "")
		b = pyLineComment.ReplaceAllString(b, "")
		b = dropPySignature(b)
		re = pyLiteralAny
	default: // "go", "typescript", and anything else use the go/ts rules
		b = goBlockComment.ReplaceAllString(body, "")
		b = goLineComment.ReplaceAllString(b, "")
		if lang == "typescript" {
			b = dropTSTitle(b)
		} else {
			b = dropGoSignature(b)
		}
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

var (
	pyDef  = regexp.MustCompile(`(?m)^[ \t]*(async[ \t]+)?def\b`)
	goFunc = regexp.MustCompile(`(?m)^[ \t]*func\b`)
	tsHead = regexp.MustCompile(`^\s*[A-Za-z_$][\w$]*(\s*\.\s*[A-Za-z_$][\w$]*)*`)
)

// skipString returns the index just past the string literal that opens
// at s[i] (quote ", ' or `), honouring backslash escapes; len(s) if it
// never closes.
func skipString(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case q:
			return j + 1
		}
	}
	return len(s)
}

// headerEnd scans s from i and returns the index of the first stop byte
// found outside strings and outside (), [] and {} nesting; -1 if none.
func headerEnd(s string, i int, stop byte) int {
	depth := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '"' || c == '\'' || c == '`':
			i = skipString(s, i)
			continue
		case depth == 0 && c == stop:
			return i
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		}
		i++
	}
	return -1
}

// dropPySignature removes the first `def`/`async def` header through its
// closing colon, keeping decorators above it and the body after it.
func dropPySignature(b string) string {
	loc := pyDef.FindStringIndex(b)
	if loc == nil {
		return b
	}
	end := headerEnd(b, loc[0], ':')
	if end < 0 {
		return b
	}
	return b[:loc[0]] + b[end+1:]
}

// dropGoSignature removes `func …{` (through the body's opening brace).
func dropGoSignature(b string) string {
	loc := goFunc.FindStringIndex(b)
	if loc == nil {
		return b
	}
	end := headerEnd(b, loc[0], '{')
	if end < 0 {
		return b
	}
	return b[:loc[0]] + b[end+1:]
}

// dropTSTitle removes the title string of a test call: the first
// argument of the last call group in `name(.name)*(…)(…)`, when it is a
// string literal. So `test("t", fn)` loses "t" and
// `test.each([...])("t %s", fn)` keeps its table and loses "t %s".
func dropTSTitle(b string) string {
	loc := tsHead.FindStringIndex(b)
	if loc == nil {
		return b
	}
	i, last := loc[1], -1
	for {
		j := i
		for j < len(b) && (b[j] == ' ' || b[j] == '\t' || b[j] == '\n' || b[j] == '\r') {
			j++
		}
		switch {
		case j < len(b) && b[j] == '(':
			end := headerEnd(b, j+1, ')')
			if end < 0 {
				return b
			}
			last, i = j, end+1
			continue
		case j < len(b) && b[j] == '`': // tagged template: test.each`table`
			i = skipString(b, j)
			continue
		}
		break
	}
	if last < 0 {
		return b
	}
	k := last + 1
	for k < len(b) && (b[k] == ' ' || b[k] == '\t' || b[k] == '\n' || b[k] == '\r') {
		k++
	}
	if k >= len(b) || (b[k] != '"' && b[k] != '\'' && b[k] != '`') {
		return b
	}
	return b[:k] + b[skipString(b, k):]
}
