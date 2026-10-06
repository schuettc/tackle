package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/schuettc/tackle/internal/sift/clean"
	"github.com/schuettc/tackle/internal/sift/store"
	tools "github.com/schuettc/tools-common"
)

var cleanFlags = flags("clean", "sift clean [--dry-run] [--json]",
	"Removes what sift apply recorded creating, and nothing else: each round branch, local and\n"+
		"remote, once its pull request is merged or closed (checked with gh; without gh, or with no\n"+
		"pull request, once its commit is in the base), and any worktree apply left behind. A\n"+
		"branch now at another commit than the one apply made is kept. Nothing is chosen by name or\n"+
		"author. Prints the plan first, then removes; --dry-run prints the plan only. Exit 0 done,\n"+
		"1 a removal failed, 2 error.",
	func(fs *flag.FlagSet) {
		fs.Bool("dry-run", false, "print the plan and remove nothing")
		fs.Bool("json", false, "print the result as JSON")
	})

func runClean(args []string, out, errw io.Writer) error {
	fs := cleanFlags()
	pos, err := parse(fs, args, out)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return tools.UsageError{Msg: "clean takes no arguments"}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	s, err := store.Open(ctx, store.Path())
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	defer func() { _ = s.Close() }()
	o := clean.Options{Store: s, DryRun: boolFlag(fs, "dry-run")}
	if _, err := lookPath("gh"); err == nil {
		o.Gh = ghRunner
	}
	steps, err := clean.Plan(ctx, o)
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	asJSON := boolFlag(fs, "json")
	if !asJSON {
		writeCleanPlan(out, steps, o.DryRun)
	}
	if !o.DryRun {
		steps = clean.Do(ctx, o, steps)
	}
	if asJSON {
		return tools.PrintJSON(out, map[string]any{"dry_run": o.DryRun, "steps": nonNil(steps)})
	}
	failed := 0
	if !o.DryRun {
		for _, s := range steps {
			switch {
			case s.Done:
				_, _ = fmt.Fprintf(out, "removed  %s %s (%s)\n", s.Kind, s.Name, s.Repo)
			case s.Error != "":
				failed++
				_, _ = fmt.Fprintf(out, "failed   %s %s (%s): %s\n", s.Kind, s.Name, s.Repo, s.Error)
			}
		}
	}
	if failed > 0 {
		return tools.Exitf(1, "%d removal(s) failed", failed)
	}
	return nil
}

func nonNil(steps []clean.Step) []clean.Step {
	if steps == nil {
		return []clean.Step{}
	}
	return steps
}

func writeCleanPlan(w io.Writer, steps []clean.Step, dry bool) {
	if len(steps) == 0 {
		_, _ = fmt.Fprintln(w, "nothing sift created is left to clean")
		return
	}
	head := "plan:"
	if dry {
		head = "plan (dry run: nothing is removed):"
	}
	_, _ = fmt.Fprintln(w, head)
	for _, s := range steps {
		what := ""
		if s.Kind == "branch" {
			switch {
			case s.Local && s.Remote:
				what = " [local and origin]"
			case s.Local:
				what = " [local]"
			case s.Remote:
				what = " [origin]"
			}
		}
		_, _ = fmt.Fprintf(w, "  %-6s %-8s %s%s in %s: %s\n", s.Action, s.Kind, s.Name, what, s.Repo, s.Why)
		if s.PR != "" {
			_, _ = fmt.Fprintf(w, "                  %s\n", s.PR)
		}
	}
}
