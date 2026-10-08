package runs

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func (e *engine) readLefthook() {
	for _, name := range []string{"lefthook.yml", "lefthook.yaml", ".lefthook.yml", ".lefthook.yaml", "lefthook-local.yml", "lefthook-local.yaml", ".lefthook-local.yml", ".lefthook-local.yaml"} {
		b, err := os.ReadFile(filepath.Join(e.root, name))
		if err != nil {
			continue
		}
		var doc yaml.Node
		if yaml.Unmarshal(b, &doc) != nil || len(doc.Content) == 0 {
			e.emit(ctx{dir: ".", chain: []string{name}}, "unknown", []string{"lefthook", name}, nil, "the hooks file does not parse")
			continue
		}
		top := doc.Content[0]
		if top.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(top.Content); i += 2 {
			hook, body := top.Content[i].Value, top.Content[i+1]
			if cmds := get(body, "commands"); cmds != nil && cmds.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(cmds.Content); j += 2 {
					e.hookJob(name, hook, cmds.Content[j].Value, cmds.Content[j+1])
				}
			}
			e.hookJobs(name, hook, get(body, "jobs"))
		}
	}
}

func (e *engine) hookJobs(file, hook string, jobs *yaml.Node) {
	if jobs == nil || jobs.Kind != yaml.SequenceNode {
		return
	}
	for k, j := range jobs.Content {
		label := scalar(get(j, "name"))
		if label == "" {
			label = "job" + itoa(k+1)
		}
		e.hookJob(file, hook, label, j)
		e.hookJobs(file, hook, get(get(j, "group"), "jobs"))
	}
}

func (e *engine) hookJob(file, hook, name string, job *yaml.Node) {
	run := get(job, "run")
	if run == nil || run.Kind != yaml.ScalarNode {
		return
	}
	dir := "."
	if r := scalar(get(job, "root")); r != "" {
		dir = join(".", r)
	}
	hop := file + ":" + itoa(run.Line) + " " + hook + " " + name
	first := run.Line
	if run.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		first++
	}
	c := ctx{dir: dir, active: map[string]bool{}, setup: &setupAcc{}}
	e.runText(run.Value, first, func(int) string { return hop }, c, true)
}
