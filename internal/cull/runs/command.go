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
	redirAlone = regexp.MustCompile(`^\d*(>>?|<{1,3}-?|&>)$`)
	redirWith  = regexp.MustCompile(`^(\d*(>>?|<)&?\S+|&>\S+)$`)
)

type srcLine struct {
	no   int
	text string
}

// scanShell reports whether s ends inside an open quote, and the delimiters of
// any heredocs (<<EOF, <<-EOF, <<'EOF') it starts.
func scanShell(s string) (open bool, heredocs []string) {
	r := []rune(s)
	var q rune
	for i := 0; i < len(r); i++ {
		c := r[i]
		if q != 0 {
			if q == '"' && c == '\\' {
				i++
			} else if c == q {
				q = 0
			}
			continue
		}
		switch {
		case c == '\\':
			i++
		case c == '\'' || c == '"':
			q = c
		case c == '#' && (i == 0 || strings.ContainsRune(" \t;|&(", r[i-1])):
			return false, heredocs
		case c == '<' && i+1 < len(r) && r[i+1] == '<':
			if i+2 < len(r) && r[i+2] == '<' {
				i += 2
				continue
			}
			j := i + 2
			if j < len(r) && r[j] == '-' {
				j++
			}
			for j < len(r) && (r[j] == ' ' || r[j] == '\t') {
				j++
			}
			var quote rune
			if j < len(r) && (r[j] == '\'' || r[j] == '"') {
				quote = r[j]
				j++
			}
			k := j
			for k < len(r) && !strings.ContainsRune(" \t;&|<>()'\"", r[k]) {
				k++
			}
			if k > j && (quote != 0 || r[j] == '_' || r[j] >= 'A' && r[j] <= 'Z' || r[j] >= 'a' && r[j] <= 'z') {
				heredocs = append(heredocs, string(r[j:k]))
				if quote != 0 && k < len(r) && r[k] == quote {
					k++
				}
				i = k - 1
			} else {
				i++
			}
		}
	}
	return q != 0, heredocs
}

