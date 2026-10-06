package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/profile"
	"github.com/schuettc/tackle/internal/sift/serve"
	"github.com/schuettc/tackle/internal/sift/store"
	tools "github.com/schuettc/tools-common"
)

// ghAuth is a test seam: whether gh is logged in.
var ghAuth = func() error { return exec.Command("gh", "auth", "status").Run() }

var doctorFlags = flags("doctor", "sift doctor",
	"Checks what sift needs, one line per check: ok, or what to fix. Exit 0 when nothing\n"+
		"required is missing (the config, every root readable, the state database), 1 otherwise.\n"+
		"A harness that is on but not installed, and a missing gh, are notes: without gh, PR and\n"+
		"issue references are left for you to judge. So are the page server (sift serve) and each\n"+
		"enabled harness's channel registration: without it, a session hears Send only through\n"+
		"sift wait.", nil)

func runDoctor(args []string, out, errw io.Writer) error {
	fs := doctorFlags()
	pos, err := parse(fs, args, out)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return tools.UsageError{Msg: "doctor takes no arguments"}
	}
	missing := 0
	line := func(name string, required bool, problem, okMsg string) {
		if problem == "" {
			_, _ = fmt.Fprintf(out, "%-14s ok%s\n", name, okMsg)
			return
		}
		mark := "note"
		if required {
			mark = "missing"
			missing++
		}
		_, _ = fmt.Fprintf(out, "%-14s %s: %s\n", name, mark, problem)
	}

	var profiles []profile.Profile
	cfg, err := config.Load(config.Path())
	switch {
	case errors.Is(err, config.ErrMissing):
		line("config", true, "no config at "+config.Path()+": run sift init", "")
	case err != nil:
		line("config", true, err.Error(), "")
	default:
		line("config", true, "", " ("+config.Path()+")")
		var err error
		profiles, err = cfg.Enabled()
		if err != nil {
			line("profiles", true, err.Error(), "")
		} else if len(profiles) == 0 {
			line("profiles", false, "none on: only the roots are audited", "")
		}
		for _, p := range profiles {
			if st, err := os.Stat(p.HomeDir()); err != nil || !st.IsDir() {
				line(p.Name, false, p.HomeDir()+" not found: is "+p.Label+" installed?", "")
				continue
			}
			global := ""
			for _, g := range p.Global {
				if _, err := os.Stat(p.Path(g)); err == nil {
					global = p.Path(g)
					break
				}
			}
			if global == "" {
				line(p.Name, false, "no global file in "+p.HomeDir(), "")
			} else {
				line(p.Name, false, "", " ("+global+")")
			}
		}
		if len(cfg.Roots) == 0 {
			line("roots", false, "none configured: only global files and skills are audited", "")
		}
		for _, r := range cfg.Roots {
			if _, err := os.ReadDir(r.Path); err != nil {
				line("root", true, fmt.Sprintf("%s: %v", r.Path, err), "")
			} else {
				line("root", true, "", " ("+r.Path+")")
			}
		}
	}

	if s, err := store.Open(context.Background(), store.Path()); err != nil {
		line("state", true, fmt.Sprintf("%s: %v", store.Path(), err), "")
	} else {
		_ = s.Close()
		line("state", true, "", " ("+store.Path()+")")
	}
	if _, err := lookPath("gh"); err != nil {
		line("gh", false, "not on PATH: PR and issue references are left for you to judge", "")
	} else if err := ghAuth(); err != nil {
		line("gh", false, "not logged in (gh auth login): PR and issue references are left for you to judge", "")
	} else {
		line("gh", false, "", "")
	}

	// The page server, and how each enabled harness's sessions hear Send.
	if _, err := serve.Running(); err != nil {
		line("serve", false, "sift serve is not running (sift serve starts it)", "")
	} else {
		line("serve", false, "", "")
	}
	for _, p := range profiles {
		if where, ok := channelRegistered(p); where != "" {
			if ok {
				line(p.Name+" channel", false, "", "")
			} else {
				line(p.Name+" channel", false, "sift is not in "+where+": its sessions hear Send only through sift wait", "")
			}
		}
	}

	if missing > 0 {
		return tools.Exitf(1, "%d required item(s) missing", missing)
	}
	return nil
}

// channelRegistered reports where a shipped harness registers MCP servers
// and whether sift is there: Claude Code's .claude.json mcpServers, Codex's
// config.toml [mcp_servers], pi's channels.json. where is "" for a harness
// sift doesn't know how to check.
func channelRegistered(p profile.Profile) (where string, ok bool) {
	switch p.Name {
	case "claude-code":
		dir := os.Getenv("CLAUDE_CONFIG_DIR")
		if !filepath.IsAbs(dir) {
			dir, _ = os.UserHomeDir()
		}
		where = filepath.Join(dir, ".claude.json")
		return where + " mcpServers", jsonHas(where, "mcpServers")
	case "codex":
		where = filepath.Join(p.HomeDir(), "config.toml")
		var c struct {
			MCPServers map[string]any `toml:"mcp_servers"`
		}
		_, err := toml.DecodeFile(where, &c)
		_, has := c.MCPServers["sift"]
		return where + " [mcp_servers]", err == nil && has
	case "pi":
		where = filepath.Join(p.HomeDir(), "channels.json")
		return where + " channelServers", jsonHas(where, "channelServers")
	}
	return "", false
}

// jsonHas reports whether the JSON file has a "sift" key, under section when
// given.
func jsonHas(path, section string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return false
	}
	if section != "" {
		if json.Unmarshal(m[section], &m) != nil {
			return false
		}
	}
	_, ok := m["sift"]
	return ok
}
