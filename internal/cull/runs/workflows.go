package runs

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func (e *engine) readWorkflows() {
	var files []string
	for _, ext := range []string{"yml", "yaml"} {
		m, _ := filepath.Glob(filepath.Join(e.root, ".github", "workflows", "*."+ext))
		files = append(files, m...)
	}
	sort.Strings(files)
	for _, f := range files {
		rel, _ := filepath.Rel(e.root, f)
		rel = filepath.ToSlash(rel)
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var doc yaml.Node
		if yaml.Unmarshal(b, &doc) != nil || len(doc.Content) == 0 {
			e.emit(ctx{dir: ".", chain: []string{rel}}, "unknown", []string{"workflow", rel}, nil, "the workflow file does not parse")
			continue
		}
		e.workflow(rel, doc.Content[0])
	}
}

// get returns the value node of key in a mapping node.
func get(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

func defaultDir(n *yaml.Node) string {
	return scalar(get(get(get(n, "defaults"), "run"), "working-directory"))
}

func (e *engine) workflow(rel string, root *yaml.Node) {
	wfDir := defaultDir(root)
	wfEnv := plainEnv(get(root, "env"))
	jobs := get(root, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(jobs.Content); i += 2 {
		job := jobs.Content[i+1]
		jobDir := wfDir
		jobEnv := mergeEnv(wfEnv, plainEnv(get(job, "env")))
		if d := defaultDir(job); d != "" {
			jobDir = d
		}
		if u := get(job, "uses"); u != nil && !strings.HasPrefix(u.Value, "./") {
			e.uses(rel, u, ".")
		}
		steps := get(job, "steps")
		if steps == nil || steps.Kind != yaml.SequenceNode {
			continue
		}
		for _, st := range steps.Content {
			dir := jobDir
			if d := scalar(get(st, "working-directory")); d != "" {
				dir = d
			}
			dir = workDir(dir)
			if u := get(st, "uses"); u != nil {
				e.uses(rel, u, dir)
				continue
			}
			run := get(st, "run")
			if run == nil || run.Kind != yaml.ScalarNode {
				continue
			}
			if sh, _, _ := strings.Cut(strings.TrimSpace(scalar(get(st, "shell"))), " "); sh != "" && sh != "bash" && sh != "sh" && sh != "zsh" {
				c := ctx{dir: dir, chain: []string{rel + ":" + itoa(run.Line) + " run"}}
				e.emit(c, "unknown", []string{"shell", sh}, nil, "shell "+sh+" not read")
				continue
			}
			e.runScalar(run, rel, "run", dir, mergeEnv(jobEnv, plainEnv(get(st, "env"))))
		}
	}
}

func workDir(d string) string {
	if d == "" {
		return "."
	}
	if strings.Contains(d, "${{") {
		return "{{expression}}"
	}
	return join(".", d)
}

// runScalar walks a `run:` style scalar; a block scalar's lines are numbered
// from the line after its indicator.
func (e *engine) runScalar(n *yaml.Node, file, what, dir string, env []string) {
	first, text := n.Line, n.Value
	if n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		first++
	}
	c := ctx{dir: dir, env: env, active: map[string]bool{}}
	e.runText(text, first, func(l int) string { return file + ":" + itoa(l) + " " + what }, c, true)
}

var checkWords = map[string]bool{"test": true, "tests": true, "testing": true, "ci": true, "check": true, "checks": true, "gate": true, "verify": true, "e2e": true}

var splitName = regexp.MustCompile(`[^A-Za-z0-9]+`)

// usesMayTest: an action whose name says it tests, checks or gates.
func usesMayTest(name string) bool {
	name, _, _ = strings.Cut(name, "@")
	for _, seg := range splitName.Split(strings.ToLower(path.Clean(name)), -1) {
		if checkWords[seg] {
			return true
		}
	}
	return false
}

func (e *engine) uses(file string, n *yaml.Node, dir string) {
	if !usesMayTest(n.Value) {
		return
	}
	c := ctx{dir: dir, chain: []string{file + ":" + itoa(n.Line) + " uses"}}
	e.emit(c, "unknown", []string{"uses", n.Value}, nil, "an action whose steps are not read; it may run tests")
}

// plainEnv lists the NAME=value pairs of an env: mapping whose values are
// plain strings; values holding an expression (${{ ... }}) are left out.
func plainEnv(n *yaml.Node) []string {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	var out []string
	for i := 0; i+1 < len(n.Content); i += 2 {
		v := n.Content[i+1]
		if v.Kind != yaml.ScalarNode || strings.Contains(v.Value, "${{") {
			continue
		}
		out = append(out, n.Content[i].Value+"="+v.Value)
	}
	return out
}