// logicalLines folds backslash-newline pairs and quoted strings that span lines
// into one line, and drops heredoc bodies. Each line carries the physical line
// it starts on.
func logicalLines(text string, first int) []srcLine {
	var out []srcLine
	var pending []string
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r")
		if len(pending) > 0 {
			if strings.TrimSpace(l) == pending[0] {
				pending = pending[1:]
			}
			continue
		}
		start := first + i
		cur := l
		for i+1 < len(lines) {
			if strings.HasSuffix(cur, "\\") {
				i++
				cur = strings.TrimSuffix(cur, "\\") + " " + strings.TrimRight(lines[i], "\r")
				continue
			}
			if open, _ := scanShell(cur); open {
				i++
				cur += "\n" + strings.TrimRight(lines[i], "\r")
				continue
			}
			break
		}
		cur = strings.TrimSuffix(cur, "\\")
		t := strings.TrimSpace(cur)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		_, hd := scanShell(t)
		pending = append(pending, hd...)
		out = append(out, srcLine{start, t})
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

// lineEvent is one step of a line: a command, or a ( or ) that moves the directory.
type lineEvent struct {
	words   []string
	op      string // "(" or ")" for a group event, else ""
	prev    string // operator before the command
	next    string // operator after it
	noSetup bool
}

func (e *engine) runLine(line string, c *ctx) {
	var evs []lineEvent
	var words []string
	prev := ""
	flush := func(next string) {
		if len(words) > 0 {
			evs = append(evs, lineEvent{words: words, prev: prev, next: next})
			words = nil
		}
	}
	for _, t := range tokenize(line) {
		switch t.op {
		case "":
			words = append(words, t.word)
			continue
		case "(", ")":
			flush(t.op)
			evs = append(evs, lineEvent{op: t.op})
		default:
			flush(t.op)
		}
		prev = t.op
	}
	flush("")
	markConditional(evs)
	var stack []string
	for _, ev := range evs {
		switch ev.op {
		case "(":
			stack = append(stack, c.dir)
		case ")":
			if n := len(stack); n > 0 {
				c.dir = stack[n-1]
				stack = stack[:n-1]
			}
		default:
			c.noSetup = ev.noSetup
			e.command(ev.words, c)
			c.noSetup = false
		}
	}
}

// markConditional sets noSetup on the commands of each and-or list that may
// not run: all of a list that has ||, anything in a pipeline or background
// job, and a command after && once an earlier one in the chain is a test.
func markConditional(evs []lineEvent) {
	for i := 0; i < len(evs); {
		j := i
		hasOr := false
		for j < len(evs) && evs[j].op == "" {
			if evs[j].next == "||" || evs[j].prev == "||" {
				hasOr = true
			}
			if evs[j].next == ";" || evs[j].next == "&" || evs[j].next == "(" || evs[j].next == ")" || evs[j].next == "" {
				j++
				break
			}
			j++
		}
		if j == i {
			i++
			continue
		}
		cond := false
		for k := i; k < j; k++ {
			ev := &evs[k]
			ev.noSetup = hasOr || cond || ev.prev == "|" || ev.next == "|" || ev.next == "&"
			if ev.next == "&&" && isTestCommand(ev.words) {
				cond = true
			}
		}
		i = j
	}
}

// normalize drops redirections, leading shell keywords and variable
// assignments, and returns the command words and the assignments.
func normalize(words []string) (rest, prefix []string) {
	words = clean(words)
	for len(words) > 0 {
		w := words[0]
		switch {
		case shellKeywords[w] || controlWords[w]:
			words = words[1:]
		case w == "command" && len(words) > 1 && words[1] != "-v" && words[1] != "-V":
			words = words[1:]
		default:
			goto done
		}
	}
done:
	for len(words) > 0 && envWord.MatchString(words[0]) {
		prefix = append(prefix, words[0])
		words = words[1:]
	}
	return words, prefix
}

// isTestCommand: the command only checks a condition, so what follows &&
// may not run.
func isTestCommand(words []string) bool {
	w, _ := normalize(words)
	if len(w) == 0 {
		return false
	}
	switch path.Base(w[0]) {
	case "[", "[[", "test", "which", "type":
		return true
	case "command":
		return len(w) > 1 && (w[1] == "-v" || w[1] == "-V")
	}
	return false
}

// controlWords are block keywords; they are never commands.
var controlWords = map[string]bool{
	"then": true, "elif": true, "else": true, "fi": true, "do": true, "done": true,
	"for": true, "while": true, "until": true, "case": true, "esac": true, "select": true,
	"function": true, "in": true, "if": true,
}

var shellKeywords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "do": true, "while": true, "until": true,
	"!": true, "{": true, "exec": true,
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
	// Track block structure: commands inside if/for/while/case/function
	// bodies are not setup. The scope's depth carries across lines.
	body := false
	for len(words) > 0 && (shellKeywords[words[0]] || controlWords[words[0]] || words[0] == "command" && len(words) > 1 && words[1] != "-v" && words[1] != "-V") {
		switch words[0] {
		case "if", "while", "until":
			c.setup.enter(1)
		case "for", "select", "case":
			c.setup.enter(1)
			return // the rest is the loop header, not a command
		case "function":
			c.setup.enter(1)
			c.setup.fnOpen(words)
			return
		case "fi", "done", "esac":
			c.setup.enter(-1)
			return
		case "in":
			return
		}
		words = words[1:]
	}
	if c.setup != nil && c.setup.depth > 0 {
		body = true
	}
	if c.setup != nil && c.setup.fn > 0 {
		c.setup.fnBraces(words)
		body = true
	}
	words, prefix := normalize(words)
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
	lc := *c
	lc.env = mergeEnv(c.env, prefix)
	before := e.emitted
	e.classify(words, words, &lc)
	if e.emitted == before && c.setup != nil && !body && !c.noSetup && !c.ignoreFail && !nonSetup[path.Base(words[0])] && !isTestCommand(words) {
		c.setup.steps = append(c.setup.steps, Step{Dir: c.dir, Argv: append([]string(nil), words...), Env: append([]string(nil), lc.env...), From: strings.Join(c.chain, " > ")})
	}
}

// nonSetup are shell builtins and tests that are never a setup step.
var nonSetup = map[string]bool{
	"[": true, "[[": true, "]": true, "]]": true, "}": true, "test": true, ":": true, "true": true, "false": true,
	"echo": true, "printf": true, "set": true, "export": true, "unset": true, "trap": true, "source": true, ".": true,
	"exit": true, "return": true, "read": true, "wait": true, "eval": true, "alias": true, "shopt": true,
	"declare": true, "which": true, "type": true, "command": true, "local": true, "readonly": true, "umask": true, "ulimit": true, "pwd": true,
}

