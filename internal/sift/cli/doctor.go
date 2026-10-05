package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/store"
	tools "github.com/schuettc/tools-common"
)

// ghAuth is a test seam: whether gh is logged in.
var ghAuth = func() error { return exec.Command("gh", "auth", "status").Run() }

var doctorFlags = flags("doctor", "sift doctor",
	"Checks what sift needs, one line per check: ok, or what to fix. Exit 0 when nothing\n"+
		"required is missing (the config, every root readable, the state database), 1 otherwise.\n"+
		"A harness that is on but not installed, and a missing gh, are notes: without gh, PR and\n"+
		"issue references are left for you to judge.", nil)

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

	cfg, err := config.Load(config.Path())
	switch {
	case errors.Is(err, config.ErrMissing):
		line("config", true, "no config at "+config.Path()+": run sift init", "")
	case err != nil:
		line("config", true, err.Error(), "")
	default:
		line("config", true, "", " ("+config.Path()+")")
		profiles, err := cfg.Enabled()
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

	if missing > 0 {
		return tools.Exitf(1, "%d required item(s) missing", missing)
	}
	return nil
}
