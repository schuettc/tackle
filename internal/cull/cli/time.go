package cli

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/schuettc/tackle/internal/cull/check"
	"github.com/schuettc/tackle/internal/cull/timing"
	tools "github.com/schuettc/tools-common"
)

var timeFlags = flags("time", "cull time [path] [--timeout D] [--json]",
	"Run the checks the project runs (CI, hooks, recipes; found the way cull doctor lists them) one at a\n"+
		"time, each in its directory, after the commands that set it up in its recipe or CI step (for\n"+
		"example a build; run once per check, timed as setup, and a failing one fails that check),\n"+
		" with what timing needs added (go test -json, pytest --durations=0),\n"+
		"and report where the time goes: wall time per check, the slowest Go packages and Python files,\n"+
		"the 20 slowest tests, and what the speed scan explains. Output is captured, not shown. A check\n"+
		"that fails or times out is reported with its last lines and the run goes on. The TypeSafe key is\n"+
		"not in the commands' environment. Exit 0 when every check passed, 1 when one failed or timed out,\n"+
		"2 on error.",
	func(fs *flag.FlagSet) {
		fs.Duration("timeout", timing.DefaultTimeout, "per check timeout")
		fs.Bool("json", false, "print the report as JSON")
	})

func runTime(args []string, out, errw io.Writer) error {
	fs := timeFlags()
	pos, err := parse(fs, args, out)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return tools.UsageError{Msg: "time takes at most one path"}
	}
	path := "."
	if len(pos) == 1 {
		path = pos[0]
	}
	root, cfg, err := check.ResolveConfig(path, true)
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	rep, err := timing.Run(ctx, timing.Options{
		Root: root, Timeout: fs.Lookup("timeout").Value.(flag.Getter).Get().(time.Duration), TestCommand: cfg.TestCommand,
	})
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	if boolFlag(fs, "json") {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		timing.WriteText(out, rep)
	}
	if !rep.OK {
		return tools.Exitf(1, "a check failed or timed out")
	}
	return nil
}