// mergeEnv returns base with extra applied on top (a later VAR replaces an earlier one).
func mergeEnv(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	out := append([]string(nil), base...)
	for _, kv := range extra {
		name, _, _ := strings.Cut(kv, "=")
		replaced := false
		for i, o := range out {
			if n, _, _ := strings.Cut(o, "="); n == name {
				out[i], replaced = kv, true
			}
		}
		if !replaced {
			out = append(out, kv)
		}
	}
	return out
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
	case wrappers[base] != nil:
		e.classify(argv, peel(base, words[1:]), c)
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
		if !targetsExempt(words[1:]) {
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

var testWords = map[string]bool{"test": true, "tests": true, "spec": true, "e2e": true, "check": true, "verify": true, "ci": true}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]+`)

// hasTestWord: some token of s is a test word (test, spec, e2e, check, ...).
func hasTestWord(s string) bool {
	for _, t := range nonAlnum.Split(strings.ToLower(s), -1) {
		if testWords[t] {
			return true
		}
	}
	return false
}

// exempt: s names something that builds, installs or lints, and never a test.
func exempt(s string) bool { return exemptWord.MatchString(s) && !hasTestWord(s) }

// targetsExempt judges each make/task target on its own; no target is the
// default target, which is not exempt.
func targetsExempt(args []string) bool {
	n := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			switch a {
			case "-C", "-f", "-I", "-o", "-W", "--directory", "--file", "--taskfile", "-d", "-t":
				i++
			}
			continue
		}
		if strings.Contains(a, "=") {
			continue
		}
		n++
		if !exempt(a) {
			return false
		}
	}
	return n > 0
}

// wrappers are commands that run another command: name -> flags that take a value.
var wrappers = map[string]map[string]bool{
	"env":       {"-u": true, "--unset": true, "-C": true, "--chdir": true, "-S": true},
	"timeout":   {"-s": true, "--signal": true, "-k": true, "--kill-after": true},
	"cross-env": {},
	"xvfb-run":  {"-e": true, "--error-file": true, "-f": true, "--auth-file": true, "-n": true, "--server-num": true, "-s": true, "--server-args": true, "-w": true, "--wait": true},
	"dotenv":    {"-e": true, "-v": true, "-f": true},
	"c8": {"-r": true, "--reporter": true, "-x": true, "--exclude": true, "-n": true, "--include": true, "--reports-dir": true,
		"--temp-directory": true, "-o": true, "--lines": true, "--functions": true, "--branches": true, "--statements": true, "--config": true},
	"nyc": {"-r": true, "--reporter": true, "-x": true, "--exclude": true, "-n": true, "--include": true, "--report-dir": true,
		"--temp-dir": true, "-t": true, "--lines": true, "--functions": true, "--branches": true, "--statements": true, "--cwd": true, "--nycrc-path": true},
	"nice": {"-n": true, "--adjustment": true},
	"time": {"-f": true, "--format": true, "-o": true, "--output": true},
	"sudo": {"-u": true, "-g": true, "-h": true, "-p": true, "-C": true, "-D": true, "-R": true, "-T": true, "-U": true},
}

// peel drops a wrapper's own flags, variables and arguments, leaving the
// command it runs (the same slice length as words when there is none).
func peel(name string, words []string) []string {
	vf := wrappers[name]
	i := 0
	for i < len(words) {
		w := words[i]
		switch {
		case w == "--":
			i++
			if name == "dotenv" || name == "env" || name == "sudo" {
				return words[i:]
			}
		case strings.HasPrefix(w, "-") && len(w) > 1:
			if vf[w] {
				i++
			}
			i++
		case envWord.MatchString(w) && (name == "env" || name == "cross-env"):
			i++
		default:
			if name == "timeout" {
				return words[i+1:] // the duration
			}
			return words[i:]
		}
	}
	return nil
}

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
	case "rspec", "phpunit", "mocha", "ava", "tap":
		return true
	case "playwright":
		return arg(1) == "test"
	case "cypress":
		return arg(1) == "run"
	case "bundle":
		return arg(1) == "exec" && arg(2) == "rspec"
	}
	return false
}

var shellDashC = regexp.MustCompile(`^-[A-Za-z]*c[A-Za-z]*$`)

// shell: `bash -c "..."` is read; `bash file` is a script.
func (e *engine) shell(argv, words []string, c *ctx) {
	for i := 1; i < len(words); i++ {
		w := words[i]
		if shellDashC.MatchString(w) && i+1 < len(words) {
			e.runText(words[i+1], 1, func(int) string { return last(c.chain) }, ctx{dir: c.dir, chain: c.chain[:len(c.chain)-1], env: c.env, vars: c.vars, active: c.active, setup: c.setup.fork()}, false)
			return
		}
		if w == "-o" || w == "+o" {
			i++
			continue
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
		if strings.HasPrefix(w, "/") || entry == outsideDir {
			e.emit(ctx{dir: outsideDir, chain: c.chain, env: c.env}, "unknown", argv, nil, "")
			return
		}
		if strings.Contains(entry, "{{") {
			e.emit(*c, "script", argv, nil, "")
			return
		}
		e.emit(*c, "script", argv, e.localImports(entry), "")
		return
	}
}
