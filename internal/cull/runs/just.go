package runs

import (
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
)

type justInfo struct {
	First       string                `json:"first"`
	Recipes     map[string]justRecipe `json:"recipes"`
	Assignments map[string]justAssign `json:"assignments"`
}

type justAssign struct {
	Value any `json:"value"`
}

type justRecipe struct {
	Name       string          `json:"name"`
	Shebang    bool            `json:"shebang"`
	Priors     int             `json:"priors"`
	Body       [][]any         `json:"body"`
	Params     []justParam     `json:"parameters"`
	Deps       []justDep       `json:"dependencies"`
	Attributes json.RawMessage `json:"attributes"`
}

type justParam struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Default any    `json:"default"`
}

type justDep struct {
	Recipe string `json:"recipe"`
}

// dumpJust prints the justfile's recipes as JSON. A variable so tests can stand
// in for a machine without just.
var dumpJust = func(root string) ([]byte, error) {
	cmd := exec.Command("just", "--dump", "--dump-format", "json")
	cmd.Dir = root
	return cmd.Output()
}

func (e *engine) loadJust() *justInfo {
	if e.justOK {
		return e.just
	}
	e.justOK = true
	b, err := dumpJust(e.root)
	if err != nil {
		e.justEr = err.Error()
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			e.justEr = strings.TrimSpace(string(ee.Stderr))
		}
		return nil
	}
	var j justInfo
	if err := json.Unmarshal(b, &j); err != nil {
		e.justEr = "unreadable just output: " + err.Error()
		return nil
	}
	e.just = &j
	return e.just
}

var justNoRun = map[string]bool{
	"--list": true, "-l": true, "--fmt": true, "--summary": true, "--evaluate": true, "--show": true, "-s": true,
	"--choose": true, "--dump": true, "--init": true, "--edit": true, "-e": true, "--completions": true,
	"--variables": true, "--groups": true, "--changelog": true, "--man": true,
}

// callJust follows `just [flags] recipe [args] [recipe ...]`.
func (e *engine) callJust(args []string, c *ctx) {
	var names []string
	flags := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = true
			if justNoRun[a] {
				return // lists, formats or shows; runs no recipe
			}
			switch a {
			case "-f", "--justfile", "-d", "--working-directory", "--set":
				i++
				if a == "--set" {
					i++
				}
			}
			continue
		}
		names = append(names, a)
	}
	if len(names) == 0 && flags {
		return // flags and no recipe: not the default recipe
	}
	j := e.loadJust()
	if len(names) == 0 {
		if j == nil {
			e.emit(*c, "unknown", []string{"just"}, nil, "just could not be read: "+e.justEr)
			return
		}
		names = []string{j.First}
	}
	for i := 0; i < len(names); i++ {
		name := names[i]
		if strings.Contains(name, "=") {
			continue
		}
		if j == nil {
			e.emit(*c, "unknown", append([]string{"just"}, names[i:]...), nil, "just could not be read: "+e.justEr)
			return
		}
		r, ok := j.Recipes[name]
		if !ok {
			e.emit(*c, "unknown", []string{"just", name}, nil, "no such recipe in the justfile")
			continue
		}
		vars := map[string]string{}
		for _, p := range r.Params {
			if p.Kind == "plus" || p.Kind == "star" {
				vars[p.Name] = strings.Join(names[i+1:], " ")
				i = len(names)
				break
			}
			if i+1 < len(names) {
				i++
				vars[p.Name] = names[i]
			} else if s, ok := p.Default.(string); ok {
				vars[p.Name] = s
			}
		}
		e.runRecipe(j, r, vars, c, c.setup.fork())
	}
}

// runRecipe walks a recipe. acc is its setup scope: the commands before it,
// shared with its dependency recipes, whose commands run first.
func (e *engine) runRecipe(j *justInfo, r justRecipe, vars map[string]string, c *ctx, acc *setupAcc) {
	key := "just\x00" + r.Name
	if c.active[key] {
		return
	}
	c.active[key] = true
	defer delete(c.active, key)
	label := "justfile " + r.Name
	dep := func(d justDep) {
		if dr, ok := j.Recipes[d.Recipe]; ok {
			// A dependency that is itself a check (it emitted one) does not
			// set up what comes after it; one that only prepares does.
			sub := acc.fork()
			nc := ctx{dir: ".", chain: c.chain, env: c.env, active: c.active, setup: sub}.with(label)
			before := e.emitted
			e.runRecipe(j, dr, nil, &nc, sub)
			if e.emitted == before {
				acc.steps = sub.steps
			}
		}
	}
	for i, d := range r.Deps {
		if i < r.Priors {
			dep(d)
		}
	}
	var lines []string
	for _, l := range r.Body {
		lines = append(lines, e.render(j, l, vars))
	}
	hop := func(int) string { return label }
	base := ctx{dir: ".", chain: c.chain, env: c.env, vars: vars, active: c.active, setup: acc}
	if r.Shebang {
		if len(lines) > 0 {
			lines = lines[1:] // the #! line
		}
		e.runText(strings.Join(lines, "\n"), 1, hop, base, true)
	} else {
		for _, l := range lines {
			t := strings.TrimLeft(l, "@- ")
			lb := base
			lb.ignoreFail = strings.Contains(l[:len(l)-len(t)], "-")
			e.runText(t, 1, hop, lb, false)
		}
	}
	for i, d := range r.Deps {
		if i >= r.Priors {
			dep(d)
		}
	}
}

// render turns one body line (strings and interpolations) into text.
// Variables that are plain strings, or bound parameters, are substituted; the
// rest become "{{name}}", which makes any check using them unknown.
func (e *engine) render(j *justInfo, line []any, vars map[string]string) string {
	var sb strings.Builder
	for _, f := range line {
		switch v := f.(type) {
		case string:
			sb.WriteString(v)
		case []any:
			sb.WriteString(e.interp(j, v, vars))
		}
	}
	return sb.String()
}

func (e *engine) interp(j *justInfo, parts []any, vars map[string]string) string {
	var sb strings.Builder
	for _, p := range parts {
		arr, ok := p.([]any)
		if !ok || len(arr) == 2 && arr[0] != "variable" {
			return "{{expression}}"
		}
		if len(arr) == 2 && arr[0] == "variable" {
			name, _ := arr[1].(string)
			if s, ok := vars[name]; ok {
				sb.WriteString(s)
			} else if a, ok := j.Assignments[name]; ok {
				if s, ok := a.Value.(string); ok {
					sb.WriteString(s)
				} else {
					return "{{" + name + "}}"
				}
			} else {
				return "{{" + name + "}}"
			}
			continue
		}
		return "{{expression}}"
	}
	return sb.String()
}
