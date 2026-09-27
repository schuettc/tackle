package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"

	"github.com/schuettc/tackle/internal/cull/check"
	"github.com/schuettc/tackle/internal/cull/jev"
	"github.com/schuettc/tackle/internal/cull/judge"
	tools "github.com/schuettc/tools-common"
)

var checkFlags = flags("check", "cull check [path] [--diff base] [--json] [--dry-run] [--refresh]",
	"Judge tests and near-duplicate groups under path (default .) with Jev: whole suite, or\n"+
		"only what --diff base changed. Writes <root>/.cull/last.json. Requires egress = true in\n"+
		".cull.toml (or --dry-run, which prints the states that would be sent and sends nothing).\n"+
		"Sends test source to "+endpoint+". The key comes from TYPESAFE_API_KEY:\n"+
		"  creel exec TYPESAFE_API_KEY -- cull check ...",
	func(fs *flag.FlagSet) {
		fs.String("diff", "", "base ref; judge only tests changed since it (default: the whole suite)")
		fs.Bool("json", false, "print the full report as JSON instead of a table")
		fs.Bool("dry-run", false, "print each state that would be sent; send nothing")
		fs.Bool("refresh", false, "ignore cached answers")
	})

func runCheck(stdin io.Reader) func(args []string, out, errw io.Writer) error {
	return func(args []string, out, errw io.Writer) error {
		fs := checkFlags()
		pos, err := parse(fs, args, out)
		if err != nil {
			return err
		}
		if len(pos) > 1 {
			return tools.UsageError{Msg: "check takes at most one path"}
		}
		path := "."
		if len(pos) == 1 {
			path = pos[0]
		}
		dryRun := boolFlag(fs, "dry-run")
		jsonMode := boolFlag(fs, "json")

		var ev judge.Evaluator
		if !dryRun {
			key := os.Getenv("TYPESAFE_API_KEY")
			if key == "" {
				return tools.Exitf(2, "TYPESAFE_API_KEY is not set").WithHint("creel exec TYPESAFE_API_KEY -- cull check ...")
			}
			ev = jev.NewClient(key)
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()

		report, err := check.Run(ctx, ev, check.Options{
			Path: path, Diff: str(fs, "diff"), DryRun: dryRun, Refresh: boolFlag(fs, "refresh"), Stderr: errw,
		})
		if errors.Is(err, jev.ErrUnauthorized) {
			return tools.Exitf(2, "%v", err).WithHint("creel exec TYPESAFE_API_KEY -- cull check ...")
		}
		if err != nil {
			return tools.Exitf(2, "%v", err)
		}

		if dryRun {
			return nil
		}

		if jsonMode {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			if err := enc.Encode(report); err != nil {
				return err
			}
		} else {
			check.WriteTable(out, report)
		}

		if report.HasActions() {
			return tools.Exitf(1, "%d to cut, %d to consolidate",
				report.Summary["cut"], report.Summary["consolidate"])
		}
		return nil
	}
}
