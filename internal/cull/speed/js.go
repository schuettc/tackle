package speed

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type comment struct {
	line int
	text string
}

// maskJS blanks comments, string contents, template text and regex bodies
// (keeping offsets and newlines) so patterns only see code. Template
// substitutions (${...}) stay code.
type jsMasker struct {
	src      []byte
	out      []byte
	comments []comment
	line     int
	last     byte // last significant code byte
}

func maskJS(src []byte) ([]byte, []comment) {
	m := &jsMasker{src: src, out: append([]byte(nil), src...), line: 1}
	m.code(0, false)
	return m.out, m.comments
}

func (m *jsMasker) blank(from, to int) {
	for i := from; i < to && i < len(m.out); i++ {
		if m.out[i] != '\n' {
			m.out[i] = ' '
		}
	}
}

// code scans code from i. With untilBrace it returns at the unmatched '}'.
func (m *jsMasker) code(i int, untilBrace bool) int {
	depth := 0
	n := len(m.src)
	for i < n {
		c := m.src[i]
		switch {
		case c == '\n':
			m.line++
			i++
		case c == '/' && i+1 < n && m.src[i+1] == '/':
			j := i
			for j < n && m.src[j] != '\n' {
				j++
			}
			m.comments = append(m.comments, comment{m.line, string(m.src[i:j])})
			m.blank(i, j)
			i = j
		case c == '/' && i+1 < n && m.src[i+1] == '*':
			j := i + 2
			for j+1 < n && (m.src[j] != '*' || m.src[j+1] != '/') {
				j++
			}
			if j+1 >= n {
				j = n
			} else {
				j += 2
			}
			m.comments = append(m.comments, comment{m.line, string(m.src[i:j])})
			m.line += strings.Count(string(m.src[i:j]), "\n")
			m.blank(i, j)
			i = j
		case c == '\'' || c == '"':
			j := i + 1
			for j < n && m.src[j] != c && m.src[j] != '\n' {
				if m.src[j] == '\\' {
					j++
				}
				j++
			}
			if j > n {
				j = n
			}
			m.blank(i+1, j)
			m.last = '"'
			i = j + 1
		case c == '`':
			i = m.template(i + 1)
			m.last = '"'
		case c == '/' && m.regexAllowed():
			j := i + 1
			inClass := false
			for j < n && m.src[j] != '\n' {
				if m.src[j] == '\\' {
					j += 2
					continue
				}
				ch := m.src[j]
				if ch == '/' && !inClass {
					break
				}
				switch ch {
				case '[':
					inClass = true
				case ']':
					inClass = false
				}
				j++
			}
			if j >= n || m.src[j] != '/' {
				i++
				m.last = '/'
				continue
			}
			m.blank(i+1, j)
			m.last = '"'
			i = j + 1
		case c == '{':
			depth++
			m.last = c
			i++
		case c == '}':
			if untilBrace && depth == 0 {
				return i
			}
			depth--
			m.last = c
			i++
		default:
			if c != ' ' && c != '\t' && c != '\r' {
				m.last = c
			}
			i++
		}
	}
	return i
}

func (m *jsMasker) regexAllowed() bool {
	if m.last == 0 {
		return true
	}
	return strings.IndexByte("(,=:[!&|?{};+-*%<>~^", m.last) >= 0
}

// template scans a template literal body from i (after the backtick).
func (m *jsMasker) template(i int) int {
	n := len(m.src)
	start := i
	for i < n {
		c := m.src[i]
		switch {
		case c == '\\':
			i += 2
		case c == '`':
			m.blankKeep(start, i)
			return i + 1
		case c == '$' && i+1 < n && m.src[i+1] == '{':
			m.blankKeep(start, i)
			j := m.code(i+2, true)
			i = j + 1
			start = i
			m.last = '"'
		default:
			if c == '\n' {
				m.line++
			}
			i++
		}
	}
	m.blankKeep(start, n)
	return n
}

func (m *jsMasker) blankKeep(from, to int) { m.blank(from, to) }

