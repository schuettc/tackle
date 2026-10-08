package runs

import (
	"path"
	"regexp"
	"strings"
)

type tok struct {
	op   string // "", "&&", "||", ";", "|", "&", "(", ")"
	word string
}

// tokenize splits one shell line into words and control operators. Quotes are
// removed; $(...) and backticks stay inside their word; an unquoted # starts a
// comment.
func tokenize(line string) []tok {
	var out []tok
	var cur strings.Builder
	in := false
	flush := func() {
		if in {
			out = append(out, tok{word: cur.String()})
			cur.Reset()
			in = false
		}
	}
	r := []rune(line)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			flush()
		case c == '#' && !in:
			flush()
			return out
		case c == '\\' && i+1 < len(r):
			cur.WriteRune(r[i+1])
			in = true
			i++
		case c == '\'':
			in = true
			for i++; i < len(r) && r[i] != '\''; i++ {
				cur.WriteRune(r[i])
			}
		case c == '"':
			in = true
			for i++; i < len(r) && r[i] != '"'; i++ {
				if r[i] == '\\' && i+1 < len(r) && strings.ContainsRune("\"\\$`", r[i+1]) {
					i++
				}
				cur.WriteRune(r[i])
			}
		case c == '`':
			in = true
			cur.WriteRune(c)
			for i++; i < len(r) && r[i] != '`'; i++ {
				cur.WriteRune(r[i])
			}
			cur.WriteRune('`')
		case c == '$' && i+1 < len(r) && r[i+1] == '(':
			in = true
			depth := 0
			for ; i < len(r); i++ {
				cur.WriteRune(r[i])
				if r[i] == '(' {
					depth++
				} else if r[i] == ')' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
		case c == '&' && i+1 < len(r) && r[i+1] == '&':
			flush()
			out = append(out, tok{op: "&&"})
			i++
		case c == '|' && i+1 < len(r) && r[i+1] == '|':
			flush()
			out = append(out, tok{op: "||"})
			i++
		case c == '&' && in && cur.Len() > 0 && strings.HasSuffix(cur.String(), ">"), c == '&' && i+1 < len(r) && r[i+1] == '>':
			// a redirect (2>&1, &>file): part of the word
			in = true
			cur.WriteRune(c)
		case c == '&', c == '|', c == ';':
			flush()
			out = append(out, tok{op: string(c)})
		case c == '(' || c == ')':
			flush()
			out = append(out, tok{op: string(c)})
		default:
			in = true
			cur.WriteRune(c)
		}
	}
	flush()
	return out
}

var (
	envWord    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	redirAlone = regexp.MustCompile(`^\d*(>>?|<|&>)$`)
	redirWith  = regexp.MustCompile(`^(\d*(>>?|<)&?\S+|&>\S+)$`)
)

// joinContinuations folds backslash-newline pairs, returning each logical line
// with the (first) physical line it starts on.
type srcLine struct {
	no   int
	text string
}

func logicalLines(text string, first int) []srcLine {
	var out []srcLine
	var cur strings.Builder
	start := 0
	for i, l := range strings.Split(text, "\n") {
		l = strings.TrimRight(l, "\r")
		if cur.Len() == 0 {
			start = first + i
		}
		if strings.HasSuffix(l, "\\") {
			cur.WriteString(strings.TrimSuffix(l, "\\"))
			cur.WriteString(" ")
			continue
		}
		cur.WriteString(l)
		t := strings.TrimSpace(cur.String())
		cur.Reset()
		if t != "" && !strings.HasPrefix(t, "#") {
			out = append(out, srcLine{start, t})
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, srcLine{start, s})
	}
	return out
}

// runText walks shell text line by line. A hop names where each line comes
// from. When persist is true, `cd` carries over to later lines (one shell).
func (e *engine) runText(text string, first int, hop func(line int) string, c ctx, persist bool) {
	base := c.dir
	for _, l := range logicalLines(text, first) {
		lc := c.with(hop(l.no))
		if !persist {
			lc.dir = base
		}
		e.runLine(l.text, &lc)
		if persist {
			c.dir = lc.dir
		}
	}
}

func (e *engine) runLine(line string, c *ctx) {
	var words []string
	var stack []string
	flush := func() {
		if len(words) > 0 {
			e.command(words, c)
			words = nil
		}
	}
	for _, t := range tokenize(line) {
		switch t.op {
		case "":
			words = append(words, t.word)
		case "(":
			flush()
			stack = append(stack, c.dir)
		case ")":
			flush()
			if n := len(stack); n > 0 {
				c.dir = stack[n-1]
				stack = stack[:n-1]
			}
		default:
			flush()
		}
	}
	flush()
}

var shellKeywords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "do": true, "while": true, "until": true,
	"!": true, "{": true, "time": true, "exec": true, "command": true,
}

// clean drops redirections.
func clean(words []string) []string {
	var out []string
	for i := 0; i < len(words); i++ {
		w := words[i]
		if redirAlone.MatchString(w) {
			i++
			continue
		}
		if redirWith.MatchString(w) {
			continue
		}
		out = append(out, w)
	}
	return out
}

func (e *engine) command(words []string, c *ctx) {
	words = clean(words)
	for len(words) > 0 && shellKeywords[words[0]] {
		words = words[1:]
	}
	for len(words) > 0 && envWord.MatchString(words[0]) {
		words = words[1:]
	}
	if len(words) == 0 {
		return
	}
	if words[0] == "cd" {
		switch {
		case len(words) < 2:
			c.dir = "{{unknown}}"
		case strings.ContainsAny(words[1], "$`~") || strings.HasPrefix(words[1], "/") || words[1] == "-" || strings.Contains(words[1], "{{"):
			c.dir = "{{unknown}}"
		default:
			c.dir = join(c.dir, words[1])
		}
		return
	}
	e.classify(words, words, c)
}

