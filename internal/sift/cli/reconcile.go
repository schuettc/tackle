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
	"Holds each branch sift apply wrote for the round against the files approved for its repo:\n"+
		"each file apply wrote must be on the branch exactly as approved (changed or missing\n"+
		"otherwise), and any other file the branch changes is extra. Run it after a writer or\n"+
		"reviewer changed a branch. Exit 0 everything matches, 1 something does not, 2 error.",
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
			_, _ = fmt.Fprintf(out, "%s %s (from %s): %d file(s), %d problem(s)\n", r.Repo, r.Branch, r.Base, len(r.Files), r.Problems())
			for _, x := range r.Files {
				if x.State != "ok" {
					_, _ = fmt.Fprintf(out, "  %-8s %s: %s\n", x.State, x.Path, x.Detail)
				}
			}
			for _, p := range r.Extra {
				_, _ = fmt.Fprintf(out, "  extra    %s: changed on the branch, approved nowhere\n", p)
			}
		}
	}
	if problems > 0 {
		return tools.Exitf(1, "%d problem(s)", problems)
	}
	return nil
}
