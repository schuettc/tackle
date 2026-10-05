package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/schuettc/tackle/internal/sift/apply"
	"github.com/schuettc/tackle/internal/sift/store"
	tools "github.com/schuettc/tools-common"
)

// ghRunner is a test seam: how gh is run.
var ghRunner apply.Runner = apply.Gh

var applyFlags = flags("apply", "sift apply [--round N] [--dry-run] [--json]",
	"Applies the round: the certain fixes and the rows accepted or edited and sent from the review\n"+
		"page (delete, rewrite, move, merge), as one branch per repo (sift/round-N) cut from the fetched\n"+
		"base in a worktree, so the primary clone's checkout is left alone. A repo whose primary\n"+
		"clone has uncommitted or unpushed work is held, with the reason. With gh and a GitHub\n"+
		"remote the branch is pushed and a pull request opened (gh pr create --body-file);\n"+
		"otherwise the committed branch is left. Prints what it did per repo, and the approved rows\n"+
		"it leaves to you. Exit 0 all applied, 1 something was held, skipped or failed, 2 error.",
	func(fs *flag.FlagSet) {
		fs.Int64("round", 0, "the round to apply (default: the latest)")
		fs.Bool("dry-run", false, "work out what would change and touch nothing")
		fs.Bool("json", false, "print the result as JSON")
	})

func runApply(args []string, out, errw io.Writer) error {
	fs := applyFlags()
	pos, err := parse(fs, args, out)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return tools.UsageError{Msg: "apply takes no arguments"}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	s, err := store.Open(ctx, store.Path())
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	defer func() { _ = s.Close() }()
	o := apply.Options{Store: s, Round: fs.Lookup("round").Value.(flag.Getter).Get().(int64), DryRun: boolFlag(fs, "dry-run")}
	if _, err := lookPath("gh"); err == nil {
		o.Gh = ghRunner
	}
	res, err := apply.Run(ctx, o)
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	if boolFlag(fs, "json") {
		if err := tools.PrintJSON(out, res); err != nil {
			return err
		}
	} else {
		writeApply(out, res)
	}
	trouble := 0
	for _, r := range res.Repos {
		if r.State == "held" || r.State == "failed" || r.State == "nothing" {
			trouble++
		}
		trouble += len(r.Skipped)
	}
	if trouble > 0 {
		return tools.Exitf(1, "%d repo(s) or row(s) need you", trouble)
	}
	return nil
}

func writeApply(w io.Writer, res apply.Result) {
	_, _ = fmt.Fprintf(w, "round %d\n", res.Round)
	if len(res.Repos) == 0 {
		_, _ = fmt.Fprintln(w, "no repo to change")
	}
	for _, r := range res.Repos {
		head := r.State
		switch r.State {
		case "pr":
			head = "pull request " + r.PR
		case "branch", "planned":
			head = r.State + " " + r.Branch
		}
		_, _ = fmt.Fprintf(w, "\n%s: %s (%d applied, %d skipped)\n", r.Path, head, len(r.Applied), len(r.Skipped))
		if r.Detail != "" {
			_, _ = fmt.Fprintf(w, "  %s\n", r.Detail)
		}
		for _, it := range r.Applied {
			_, _ = fmt.Fprintf(w, "  applied  %-16s %-14s %s\n", it.Row, it.Verdict, it.Where)
		}
		for _, it := range r.Skipped {
			_, _ = fmt.Fprintf(w, "  skipped  %-16s %-14s %s: %s\n", it.Row, it.Verdict, it.Where, it.Why)
		}
	}
	if res.Unsent > 0 {
		_, _ = fmt.Fprintf(w, "\n%d decided row(s) not sent yet: press Send on the page to include them\n", res.Unsent)
	}
	if len(res.Left) > 0 {
		_, _ = fmt.Fprintln(w, "\nleft for you:")
		for _, it := range res.Left {
			_, _ = fmt.Fprintf(w, "  %-16s %-14s %s: %s\n", it.Row, it.Verdict, it.Where, it.Why)
		}
	}
}
