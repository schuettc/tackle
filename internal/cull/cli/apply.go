package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/cull/apply"
	"github.com/schuettc/tackle/internal/cull/check"
	"github.com/schuettc/tackle/internal/cull/discover"
	"github.com/schuettc/tackle/internal/cull/verify"
	tools "github.com/schuettc/tools-common"
)

// idList is a repeatable string flag: each --ids adds one id, verbatim
// (ids are never split on commas; test names may contain them).
type idList []string

func (l *idList) String() string     { return strings.Join(*l, " ") }
func (l *idList) Set(v string) error { *l = append(*l, v); return nil }

var applyFlags = flags("apply", "cull apply --ids <id>… | --verdict cut [--no-verify] [--timeout 15m] [--json]",
	"Remove top-level tests from the project in the current directory, using the report the\n"+
		"last `cull check` wrote (.cull/last.json): the named --ids, or every test judged cut\n"+
		"(--verdict cut). Refuses files changed since check. Tidies now-unused imports, reports\n"+
		"(never deletes) helpers left unused, and runs the project's tests (test_command in\n"+
		".cull.toml, or detected) before and after. If they fail after, every file is restored\n"+
		"byte-for-byte from the snapshot in .cull/rollback/. Nothing is committed.\n"+
		"Exit: 0 applied (and verified), 1 tests failed and rolled back, 2 refused or error.",
	func(fs *flag.FlagSet) {
		fs.Var(&idList{}, "ids", "test id to remove (repeatable; further positional args are more ids)")
		fs.String("verdict", "", "remove every top-level test with this verdict (only \"cut\")")
		fs.Bool("no-verify", false, "skip the before/after test runs")
		fs.Duration("timeout", apply.DefaultTimeout, "per test command timeout")
		fs.Bool("json", false, "print the outcome as JSON")
	})

func runApply(args []string, out, errw io.Writer) error {
	fs := applyFlags()
	pos, err := parse(fs, args, out)
	if err != nil {
		return err
	}
	ids := append([]string(nil), *fs.Lookup("ids").Value.(*idList)...)
	verdict := str(fs, "verdict")
	switch {
	case len(ids) == 0 && len(pos) > 0:
		return tools.UsageError{Msg: "unexpected arguments; pass test ids with --ids"}
	case verdict != "" && verdict != "cut":
		return tools.UsageError{Msg: fmt.Sprintf("--verdict must be cut (got %q)", verdict)}
	case len(ids) > 0 && verdict != "":
		return tools.UsageError{Msg: "pass --ids or --verdict cut, not both"}
	case len(ids) == 0 && verdict == "":
		return tools.UsageError{Msg: "pass --ids <id>… or --verdict cut"}
	}
	ids = append(ids, pos...)
	timeout := fs.Lookup("timeout").Value.(flag.Getter).Get().(time.Duration)
	if timeout <= 0 {
		return tools.UsageError{Msg: "--timeout must be positive"}
	}

	root, err := discover.Root(".")
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	cfg, _, err := check.LoadConfig(root)
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}

	jsonMode := boolFlag(fs, "json")
	var progress io.Writer = errw
	if jsonMode {
		progress = nil // keep stderr to the single JSON error envelope
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	outcome, runErr := apply.Run(ctx, apply.Options{
		Root: root, IDs: ids, VerdictCut: verdict == "cut", NoVerify: boolFlag(fs, "no-verify"),
		Timeout: timeout, TestCommand: cfg.TestCommand, Stderr: progress,
	})

	if jsonMode {
		if err := tools.PrintJSON(out, outcome); err != nil {
			return err
		}
	} else {
		writeOutcome(out, outcome)
	}

	if runErr != nil {
		var ee *apply.ExitError
		if errors.As(runErr, &ee) {
			return tools.Exitf(ee.Code, "%s", ee.Msg)
		}
		return tools.Exitf(2, "%v", runErr)
	}
	return nil
}

// writeOutcome prints the human-readable apply outcome: only the sections
// that have something in them.
func writeOutcome(w io.Writer, o apply.Outcome) {
	if len(o.Refused) > 0 {
		fmt.Fprintf(w, "refused (%d):\n", len(o.Refused))
		for _, r := range o.Refused {
			fmt.Fprintf(w, "  %s: %s\n", r.ID, r.Reason)
		}
	}
	if len(o.NeedsAgent) > 0 {
		fmt.Fprintf(w, "needs an agent (inside another test; edit by hand, then run cull check) (%d):\n", len(o.NeedsAgent))
		for _, id := range o.NeedsAgent {
			fmt.Fprintf(w, "  %s\n", id)
		}
	}
	writeResults(w, "baseline tests", o.Baseline)
	if len(o.Applied) > 0 {
		label := "applied"
		switch {
		case o.RolledBack:
			label = "attempted (rolled back)"
		case o.RollbackFailed:
			label = "attempted (ROLLBACK FAILED)"
		}
		fmt.Fprintf(w, "%s (%d):\n", label, len(o.Applied))
		for _, id := range o.Applied {
			fmt.Fprintf(w, "  %s\n", id)
		}
		fmt.Fprintf(w, "files (%d):\n", len(o.Files))
		for _, f := range o.Files {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}
	writeByFile(w, "imports removed", o.Files, o.ImportsRemoved)
	writeByFile(w, "orphaned helpers (reported, not deleted)", o.Files, o.OrphanedHelpers)
	writeResults(w, "tests after removal", o.After)
	if o.RolledBack {
		fmt.Fprintln(w, "rolled back: every file restored to its pre-edit bytes")
	}
	if o.RollbackFailed {
		fmt.Fprintf(w, "ROLLBACK FAILED: files were NOT fully restored; these may still be edited (%d):\n", len(o.Files))
		for _, f := range o.Files {
			fmt.Fprintf(w, "  %s\n", f)
		}
		fmt.Fprintf(w, "copy the pre-edit files back by hand from %s\n", o.Snapshot)
	}
	if o.Snapshot != "" {
		fmt.Fprintf(w, "snapshot: %s\n", o.Snapshot)
	}
	if len(o.Applied) == 0 && len(o.Refused) == 0 && len(o.NeedsAgent) == 0 {
		fmt.Fprintln(w, "nothing to apply")
	}
}

func writeByFile(w io.Writer, heading string, files []string, m map[string][]string) {
	if len(m) == 0 {
		return
	}
	fmt.Fprintf(w, "%s:\n", heading)
	for _, f := range files {
		if names := m[f]; len(names) > 0 {
			fmt.Fprintf(w, "  %s: %s\n", f, strings.Join(names, ", "))
		}
	}
}

func writeResults(w io.Writer, heading string, rs []verify.Result) {
	if len(rs) == 0 {
		return
	}
	fmt.Fprintf(w, "%s:\n", heading)
	for _, r := range rs {
		switch {
		case r.OK:
			fmt.Fprintf(w, "  ok    %s\n", r.Command)
		case r.TimedOut:
			fmt.Fprintf(w, "  FAIL  %s (timed out)\n", r.Command)
		default:
			fmt.Fprintf(w, "  FAIL  %s (exit %d)\n", r.Command, r.ExitCode)
		}
		if !r.OK && r.OutputTail != "" {
			for _, line := range strings.Split(strings.TrimRight(r.OutputTail, "\n"), "\n") {
				fmt.Fprintf(w, "        %s\n", line)
			}
		}
	}
}
