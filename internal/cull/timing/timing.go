// Package timing runs the checks a project runs (what package runs finds),
// with what timing needs added, and reports where the time goes: per check,
// per Go package or Python file, and per test.
package timing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/schuettc/tackle/internal/cull/discover"
	"github.com/schuettc/tackle/internal/cull/extract"
	_ "github.com/schuettc/tackle/internal/cull/extract/golang" // registers the Go extractor
	_ "github.com/schuettc/tackle/internal/cull/extract/python" // registers the Python extractor
	_ "github.com/schuettc/tackle/internal/cull/extract/ts"     // registers the TypeScript extractor
	"github.com/schuettc/tackle/internal/cull/runs"
	"github.com/schuettc/tackle/internal/cull/speed"
	"github.com/schuettc/tackle/internal/cull/verify"
)

// DefaultTimeout is how long one check may run.
const DefaultTimeout = 30 * time.Minute

const (
	slowestKeep    = 20
	goMedianHint   = 0.3 // seconds
	pySetupHint    = 0.5 // share of a file's time
	failTailLines  = 20
	killGraceDelay = 5 * time.Second
)

// Options for Run.
type Options struct {
	Root        string
	Timeout     time.Duration // per check; DefaultTimeout when zero
	TestCommand string        // .cull.toml test_command, used only when the project lists no runnable check
}

// CheckResult is one check's run.
type CheckResult struct {
	Kind     string      `json:"kind"`
	Dir      string      `json:"dir"`
	Argv     []string    `json:"argv"` // as run, with the timing flags
	From     []string    `json:"from,omitempty"`
	Seconds  float64     `json:"seconds"`
	Exit     int         `json:"exit"`
	OK       bool        `json:"ok"`
	TimedOut bool        `json:"timed_out,omitempty"`
	Tail     string      `json:"tail,omitempty"` // last lines, when it failed or timed out
	Summary  string      `json:"summary,omitempty"`
	Packages []GoPackage `json:"packages,omitempty"`
	Files    []PyFile    `json:"files,omitempty"`
}

// Hint is something the numbers point at.
type Hint struct {
	Kind    string `json:"kind"` // go_setenv | python_setup
	Where   string `json:"where"`
	Message string `json:"message"`
}

// Report is the whole timing run.
type Report struct {
	Root         string        `json:"root"`
	TotalSeconds float64       `json:"total_seconds"`
	OK           bool          `json:"ok"`
	Checks       []CheckResult `json:"checks"`
	NotRun       []string      `json:"not_run"`
	Slowest      []TestTime    `json:"slowest_tests"`
	Hints        []Hint        `json:"hints"`
}

// addTimingFlags adds only what timing needs to a check's command.
func addTimingFlags(kind string, argv []string) []string {
	switch kind {
	case "go":
		if len(argv) < 2 || argv[0] != "go" || argv[1] != "test" {
			return argv
		}
		hasJSON, hasCount := false, false
		for _, a := range argv[2:] {
			if a == "-json" || a == "--json" {
				hasJSON = true
			}
			if strings.HasPrefix(a, "-count") || strings.HasPrefix(a, "--count") {
				hasCount = true
			}
		}
		out := append([]string(nil), argv[:2]...)
		if !hasJSON {
			out = append(out, "-json")
		}
		if !hasCount {
			out = append(out, "-count=1")
		}
		return append(out, argv[2:]...)
	case "pytest":
		// Timing flags go before the first "--"; -q/-qq are dropped, since -vv
		// and the summary line are what timing reads.
		cut := len(argv)
		for i, a := range argv {
			if a == "--" {
				cut = i
				break
			}
		}
		out := []string{}
		for _, a := range argv[:cut] {
			if a == "-q" || a == "-qq" || a == "-qqq" || a == "--quiet" {
				continue
			}
			out = append(out, a)
		}
		out = append(out, "--durations=0", "-vv", "-p", "no:cacheprovider")
		return append(out, argv[cut:]...)
	}
	return argv
}

type plan struct {
	kind string
	dir  string // root-relative
	argv []string
	from []string
	env  []string // VAR=value prefixes and workflow env of the check
	raw  bool     // argv is run as given (verify's own commands)
}

var runnable = map[string]bool{"go": true, "pytest": true, "jest": true, "vitest": true, "node-test": true, "script": true}

