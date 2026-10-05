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
	"time"

	"github.com/schuettc/tackle/internal/sift/check"
	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/host"
	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tackle/internal/sift/store"
	tools "github.com/schuettc/tools-common"
)

// lookPath is a test seam: finding gh.
var lookPath = exec.LookPath

var checkFlags = flags("check", "sift check [--json]",
	"Audits the instruction files: each enabled profile's global file and skills, and every\n"+
		"instruction file and skill under the configured roots, with repos read at their fetched\n"+
		"base (origin/<base>, else origin/HEAD, else HEAD), not the working tree. Runs the checks\n"+
		"(size, load-limit, duplicate, dead-path, stale-status, retired-store, misplaced,\n"+
		"negative-rule, secret), leaves out rows you muted, records the round, and prints its rows.\n"+
		"With gh on PATH, PR and issue references are looked up. Exit 0 with no findings, 1 with\n"+
		"findings, 2 on error.",
	func(fs *flag.FlagSet) {
		fs.Bool("json", false, "print the round as JSON")
	})

// report is what check prints with --json.
type report struct {
	Round    int64          `json:"round"`
	Kind     string         `json:"kind"`
	At       time.Time      `json:"at"`
	Files    int            `json:"files"`
	Repos    int            `json:"repos"`
	Copies   []copyJSON     `json:"copies"`
	Warnings []string       `json:"warnings"`
	Muted    int            `json:"muted"`
	Summary  map[string]int `json:"summary"`
	Rows     []row.Row      `json:"rows"`
}

type copyJSON struct {
	Path string `json:"path"`
	Of   string `json:"of"`
}

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
	profiles, err := cfg.Enabled()
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	found, err := discover.Run(ctx, discover.Options{Profiles: profiles, Roots: cfg.Roots})
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	in := &check.Input{
		Files: found.Files, Chains: found.Chains, Repos: found.Repos, Config: cfg,
		Host: host.Detect(lookPath, host.Exec), Now: time.Now(),
	}
	rows := check.Run(ctx, in)

	rep := report{Kind: "on-demand", At: in.Now, Files: len(found.Files), Repos: len(found.Repos),
		Copies: []copyJSON{}, Warnings: append([]string{}, found.Warnings...), Summary: map[string]int{}}
	for _, c := range found.Copies {
		rep.Copies = append(rep.Copies, copyJSON(c))
	}
	s, err := store.Open(ctx, store.Path())
	if err != nil {
		_, _ = fmt.Fprintf(errw, "sift: warning: the round is not recorded: %v\n", err)
	} else {
		defer func() { _ = s.Close() }()
		if rows, rep.Muted, err = s.Unmuted(ctx, rows); err != nil {
			return tools.Exitf(2, "%v", err)
		}
	}
	for _, r := range rows {
		rep.Summary[r.Check]++
	}
	if s != nil {
		if rep.Round, err = s.RecordRound(ctx, store.Round{Kind: rep.Kind, At: rep.At, Summary: rep.Summary}, rows); err != nil {
			_, _ = fmt.Fprintf(errw, "sift: warning: the round is not recorded: %v\n", err)
		}
	}
	rep.Rows = append([]row.Row{}, rows...)

	if boolFlag(fs, "json") {
		if err := tools.PrintJSON(out, rep); err != nil {
			return err
		}
	} else {
		writeTable(out, rep)
	}
	if len(rows) > 0 {
		return tools.Exitf(1, "%d finding(s) in round %d", len(rows), rep.Round)
	}
	return nil
}

// writeTable prints the rows grouped by file, then a summary.
func writeTable(w io.Writer, rep report) {
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
	for _, c := range checks {
		_, _ = fmt.Fprintf(w, "  %-14s %d\n", c, rep.Summary[c])
	}
	if len(rep.Rows) == 0 {
		_, _ = fmt.Fprintln(w, "no findings")
	} else {
		_, _ = fmt.Fprintln(w, "  (! = certain: sift applies these itself)")
	}
}
