package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/schuettc/tackle/internal/cull/check"
	"github.com/schuettc/tackle/internal/cull/discover"
	"github.com/schuettc/tackle/internal/cull/key"
	"github.com/schuettc/tackle/internal/cull/serve"
	"github.com/schuettc/tackle/internal/cull/verify"
	tools "github.com/schuettc/tools-common"
)

var doctorFlags = flags("doctor", "cull doctor [path]",
	"Checks what cull needs, one line per check: ok, or what to fix. Exit 0 when nothing\n"+
		"required is missing (key, egress, python3 for Python projects, node and typescript for TypeScript projects,\n"+
		"a test command), 1 otherwise. The server and the agent registrations are reported but\n"+
		"not required. Shows where the key comes from, never the key.", nil)

func runDoctor(args []string, out, errw io.Writer) error {
	fs := doctorFlags()
	pos, err := parse(fs, args, out)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return tools.UsageError{Msg: "doctor takes at most one path"}
	}
	path := "."
	if len(pos) == 1 {
		path = pos[0]
	}
	root, cfg, err := check.ResolveConfig(path, true)
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}

	missing := 0
	line := func(name string, required bool, problem, okMsg string) {
		if problem == "" {
			_, _ = fmt.Fprintf(out, "%-14s ok%s\n", name, okMsg)
			return
		}
		mark := "missing"
		if !required {
			mark = "note"
		} else {
			missing++
		}
		_, _ = fmt.Fprintf(out, "%-14s %s: %s\n", name, mark, problem)
	}

	if _, src, err := key.Load(); err != nil {
		line("key", true, "no TypeSafe key: run cull init", "")
	} else {
		line("key", true, "", " ("+src+")")
	}
	if cfg.Egress {
		line("egress", true, "", "")
	} else {
		line("egress", true, "egress is not enabled: run cull init, or set egress = true in .cull.toml", "")
	}
	files, suiteErr := discover.Suite(root, "", cfg.Exclude)
	langs := map[string]string{}
	for _, f := range files {
		switch strings.ToLower(filepath.Ext(f)) {
		case ".go":
			langs[f] = "go"
		case ".py":
			langs[f] = "python"
		case ".ts", ".tsx":
			langs[f] = "typescript"
		}
	}
	isPy := false
	for _, l := range langs {
		isPy = isPy || l == "python"
	}
	if isPy {
		if _, err := exec.LookPath("python3"); err != nil {
			line("python3", true, "python3 is not on PATH: install Python 3", "")
		} else {
			line("python3", true, "", "")
		}
	}
	isTS := false
	for _, l := range langs {
		isTS = isTS || l == "typescript"
	}
	if isTS {
		if _, err := exec.LookPath("node"); err != nil {
			line("node", true, "node is not on PATH: install Node.js", "")
		} else {
			line("node", true, "", "")
		}
		if _, err := os.Stat(filepath.Join(root, "node_modules", "typescript")); err != nil {
			line("typescript", true, "node_modules/typescript not found: install the project's dependencies", "")
		} else {
			line("typescript", true, "", "")
		}
	}

	if suiteErr != nil {
		line("tests", true, fmt.Sprintf("could not list the project's tests: %v", suiteErr), "")
	} else if cmds, err := verify.Plan(root, cfg.TestCommand, langs); err != nil {
		line("test command", true, fmt.Sprintf("%v: set test_command in .cull.toml", err), "")
	} else if len(cmds) == 0 {
		line("test command", true, "no tests found under "+root, "")
	} else {
		var parts []string
		for _, c := range cmds {
			parts = append(parts, strings.Join(c.Argv, " "))
		}
		line("test command", true, "", " ("+strings.Join(parts, "; ")+")")
	}

	if _, err := serve.Running(); err != nil {
		line("serve", false, "cull serve is not running (cull serve starts it)", "")
	} else {
		line("serve", false, "", "")
	}

	home, _ := os.UserHomeDir()
	if registered(filepath.Join(home, ".claude.json"), "mcpServers") {
		line("~/.claude.json", false, "", "")
	} else {
		line("~/.claude.json", false, "cull is not in mcpServers: install cull with kempt", "")
	}
	if registered(filepath.Join(home, ".pi", "agent", "channels.json"), "") {
		line("channels.json", false, "", "")
	} else {
		line("channels.json", false, "cull is not in ~/.pi/agent/channels.json: install cull with kempt", "")
	}

	if missing > 0 {
		return tools.Exitf(1, "%d required item(s) missing", missing)
	}
	return nil
}

// registered reports whether the JSON file has a "cull" entry, under
// section when given (else at the top level or under "channels").
func registered(path, section string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return false
	}
	has := func(raw json.RawMessage) bool {
		var inner map[string]json.RawMessage
		if json.Unmarshal(raw, &inner) != nil {
			return false
		}
		_, ok := inner["cull"]
		return ok
	}
	if section != "" {
		return has(m[section])
	}
	if _, ok := m["cull"]; ok {
		return true
	}
	return has(m["channels"])
}