var (
	reWaitForTimeout = regexp.MustCompile(`\bwaitForTimeout\s*\(`)
	reSetTimeout     = regexp.MustCompile(`\bsetTimeout\s*\(`)
	reWaitForFn      = regexp.MustCompile(`\bwaitForFunction\s*\(`)
	reScreenshot     = regexp.MustCompile(`\.\s*screenshot\s*\(`)
	reCatch          = regexp.MustCompile(`\.\s*catch\s*\(`)
	reTry            = regexp.MustCompile(`\btry\s*\{`)
	reCatchBlock     = regexp.MustCompile(`^\s*catch\s*(\([^)]*\))?\s*\{`)
	reWaitCall       = regexp.MustCompile(`(?:^|[^\w$])((?:wait|until)(?:[A-Z_][\w$]*)?)\s*\(`)
	reExpectCall     = regexp.MustCompile(`\.\s*(to[A-Z][\w$]*)\s*\(`)
	reWaitName       = regexp.MustCompile(`^(?:wait|until)(?:[A-Z_][\w$]*)?$`)
	reToName         = regexp.MustCompile(`^to[A-Z][\w$]*$`)
	reNumber         = regexp.MustCompile(`^\d[\d_]*(?:\.\d+)?$`)
	reEmptyHandler   = regexp.MustCompile(`^(?:async\s+)?(?:\(\s*[\w$]*\s*\)|[\w$]+)\s*=>\s*(?:\{\s*\}|undefined|null|void\s+0)$`)
	reEmptyFunc      = regexp.MustCompile(`^(?:async\s+)?function\s*[\w$]*\s*\(\s*[\w$]*\s*\)\s*\{\s*\}$`)
)

// callArgs returns the top-level arguments of the call whose '(' is at open.
func callArgs(m string, open int) []string {
	var args []string
	depth := 0
	start := open + 1
	for i := open; i < len(m); i++ {
		switch m[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				last := strings.TrimSpace(m[start:i])
				if last != "" || len(args) > 0 {
					args = append(args, last)
				}
				if last == "" && len(args) > 0 {
					args = args[:len(args)-1]
				}
				return args
			}
		case ',':
			if depth == 1 {
				args = append(args, strings.TrimSpace(m[start:i]))
				start = i + 1
			}
		}
	}
	return args
}

