package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sort"

	"github.com/schuettc/tackle/internal/sift/audit"
	tools "github.com/schuettc/tools-common"
)

// lookPath is a test seam: finding gh.
var lookPath = exec.LookPath

var checkFlags = flags("check", "sift check [--json]",
	"Audits the instruction files: each enabled profile's global file and skills, and every\n"+
		"instruction file and skill under the configured roots, with repos read at their fetched\n"+
		"base (origin/<base>, else origin/HEAD, else HEAD), not the working tree. Runs the checks\n"+
		"(size, load-limit, duplicate, dead-path, stale-status, retired-store, misplaced, secret),\n"+
		"leaves out rows you muted, records the round and its files, and prints its rows. Forks and\n"+
		"vendor-managed skill directories (Claude Code's synced skills and plugins) are skipped\n"+
		"unless the config's [include] turns them on. The round then waits for a recommendation per\n"+
		"file (sift next, sift propose). With gh on PATH, PR and issue references are looked up.\n"+
		"Exit 0 with no findings, 1 with findings, 2 on error.",
	func(fs *flag.FlagSet) {
		fs.Bool("json", false, "print the round as JSON")
	})

func runCheck(args []string, out, errw io.Writer) error {
	fs := checkFlags()
	pos, err := parse(fs, args, out)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return tools.UsageError{Msg: "check takes no arguments"}
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	rep, err := audit.Run(ctx, audit.Options{Config: cfg, LookPath: lookPath, Warn: errw})
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	if boolFlag(fs, "json") {
		if err := tools.PrintJSON(out, rep); err != nil {
			return err
		}
	} else {
		writeTable(out, rep)
	}
	if len(rep.Rows) > 0 {
		return tools.Exitf(1, "%d finding(s) in round %d", len(rep.Rows), rep.Round)
	}
	return nil
}

// writeTable prints the rows grouped by file, then a summary.
func writeTable(w io.Writer, rep audit.Report) {
	file := ""
	for _, r := range rep.Rows {
		if r.Source.File != file {
			file = r.Source.File
			_, _ = fmt.Fprintf(w, "\n%s\n", file)
		}
		at := "      "
		if r.Source.Start > 0 {
			at = fmt.Sprintf("%5d ", r.Source.Start)
		}
		mark := " "
		if r.Certain {
			mark = "!"
		}
		_, _ = fmt.Fprintf(w, "%s%s %-14s %s\n", at, mark, r.Check, r.Summary)
	}
	for _, warn := range rep.Warnings {
		_, _ = fmt.Fprintf(w, "warning: %s\n", warn)
	}
	checks := make([]string, 0, len(rep.Summary))
	for c := range rep.Summary {
		checks = append(checks, c)
	}
	sort.Strings(checks)
	_, _ = fmt.Fprintf(w, "\n%d file(s) in %d repo(s), %d installed cop(ies) counted at their source, %d muted\n",
		rep.Files, rep.Repos, len(rep.Copies), rep.Muted)
	if len(rep.Skipped) > 0 {
		n := map[string]int{}
		for _, s := range rep.Skipped {
			n[s.Why]++
		}
		_, _ = fmt.Fprintf(w, "skipped as another project's: %d fork(s), %d vendor-managed skill(s) (config [include] audits them)\n",
			n["fork"], n["vendor"])
	}
	for _, c := range checks {
		_, _ = fmt.Fprintf(w, "  %-14s %d\n", c, rep.Summary[c])
	}
	if len(rep.Rows) == 0 {
		_, _ = fmt.Fprintln(w, "no findings")
	} else {
		_, _ = fmt.Fprintln(w, "  (! = certain: the file's recommendation must fix it)\nnext: recommend each file (sift next, sift propose), then sift serve")
	}
}