var wrapperSkipFlag = regexp.MustCompile(`^-`)

// classify decides what words is. argv is the full command as written; words
// is what is left after peeling wrappers (npx, uv run, ...).
func (e *engine) classify(argv, words []string, c *ctx) {
	if len(words) == 0 {
		return
	}
	base := path.Base(words[0])
	switch {
	case base == "npx" || base == "bunx":
		rest := words[1:]
		for len(rest) > 0 && wrapperSkipFlag.MatchString(rest[0]) {
			rest = rest[1:]
		}
		e.classify(argv, rest, c)
		return
	case (base == "uv" || base == "poetry" || base == "pipenv" || base == "pdm") && len(words) > 2 && words[1] == "run":
		rest := words[2:]
		for len(rest) > 0 && wrapperSkipFlag.MatchString(rest[0]) {
			rest = rest[1:]
		}
		e.classify(argv, rest, c)
		return
	case base == "go":
		if len(words) > 1 && words[1] == "test" {
			e.emit(*c, "go", argv, nil, "")
		}
		return
	case base == "pytest" || base == "py.test":
		e.emit(*c, "pytest", argv, nil, "")
		return
	case strings.HasPrefix(base, "python"):
		for i := 1; i+1 < len(words); i++ {
			if words[i] == "-m" && words[i+1] == "pytest" {
				e.emit(*c, "pytest", argv, nil, "")
				return
			}
		}
		return
	case base == "jest":
		e.emit(*c, "jest", argv, nil, "")
		return
	case base == "vitest":
		e.emit(*c, "vitest", argv, nil, "")
		return
	case base == "node":
		e.node(argv, words, c)
		return
	case base == "just":
		e.callJust(words[1:], c)
		return
	case base == "npm" || base == "pnpm" || base == "yarn":
		e.callPackageManager(argv, base, words[1:], c)
		return
	case base == "bash" || base == "sh" || base == "zsh":
		e.shell(argv, words, c)
		return
	case base == "make" || base == "gmake" || base == "task" || base == "mage":
		if !exempt(strings.Join(words[1:], " ")) {
			e.emit(*c, "unknown", argv, nil, base+" target: what it runs is not read")
		}
		return
	}
	if len(words[0]) > 0 && (strings.HasPrefix(words[0], "./") || strings.HasSuffix(words[0], ".sh") || strings.HasSuffix(words[0], ".bash")) {
		if !exempt(words[0]) {
			e.emit(*c, "unknown", argv, nil, "a script: what it runs is not read")
		}
		return
	}
	if otherRunner(words) {
		e.emit(*c, "unknown", argv, nil, "a test runner cull does not handle")
	}
}

var exemptWord = regexp.MustCompile(`(?i)lock|export|lint|fmt|format|build|deploy|install|setup|release|publish|clean|migrate|docker|package|bump`)

func exempt(s string) bool { return exemptWord.MatchString(s) }

// otherRunner: test commands of other ecosystems, listed as not understood.
func otherRunner(w []string) bool {
	b := path.Base(w[0])
	arg := func(i int) string {
		if i < len(w) {
			return w[i]
		}
		return ""
	}
	switch b {
	case "cargo", "dotnet", "swift", "deno", "bun", "mix", "gradle", "gradlew", "mvn", "mvnw", "sbt":
		for _, a := range w[1:] {
			if a == "test" || a == "verify" || a == "check" {
				return true
			}
		}
	case "rspec", "phpunit", "mocha", "ava", "tap", "playwright", "cypress":
		return true
	case "bundle":
		return arg(1) == "exec" && arg(2) == "rspec"
	}
	return false
}

// shell: `bash -c "..."` is read; `bash file` is a script.
func (e *engine) shell(argv, words []string, c *ctx) {
	for i := 1; i < len(words); i++ {
		w := words[i]
		if w == "-c" && i+1 < len(words) {
			e.runText(words[i+1], 1, func(int) string { return last(c.chain) }, ctx{dir: c.dir, chain: c.chain[:len(c.chain)-1], vars: c.vars, active: c.active}, false)
			return
		}
		if strings.HasPrefix(w, "-") {
			continue
		}
		if !exempt(w) {
			e.emit(*c, "unknown", argv, nil, "a script: what it runs is not read")
		}
		return
	}
}

func last(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[len(s)-1]
}

var nodeValueFlags = map[string]bool{
	"--import": true, "--require": true, "-r": true, "--loader": true, "--experimental-loader": true,
	"--env-file": true, "-e": true, "-p": true, "--eval": true, "--print": true, "--conditions": true, "-C": true,
}

func (e *engine) node(argv, words []string, c *ctx) {
	for i := 1; i < len(words); i++ {
		w := words[i]
		if w == "--test" {
			e.emit(*c, "node-test", argv, nil, "")
			return
		}
	}
	for i := 1; i < len(words); i++ {
		w := words[i]
		if w == "-e" || w == "-p" || w == "--eval" || w == "--print" {
			return
		}
		if nodeValueFlags[w] {
			i++
			continue
		}
		if strings.HasPrefix(w, "-") {
			continue
		}
		if strings.Contains(w, "node_modules/") {
			return // a tool binary run through node, not a project script
		}
		entry := join(c.dir, w)
		e.emit(*c, "script", argv, e.localImports(entry), "")
		return
	}
}
