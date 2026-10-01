package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/schuettc/tackle/internal/cull/check"
	"github.com/schuettc/tackle/internal/cull/jev"
	"github.com/schuettc/tackle/internal/cull/judge"
	"github.com/schuettc/tackle/internal/cull/key"
	tools "github.com/schuettc/tools-common"
)

var checkFlags = flags("check", "cull check [path] [--diff base] [--json] [--dry-run] [--refresh]",
	"Judge tests and near-duplicate groups under path (default .) with Jev: whole suite, or\n"+
		"only what --diff base changed. Writes <root>/.cull/last.json. Requires egress = true in\n"+
		".cull.toml (or --dry-run, which prints the states that would be sent and sends nothing).\n"+
		"Also records the run's uncertain items and applies Court's saved answers from the review\n"+
		"database (cull serve's page); if that database can't be opened, it warns on stderr and\n"+
		"writes last.json without them.\n"+
		"Sends test source to "+endpoint+". The key comes from TYPESAFE_API_KEY or the key file cull init writes.",
	func(fs *flag.FlagSet) {
		fs.String("diff", "", "base ref; judge only tests changed since it (default: the whole suite)")
		fs.Bool("json", false, "print the full report as JSON instead of a table")
		fs.Bool("dry-run", false, "print each state that would be sent; send nothing")
		fs.Bool("refresh", false, "ignore cached answers")
		fs.String("group", "", "verify a group id's rewrite (from the last check) instead of judging the suite")
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
		groupID := str(fs, "group")

		root, _, err := check.ResolveConfig(path, dryRun)
		if err != nil {
			return tools.Exitf(2, "%v", err)
		}

		var ev judge.Evaluator
		if !dryRun {
			k, _, err := key.Load()
			if err != nil {
				return tools.Exitf(2, "%v", err).WithHint("cull init")
			}
			ev = jev.NewClient(k)
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()

		if groupID != "" {
			return runCheckGroup(ctx, ev, root, groupID, check.Options{
				DryRun: dryRun, Refresh: boolFlag(fs, "refresh"), Stdout: out, Stderr: errw,
			}, jsonMode, out)
		}

		report, err := check.Run(ctx, ev, check.Options{
			Path: path, Diff: str(fs, "diff"), DryRun: dryRun, Refresh: boolFlag(fs, "refresh"), Stdout: out, Stderr: errw,
		})
		if errors.Is(err, jev.ErrUnauthorized) {
			return tools.Exitf(2, "%v", err).WithHint("cull init")
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

		if n := report.Summary["errors"]; n > 0 {
			return tools.Exitf(2, "%d item(s) could not be judged (incomplete run)", n)
		}
		if report.HasActions() {
			return tools.Exitf(1, "%d to cut, %d to consolidate",
				report.Summary["cut"], report.Summary["consolidate"])
		}
		return nil
	}
}

// runCheckGroup runs `cull check --group <id>`: verifies an agent's
// rewrite of one near-duplicate group into a table-driven test, prints the
// result, and maps it to an exit code (0 all four checks passed, 1 one or
// more failed, 2 error).
func runCheckGroup(ctx context.Context, ev judge.Evaluator, root, groupID string, opt check.Options, jsonMode bool, out io.Writer) error {
	gc, err := check.CheckGroup(ctx, ev, root, groupID, opt)
	if errors.Is(err, jev.ErrUnauthorized) {
		return tools.Exitf(2, "%v", err).WithHint("cull init")
	}
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}

	if jsonMode {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(gc); err != nil {
			return err
		}
	} else {
		writeGroupCheck(out, gc)
	}

	if len(gc.Unjudged) > 0 {
		return tools.Exitf(2, "%d item(s) could not be judged (incomplete run)", len(gc.Unjudged))
	}
	if !gc.OK {
		return tools.Exitf(1, "group %s: not verified (originals_gone=%v, new=%d, missing_rows=%d, flagged=%d)",
			groupID, gc.OriginalsGone, len(gc.NewTests), len(gc.MissingRows), len(gc.Flagged))
	}
	return nil
}

// writeGroupCheck renders a GroupCheck as a human-readable table.
func writeGroupCheck(w io.Writer, gc check.GroupCheck) {
	_, _ = fmt.Fprintf(w, "%s  %s\n", gc.ID, gc.File)
	_, _ = fmt.Fprintf(w, "  originals gone: %v\n", gc.OriginalsGone)
	if len(gc.StillPresent) > 0 {
		_, _ = fmt.Fprintf(w, "    still present: %s\n", strings.Join(gc.StillPresent, ", "))
	}
	if len(gc.NewTests) > 0 {
		_, _ = fmt.Fprintf(w, "  new tests: %s\n", strings.Join(gc.NewTests, ", "))
	}
	if len(gc.MissingRows) > 0 {
		_, _ = fmt.Fprintf(w, "  missing rows: %s\n", strings.Join(gc.MissingRows, ", "))
	}
	if len(gc.Flagged) > 0 {
		_, _ = fmt.Fprintf(w, "  flagged: %s\n", strings.Join(gc.Flagged, ", "))
	}
	if len(gc.Unjudged) > 0 {
		_, _ = fmt.Fprintf(w, "  unjudged: %s\n", strings.Join(gc.Unjudged, ", "))
	}
	for _, r := range gc.Verify {
		status := "ok"
		if !r.OK {
			status = "fail"
		}
		_, _ = fmt.Fprintf(w, "  verify %s: %s\n", status, r.Command)
	}
	_, _ = fmt.Fprintf(w, "  ok: %v\n", gc.OK)
}
