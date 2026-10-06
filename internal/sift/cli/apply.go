package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/schuettc/tackle/internal/sift/apply"
	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/store"
	tools "github.com/schuettc/tools-common"
)

// ghRunner is a test seam: how gh is run.
var ghRunner apply.Runner = apply.Gh

var applyFlags = flags("apply", "sift apply [--round N] [--dry-run] [--json]",
	"Applies the round: each file accepted or edited and sent from the review page is written\n"+
		"whole (the recommendation, or your edit), as one branch per repo (sift/round-N) cut from the\n"+
		"fetched base in a worktree, so the primary clone's checkout is left alone. A file whose base\n"+
		"changed since the audit is held, and so are the files linked to it. A repo whose primary\n"+
		"clone has uncommitted or unpushed work, or an untracked instruction file, is held, with the\n"+
		"reason. Paths are confined to the repo: no .., no .git, no symlinks, and every write goes\n"+
		"through an os.Root on the worktree. With gh and a GitHub remote the branch is pushed and a\n"+
		"pull request opened (gh pr create --body-file); otherwise the committed branch is left. A\n"+
		"file read from disk whose real path is tracked in a git repo (a symlinked skill, say) goes on\n"+
		"that repo's branch; one outside every repo is left to you, its approved content saved under\n"+
		"sift's state; a backlog round's approved rows are left to the agent. Exit 0 all applied, 1\n"+
		"something was held, skipped or failed, 2 error.",
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
	// The enabled profiles' instruction names (with no config: every
	// shipped profile's).
	if cfg, err := config.Load(config.Path()); err == nil {
		o.Repos = cfg.Repos
		if ps, err := cfg.Enabled(); err == nil {
			o.Instructions = apply.InstructionNames(ps)
		}
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
		return tools.Exitf(1, "%d repo(s) or file(s) need you", trouble)
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
			_, _ = fmt.Fprintf(w, "  written  %-6s %s\n", it.Action, it.Where)
		}
		for _, it := range r.Skipped {
			_, _ = fmt.Fprintf(w, "  held     %-6s %s: %s\n", it.Action, it.Where, it.Why)
		}
	}
	if res.Unsent > 0 {
		_, _ = fmt.Fprintf(w, "\n%d decision(s) not sent yet: press Send on the page to include them\n", res.Unsent)
	}
	if len(res.Left) > 0 {
		_, _ = fmt.Fprintln(w, "\nleft for you:")
		for _, it := range res.Left {
			_, _ = fmt.Fprintf(w, "  %-12s %s: %s\n", it.Action, it.Where, it.Why)
			if it.Approved != "" {
				_, _ = fmt.Fprintf(w, "               approved content: %s\n", it.Approved)
			}
		}
	}
}
