package cli

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/schuettc/tackle/internal/sift/reconcile"
	"github.com/schuettc/tackle/internal/sift/store"
	tools "github.com/schuettc/tools-common"
)

var reconcileFlags = flags("reconcile", "sift reconcile [--round N] [--json]",
	"Holds each branch sift apply wrote for the round against the rows approved for its repo,\n"+
		"by content: a row whose change is missing from the branch, a row narrowed (its approved\n"+
		"text not on the branch verbatim, or its passage only partly removed), and hunks no approved\n"+
		"row accounts for (extra). Run it after a writer or reviewer changed a branch.\n"+
		"Exit 0 everything matches, 1 something does not, 2 error.",
	func(fs *flag.FlagSet) {
		fs.Int64("round", 0, "the round (default: the latest)")
		fs.Bool("json", false, "print the reports as JSON")
	})

func runReconcile(args []string, out, errw io.Writer) error {
	fs := reconcileFlags()
	pos, err := parse(fs, args, out)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return tools.UsageError{Msg: "reconcile takes no arguments"}
	}
	ctx := context.Background()
	s, err := store.Open(ctx, store.Path())
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	defer func() { _ = s.Close() }()
	round, reps, err := reconcile.Run(ctx, s, fs.Lookup("round").Value.(flag.Getter).Get().(int64))
	if errors.Is(err, sql.ErrNoRows) {
		return tools.Exitf(2, "no such round").WithHint("sift check")
	}
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	if reps == nil {
		reps = []reconcile.Report{}
	}
	problems := 0
	for _, r := range reps {
		problems += r.Problems()
	}
	if boolFlag(fs, "json") {
		if err := tools.PrintJSON(out, map[string]any{"round": round, "repos": reps}); err != nil {
			return err
		}
	} else {
		if len(reps) == 0 {
			_, _ = fmt.Fprintf(out, "round %d: no branch to reconcile (run sift apply)\n", round)
		}
		for _, r := range reps {
			_, _ = fmt.Fprintf(out, "%s %s (from %s): %d row(s), %d problem(s)\n", r.Repo, r.Branch, r.Base, len(r.Rows), r.Problems())
			for _, x := range r.Rows {
				if x.State != "ok" {
					_, _ = fmt.Fprintf(out, "  %-9s %-16s %-12s %s: %s\n", x.State, x.Row, x.Verdict, x.Where, x.Detail)
				}
			}
			for _, x := range r.Extra {
				_, _ = fmt.Fprintf(out, "  extra     %s %s\n", x.File, x.Header)
				for _, l := range x.Lines {
					_, _ = fmt.Fprintf(out, "            %s\n", l)
				}
			}
		}
	}
	if problems > 0 {
		return tools.Exitf(1, "%d problem(s)", problems)
	}
	return nil
}