func matchClose(m string, open int, o, c byte) int {
	depth := 0
	for i := open; i < len(m); i++ {
		switch m[i] {
		case o:
			depth++
		case c:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func matchOpenBack(m string, closeIdx int) int {
	depth := 0
	for i := closeIdx; i >= 0; i-- {
		switch m[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// enclosingPromise returns the offset of the '(' of the innermost
// `new Promise(...)` call containing pos, or -1.
func enclosingPromise(m string, pos int) int {
	depth := 0
	for i := pos - 1; i >= 0; i-- {
		switch m[i] {
		case ')':
			depth++
		case '(':
			if depth > 0 {
				depth--
				continue
			}
			if strings.HasSuffix(strings.TrimRight(m[:i], " \t\r\n"), "new Promise") {
				return i
			}
		}
	}
	return -1
}

var (
	reExecParam   = regexp.MustCompile(`^\s*(?:async\s+)?(?:function\s*[\w$]*\s*)?(?:\(\s*([\w$]+)|([\w$]+)\s*=>)`)
	reHelperArrow = regexp.MustCompile(`(?:const|let|var)\s+([\w$]+)\s*=\s*(?:async\s*)?\(?\s*([\w$]+)\s*\)?\s*=>\s*(?:\{\s*return\s+)?new\s+Promise\s*\(\s*\(?\s*([\w$]+)\s*(?:,\s*[\w$]+\s*)?\)?\s*=>\s*setTimeout\s*\(\s*([\w$]+)\s*,\s*([\w$]+)\s*\)`)
	reHelperFunc  = regexp.MustCompile(`function\s+([\w$]+)\s*\(\s*([\w$]+)\s*\)\s*\{\s*return\s+new\s+Promise\s*\(\s*\(?\s*([\w$]+)\s*(?:,\s*[\w$]+\s*)?\)?\s*=>\s*setTimeout\s*\(\s*([\w$]+)\s*,\s*([\w$]+)\s*\)`)
	reResolveOnly = regexp.MustCompile(`^\(\s*\)\s*=>\s*(?:\{\s*)?(\w+)\s*\([^()]*\)\s*;?\s*(?:\})?$`)
)

// sleepHelper is a function whose whole job is to wait: (ms) => new Promise(r => setTimeout(r, ms)).
type sleepHelper struct {
	name       string
	start, end int
}

func findSleepHelpers(m string) []sleepHelper {
	var hs []sleepHelper
	for _, re := range []*regexp.Regexp{reHelperArrow, reHelperFunc} {
		for _, g := range re.FindAllStringSubmatchIndex(m, -1) {
			sub := func(i int) string { return m[g[2*i]:g[2*i+1]] }
			// the timer's callback is the promise's resolve, its delay the helper's parameter
			if sub(3) != sub(4) || sub(2) != sub(5) {
				continue
			}
			hs = append(hs, sleepHelper{name: sub(1), start: g[0], end: g[1]})
		}
	}
	return hs
}

// isWaitCallback reports whether a setTimeout callback only resolves the
// promise: the resolve parameter itself, or an arrow that just calls it.
func isWaitCallback(cb, resolve string) bool {
	if cb == resolve {
		return true
	}
	g := reResolveOnly.FindStringSubmatch(cb)
	return g != nil && g[1] == resolve
}

func isIdent(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// waitLike reports whether the callee name at m[start:end] is a wait.
func waitLike(m, name string, start int) bool {
	if reWaitName.MatchString(name) {
		return true
	}
	if reToName.MatchString(name) {
		stmt := strings.LastIndexAny(m[:start], ";{}") + 1
		return strings.Contains(m[stmt:start], "expect")
	}
	return false
}

func scanJS(rel string, src []byte) []Finding {
	masked, comments := maskJS(src)
	m := string(masked)
	starts := lineStarts(src)
	k := keeper{marker: map[int]bool{}}
	for _, c := range comments {
		k.addComment(c.line, c.text)
	}
	mlines := strings.Split(m, "\n")
	k.lineBlank = func(n int) bool { return n >= 1 && n <= len(mlines) && strings.TrimSpace(mlines[n-1]) == "" }

	var out []Finding
	seen := map[string]bool{}
	addAlt := func(kind string, off, alt int, secs *float64, detail string) {
		key := kind + ":" + strconv.Itoa(off)
		if seen[key] {
			return
		}
		seen[key] = true
		line := lineOf(starts, off)
		if k.keeps(line) || k.keeps(lineOf(starts, alt)) {
			return
		}
		out = append(out, Finding{Kind: kind, File: rel, Line: line, Seconds: secs, Detail: detail, offset: off})
	}

	add := func(kind string, off int, secs *float64, detail string) {
		addAlt(kind, off, off, secs, detail)
	}

	for _, loc := range reWaitForTimeout.FindAllStringIndex(m, -1) {
		args := callArgs(m, loc[1]-1)
		var s *float64
		detail := "waitForTimeout"
		if len(args) == 1 && reNumber.MatchString(args[0]) {
			if v, err := strconv.ParseFloat(strings.ReplaceAll(args[0], "_", ""), 64); err == nil {
				s = secs(v / 1000)
				detail = fmt.Sprintf("waitForTimeout(%s) is a fixed wait of %gs", args[0], v/1000)
			}
		}
		add(KindFixedWait, loc[0], s, detail)
	}
	helpers := findSleepHelpers(m)
	inHelper := func(off int) bool {
		for _, h := range helpers {
			if off >= h.start && off < h.end {
				return true
			}
		}
		return false
	}
	for _, loc := range reSetTimeout.FindAllStringIndex(m, -1) {
		if inHelper(loc[0]) {
			continue
		}
		open := enclosingPromise(m, loc[0])
		if open < 0 {
			continue
		}
		g := reExecParam.FindStringSubmatch(m[open+1:])
		if g == nil {
			continue
		}
		resolve := g[1] + g[2]
		args := callArgs(m, loc[1]-1)
		if len(args) < 2 || !isWaitCallback(args[0], resolve) {
			continue
		}
		var s *float64
		detail := "setTimeout in a Promise is a fixed wait"
		if reNumber.MatchString(args[1]) {
			if v, err := strconv.ParseFloat(strings.ReplaceAll(args[1], "_", ""), 64); err == nil {
				s = secs(v / 1000)
				detail = fmt.Sprintf("setTimeout(…, %s) in a Promise is a fixed wait of %gs", args[1], v/1000)
			}
		}
		add(KindFixedWait, loc[0], s, detail)
	}
	for _, h := range helpers {
		re := regexp.MustCompile(`(^|[^\w$.])` + regexp.QuoteMeta(h.name) + `\s*\(`)
		for _, g := range re.FindAllStringSubmatchIndex(m, -1) {
			off := g[2] + len(m[g[2]:g[3]])
			if inHelper(off) || strings.HasSuffix(strings.TrimRight(m[:off], " \t"), "function") {
				continue
			}
			args := callArgs(m, g[1]-1)
			var s *float64
			detail := h.name + "() is a fixed wait"
			if len(args) == 1 && reNumber.MatchString(args[0]) {
				if v, err := strconv.ParseFloat(strings.ReplaceAll(args[0], "_", ""), 64); err == nil {
					s = secs(v / 1000)
					detail = fmt.Sprintf("%s(%s) is a fixed wait of %gs", h.name, args[0], v/1000)
				}
			}
			add(KindFixedWait, off, s, detail)
		}
	}
	for _, loc := range reWaitForFn.FindAllStringIndex(m, -1) {
		args := callArgs(m, loc[1]-1)
		if len(args) == 2 && strings.HasPrefix(args[1], "{") && regexp.MustCompile(`\btimeout\b`).MatchString(args[1]) {
			add(KindMisplaced, loc[0], nil,
				"waitForFunction(fn, { timeout }): the second argument is passed to fn, not read as options; use waitForFunction(fn, arg, { timeout })")
		}
	}
	for _, loc := range reScreenshot.FindAllStringIndex(m, -1) {
		add(KindScreenshot, loc[0], nil, "screenshot call")
	}

	// .catch(() => {}) after a wait.
	for _, loc := range reCatch.FindAllStringIndex(m, -1) {
		args := callArgs(m, loc[1]-1)
		if len(args) != 1 || (!reEmptyHandler.MatchString(args[0]) && !reEmptyFunc.MatchString(args[0])) {
			continue
		}
		q := strings.LastIndexAny(m[:loc[0]], ")")
		if q < 0 || strings.TrimSpace(m[q+1:loc[0]]) != "" {
			continue
		}
		o := matchOpenBack(m, q)
		if o < 0 {
			continue
		}
		e := o
		for e > 0 && m[e-1] == ' ' {
			e--
		}
		s := e
		for s > 0 && isIdent(m[s-1]) {
			s--
		}
		name := m[s:e]
		if name != "" && waitLike(m, name, s) {
			addAlt(KindSwallowed, loc[0], s, nil, name+"(...) failure is swallowed by .catch(() => {}): the probe carries on when the wait times out")
		}
	}

	// try { …wait… } catch {}
	for _, loc := range reTry.FindAllStringIndex(m, -1) {
		open := loc[1] - 1
		end := matchClose(m, open, '{', '}')
		if end < 0 {
			continue
		}
		cm := reCatchBlock.FindStringIndex(m[end+1:])
		if cm == nil {
			continue
		}
		cOpen := end + 1 + cm[1] - 1
		cEnd := matchClose(m, cOpen, '{', '}')
		if cEnd < 0 || strings.TrimSpace(m[cOpen+1:cEnd]) != "" {
			continue
		}
		body := m[open+1 : end]
		for _, w := range reWaitCall.FindAllStringSubmatchIndex(body, -1) {
			off := open + 1 + w[2]
			add(KindSwallowed, off, nil, m[off:off+(w[3]-w[2])]+"(...) failure is swallowed by an empty catch: the probe carries on when the wait times out")
		}
		for _, w := range reExpectCall.FindAllStringSubmatchIndex(body, -1) {
			off := open + 1 + w[2]
			name := body[w[2]:w[3]]
			if waitLike(m, name, off) {
				add(KindSwallowed, off, nil, name+"(...) failure is swallowed by an empty catch: the probe carries on when the wait times out")
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].offset < out[j].offset })
	return out
}
