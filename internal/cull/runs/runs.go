// Package runs finds what a project actually runs to test itself: the commands
// in its CI workflows, git hooks, task recipes and package scripts, followed
// from those entry points to the test runners they reach. It reads files and
// never runs a project command (the one exception is `just --dump`, which only
// prints the recipes).
package runs

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Check is one command the project runs that tests it, or that might.
type Check struct {
	Kind  string   `json:"kind"`            // go | pytest | jest | vitest | node-test | script | unknown
	Dir   string   `json:"dir"`             // root-relative working directory
	Argv  []string `json:"argv"`            // as the project runs it
	Files []string `json:"files,omitempty"` // script checks: the entry file and its local imports
	// From lists every way the check is reached. Each entry is one chain,
	// outermost first, hops joined by " > ", e.g.
	// ".github/workflows/ci.yml:111 run > justfile verify-slow > justfile cull-probe".
	From []string `json:"from"`
	Note string   `json:"note,omitempty"` // why it is unknown
	// Env is the VAR=value pairs the command runs with: its own prefixes
	// (KIT_BROWSER=required go test ...) and plain-string workflow env values.
	Env []string `json:"env,omitempty"`
	// Setup is the commands that come before the check in the same recipe
	// body (after its dependency recipes), run step or hook command, in order.
	Setup []Step `json:"setup,omitempty"`
}

// Step is one setup command: where it runs, what, and with which variables.
type Step struct {
	Dir  string   `json:"dir"`
	Argv []string `json:"argv"`
	Env  []string `json:"env,omitempty"`
	From string   `json:"from,omitempty"`
}

// setupAcc collects the setup commands seen so far in one scope.
type setupAcc struct{ steps []Step }

func (a *setupAcc) fork() *setupAcc {
	if a == nil {
		return &setupAcc{}
	}
	return &setupAcc{steps: append([]Step(nil), a.steps...)}
}

// Find reads root's entry points and returns the checks they run, merged: the
// same (kind, dir, argv) reached several ways is one Check with every chain in
// From. Order is the order of discovery.
func Find(root string) ([]Check, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	e := &engine{root: abs, index: map[string]int{}, pkgs: map[string]*pkgInfo{}}
	e.readWorkflows()
	e.readLefthook()
	if e.checks == nil {
		return []Check{}, nil
	}
	return e.checks, nil
}

type engine struct {
	root   string
	checks []Check
	index  map[string]int
	pkgs   map[string]*pkgInfo
	just   *justInfo
	justOK bool // load attempted
	justEr string
	// emitted counts emit calls, so a command that produced a check can be told
	// from one that did not.
	emitted int
}

// ctx is the state while one shell text is walked.
type ctx struct {
	dir    string            // current root-relative directory (may hold "{{" when not known)
	chain  []string          // hops so far, outermost first
	env    []string          // VAR=value pairs in force (workflow env:, command prefixes)
	vars   map[string]string // just parameters bound by the caller
	active map[string]bool   // recipes and scripts being followed (loop guard)
	setup  *setupAcc         // commands before this point in the scope (nil: not tracked)
	// noSetup: the command being walked is conditional or part of a pipeline
	// or background job, so it is not a setup step.
	noSetup bool
}

func (c ctx) with(hop string) ctx {
	n := c
	n.chain = append(append([]string(nil), c.chain...), hop)
	return n
}

func (e *engine) exists(rel string) bool {
	st, err := os.Stat(filepath.Join(e.root, filepath.FromSlash(rel)))
	return err == nil && !st.IsDir()
}

func (e *engine) emit(c ctx, kind string, argv []string, files []string, note string) {
	e.emitted++
	dir := c.dir
	if strings.Contains(dir, "{{") || hasExpr(argv) || hasExpr(c.env) {
		if kind != "unknown" {
			note = "the directory or arguments hold a variable or expression that is not known statically"
		}
		if dir == outsideDir {
			note = "outside the project: not read"
		}
		kind = "unknown"
		files = nil
		if strings.Contains(dir, "{{") {
			dir = "."
		}
	}
	key := kind + "\x00" + dir + "\x00" + strings.Join(argv, "\x00")
	from := strings.Join(c.chain, " > ")
	if i, ok := e.index[key]; ok {
		for _, f := range e.checks[i].From {
			if f == from {
				return
			}
		}
		e.checks[i].From = append(e.checks[i].From, from)
		return
	}
	e.index[key] = len(e.checks)
	var setup []Step
	if c.setup != nil && kind != "unknown" {
		setup = append(setup, c.setup.steps...)
	}
	e.checks = append(e.checks, Check{Setup: setup, Kind: kind, Dir: dir, Argv: argv, Files: files, From: []string{from}, Note: note, Env: append([]string(nil), c.env...)})
}

func hasExpr(argv []string) bool {
	for _, a := range argv {
		if strings.Contains(a, "{{") {
			return true
		}
	}
	return false
}

// outsideDir stands for a directory above the project root.
const outsideDir = "{{outside the project}}"

func join(dir, rel string) string {
	if strings.Contains(dir, "{{") {
		return dir
	}
	p := path.Clean(path.Join(dir, rel))
	if p == ".." || strings.HasPrefix(p, "../") {
		return outsideDir
	}
	return p
}