// Run runs every runnable check, one at a time, and reports.
func Run(ctx context.Context, opt Options) (*Report, error) {
	root, err := filepath.Abs(opt.Root)
	if err != nil {
		return nil, err
	}
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	found, err := runs.Find(root)
	if err != nil {
		return nil, err
	}
	rep := &Report{Root: root, OK: true, Checks: []CheckResult{}, NotRun: []string{}, Slowest: []TestTime{}, Hints: []Hint{}}
	var plans []plan
	for _, c := range found {
		if !runnable[c.Kind] {
			note := c.Note
			if note == "" {
				note = strings.Join(c.Argv, " ")
			}
			rep.NotRun = append(rep.NotRun, "not run: "+note)
			continue
		}
		plans = append(plans, plan{kind: c.Kind, dir: c.Dir, argv: c.Argv, from: c.From, env: c.Env})
	}
	if len(plans) == 0 {
		fb, err := fallback(root, opt.TestCommand)
		if err != nil {
			return nil, err
		}
		plans = fb
	}

	env := verify.FilteredEnv()
	start := time.Now()
	var tests []TestTime
	var goChecks []int
	pyByCheck := map[int]PyResult{}
	for pi, p := range plans {
		if ctx.Err() != nil {
			rep.OK = false
			for _, q := range plans[pi:] {
				rep.NotRun = append(rep.NotRun, "not run: cancelled ("+describe(q)+")")
			}
			break
		}
		argv := addTimingFlags(p.kind, p.argv)
		cenv := checkEnv(env, p.env)
		execArgv, note := argv, ""
		if !p.raw {
			execArgv, note = shellForm(argv, cenv, p.env)
		}
		if note != "" {
			rep.NotRun = append(rep.NotRun, "not run: "+note+" ("+describe(p)+")")
			continue
		}
		res, out := runOne(ctx, filepath.Join(root, filepath.FromSlash(p.dir)), execArgv, timeout, cenv)
		cr := CheckResult{Kind: p.kind, Dir: p.dir, Argv: argv, From: p.from, Seconds: res.seconds,
			Exit: res.exit, OK: res.exit == 0 && !res.timedOut && !res.cancelled, TimedOut: res.timedOut}
		if res.cancelled {
			cr.Tail = "cancelled"
		}
		switch p.kind {
		case "go":
			gr := ParseGo(bytes.NewReader(out))
			var sum float64
			for _, pk := range gr.Packages {
				sum += pk.Seconds
			}
			for i := range gr.Packages {
				if sum > 0 {
					gr.Packages[i].Share = gr.Packages[i].Seconds / sum
				}
				tests = append(tests, gr.Packages[i].leafTests()...)
			}
			cr.Packages = gr.Packages
			goChecks = append(goChecks, len(rep.Checks))
			if !cr.OK {
				cr.Tail = gr.Tail
			}
			cr.Summary = fmt.Sprintf("%d packages", len(gr.Packages))
		case "pytest":
			pr := ParsePytest(bytes.NewReader(out))
			cr.Files = pr.Files
			tests = append(tests, pr.Tests...)
			pyByCheck[len(rep.Checks)] = pr
			cr.Summary = fmt.Sprintf("%d passed, %d failed, %d skipped", pr.Passed, pr.Failed, pr.Skipped)
		}
		if !cr.OK && cr.Tail == "" {
			cr.Tail = tailOf(string(out), failTailLines)
		}
		if !cr.OK {
			rep.OK = false
		}
		rep.Checks = append(rep.Checks, cr)
	}
	rep.TotalSeconds = time.Since(start).Seconds()

	sort.SliceStable(tests, func(i, j int) bool { return tests[i].Seconds > tests[j].Seconds })
	if len(tests) > slowestKeep {
		tests = tests[:slowestKeep]
	}
	if tests != nil {
		rep.Slowest = tests
	}
	for _, i := range goChecks {
		rep.Hints = append(rep.Hints, goHints(root, rep.Checks[i])...)
	}
	idx := make([]int, 0, len(pyByCheck))
	for i := range pyByCheck {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		rep.Hints = append(rep.Hints, pythonHints(pyByCheck[i])...)
	}
	return rep, nil
}

// fallback is the commands verify detects for the project's discovered tests.
func fallback(root, testCommand string) ([]plan, error) {
	files, err := discover.Suite(root, "", nil)
	if err != nil {
		return nil, err
	}
	langs := map[string]string{}
	for _, rel := range files {
		if e := extract.ForFile(rel); e != nil {
			langs[rel] = e.Lang()
		}
	}
	if len(langs) == 0 && testCommand == "" {
		return nil, nil
	}
	cmds, err := verify.Plan(root, testCommand, langs)
	if err != nil {
		return nil, err
	}
	var out []plan
	for _, c := range cmds {
		dir, err := filepath.Rel(root, c.Dir)
		if err != nil {
			dir = "."
		}
		kind := "script"
		switch {
		case len(c.Argv) >= 2 && c.Argv[0] == "go" && c.Argv[1] == "test":
			kind = "go"
		case len(c.Argv) >= 3 && c.Argv[1] == "-m" && c.Argv[2] == "pytest":
			kind = "pytest"
		}
		out = append(out, plan{kind: kind, dir: filepath.ToSlash(dir), argv: c.Argv, from: []string{"cull: detected test command"}, raw: true})
	}
	return out, nil
}

type procResult struct {
	seconds   float64
	exit      int
	timedOut  bool
	cancelled bool
}

func describe(p plan) string {
	dir := p.dir
	if dir == "" {
		dir = "."
	}
	return strings.Join(p.argv, " ") + " in " + dir
}

// checkEnv is the filtered environment with the check's own variables on top.
// The API key never gets through, even when a prefix sets it.
func checkEnv(base, extra []string) []string {
	out := make([]string, 0, len(base)+len(extra))
	out = append(out, base...)
	out = append(out, extra...)
	kept := out[:0]
	for _, kv := range out {
		if strings.HasPrefix(kv, "TYPESAFE_API_KEY=") {
			continue
		}
		kept = append(kept, kv)
	}
	return kept
}

var (
	varRef    = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*|[0-9@#?*!$-])`)
	expansion = regexp.MustCompile("\\$[A-Za-z_{(0-9@#?*!$-]|`")
)

// expands: the word holds something sh would expand.
func expands(w string) bool {
	return expansion.MatchString(w) || strings.ContainsAny(w, "*?[")
}

func shQuote(w string) string { return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'" }

// shellForm is how to run argv. Plain commands run directly. When a word needs
// shell expansion ($var, `cmd`, globs) the command runs through sh -c with the
// other words quoted, so the expansion happens at run time as in the project's
// own script. A note (non-empty) means the check cannot be run faithfully.
func shellForm(argv, env, own []string) ([]string, string) {
	for _, kv := range own {
		_, v, _ := strings.Cut(kv, "=")
		if expansion.MatchString(v) {
			return nil, "an environment value holds a shell expansion"
		}
	}
	needSh := false
	for _, w := range argv {
		if strings.Contains(w, "${{") || strings.Contains(w, "{{") {
			return nil, "depends on a workflow or recipe expression"
		}
		if expands(w) {
			needSh = true
		}
	}
	if !needSh {
		return argv, ""
	}
	set := map[string]bool{}
	for _, kv := range env {
		n, _, _ := strings.Cut(kv, "=")
		set[n] = true
	}
	var missing []string
	seen := map[string]bool{}
	for _, w := range argv {
		if !expansion.MatchString(w) {
			continue
		}
		for _, m := range varRef.FindAllStringSubmatch(w, -1) {
			if !set[m[1]] && !seen[m[1]] {
				seen[m[1]] = true
				missing = append(missing, "$"+m[1])
			}
		}
	}
	if len(missing) > 0 {
		return nil, "depends on " + strings.Join(missing, ", ") + " set elsewhere in the step"
	}
	parts := make([]string, len(argv))
	for i, w := range argv {
		if expands(w) {
			parts[i] = w
		} else {
			parts[i] = shQuote(w)
		}
	}
	return []string{"sh", "-c", strings.Join(parts, " ")}, ""
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

// runOne runs argv in dir with a timeout, in its own process group, capturing
// stdout and stderr together. Nothing is streamed.
func runOne(ctx context.Context, dir string, argv []string, timeout time.Duration, env []string) (procResult, []byte) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	buf := &syncBuffer{}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = buf, buf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	began := time.Now()
	if err := cmd.Start(); err != nil {
		return procResult{exit: -1}, []byte(err.Error() + "\n")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-cctx.Done():
		cancelled := ctx.Err() != nil
		if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(killGraceDelay):
		}
		return procResult{seconds: time.Since(began).Seconds(), exit: -1, timedOut: !cancelled, cancelled: cancelled}, buf.Bytes()
	case err := <-done:
		code := 0
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		return procResult{seconds: time.Since(began).Seconds(), exit: code}, buf.Bytes()
	}
}

func tailOf(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// goHints: packages whose median test takes at least 0.3 s and that the speed
// scan found Go setup blocking parallel tests in.
func goHints(root string, cr CheckResult) []Hint {
	var slow []GoPackage
	for _, p := range cr.Packages {
		if p.Top > 0 && p.MedianSeconds >= goMedianHint {
			slow = append(slow, p)
		}
	}
	if len(slow) == 0 {
		return nil
	}
	dir := filepath.Join(root, filepath.FromSlash(cr.Dir))
	modDir, mod := findModule(root, dir)
	if mod == "" {
		return nil
	}
	var files []string
	_ = filepath.WalkDir(dir, func(abs string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		if d.IsDir() {
			n := d.Name()
			if abs != dir && (strings.HasPrefix(n, ".") || n == "node_modules" || n == "vendor" || n == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), "_test.go") {
			if rel, err := filepath.Rel(root, abs); err == nil {
				files = append(files, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	findings, err := speed.Scan(root, files, nil)
	if err != nil {
		return nil
	}
	byPkg := map[string]speed.Finding{}
	for _, f := range findings {
		if f.Kind != speed.KindSetenv {
			continue
		}
		rel, err := filepath.Rel(modDir, filepath.Join(root, filepath.FromSlash(path.Dir(f.File))))
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		pkg := mod
		if rel != "." {
			pkg = mod + "/" + rel
		}
		if _, ok := byPkg[pkg]; !ok {
			byPkg[pkg] = f
		}
	}
	var out []Hint
	for _, p := range slow {
		f, ok := byPkg[p.Name]
		if !ok {
			continue
		}
		n := f.Held
		if n == 0 {
			n = p.Top
		}
		out = append(out, Hint{Kind: "go_setenv", Where: fmt.Sprintf("%s:%d", f.File, f.Line),
			Message: fmt.Sprintf("%s: %d tests average %.2f s and can't run in parallel because %s",
				p.Name, n, p.MedianSeconds, setenvCause(f.Detail))})
	}
	return out
}

// setenvCause names what blocks parallel tests, from a setenv finding's detail.
func setenvCause(detail string) string {
	if i := strings.Index(detail, " calls t.Setenv"); i > 0 && !strings.Contains(detail[:i], " ") {
		return detail[:i] + " calls t.Setenv"
	}
	return "the tests call t.Setenv"
}

// findModule walks up from dir (not above root) to the directory holding go.mod.
func findModule(root, dir string) (string, string) {
	for d := dir; ; d = filepath.Dir(d) {
		if m := modulePath(d); m != "" {
			return d, m
		}
		if d == root || filepath.Dir(d) == d {
			return "", ""
		}
		if rel, err := filepath.Rel(root, d); err != nil || strings.HasPrefix(rel, "..") {
			return "", ""
		}
	}
}

func modulePath(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "module ") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(l, "module ")), `"`)
		}
	}
	return ""
}

// pythonHints: files that spend at least half their time in setup.
func pythonHints(r PyResult) []Hint {
	var out []Hint
	for _, f := range r.Files {
		if f.Seconds > 0 && f.SetupShare >= pySetupHint {
			out = append(out, Hint{Kind: "python_setup", Where: f.File,
				Message: fmt.Sprintf("%s spends %.0f%% of its time in setup (%.1f s of %.1f s)", f.File, f.SetupShare*100, f.SetupSeconds, f.Seconds)})
		}
	}
	return out
}

// WriteText prints the report as tables.
func WriteText(w io.Writer, r *Report) {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	p("total %.1f s\n", r.TotalSeconds)
	for _, c := range r.Checks {
		state := "ok"
		switch {
		case c.TimedOut:
			state = "TIMED OUT"
		case !c.OK:
			state = fmt.Sprintf("FAILED (exit %d)", c.Exit)
		}
		dir := c.Dir
		if dir == "" {
			dir = "."
		}
		p("\n%s  %s  %s  %.1f s  %s\n", c.Kind, dir, strings.Join(c.Argv, " "), c.Seconds, state)
		if c.Summary != "" {
			p("  %s\n", c.Summary)
		}
		if !c.OK && c.Tail != "" {
			p("  last lines:\n")
			for _, l := range strings.Split(c.Tail, "\n") {
				p("    %s\n", l)
			}
		}
		for i, pk := range c.Packages {
			if i == 10 {
				p("  … %d more packages\n", len(c.Packages)-10)
				break
			}
			if pk.NoTests {
				continue
			}
			p("  %5.2f s %3.0f%%  %s  (%d tests, median %.2f s)\n", pk.Seconds, pk.Share*100, pk.Name, pk.Top, pk.MedianSeconds)
		}
		for i, f := range c.Files {
			if i == 10 {
				p("  … %d more files\n", len(c.Files)-10)
				break
			}
			p("  %5.2f s %3.0f%%  %s  (setup %.0f%%)\n", f.Seconds, f.Share*100, f.File, f.SetupShare*100)
		}
	}
	for _, n := range r.NotRun {
		p("\n%s\n", n)
	}
	if len(r.Slowest) > 0 {
		p("\nslowest tests\n")
		for _, t := range r.Slowest {
			name := t.Name
			if t.Package != "" {
				name = t.Package + " " + t.Name
			}
			p("  %6.2f s  %s\n", t.Seconds, name)
		}
	}
	if len(r.Hints) > 0 {
		p("\nhints\n")
		for _, h := range r.Hints {
			p("  %s\n", h.Message)
		}
	}
}
