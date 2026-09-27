package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/cull/corpus"
	"github.com/schuettc/tackle/internal/cull/eval"
	"github.com/schuettc/tackle/internal/cull/jev"
	"github.com/schuettc/tackle/internal/cull/judge"
	"github.com/schuettc/tackle/internal/cull/policy"
	"github.com/schuettc/tackle/internal/cull/rubric"
	tools "github.com/schuettc/tools-common"
)

const endpoint = "https://api.typesafe.ai"

var (
	importFlags = flags("corpus import", "cull corpus import <cases.jsonl> [--repo name]",
		"Add tests (TestCase JSONL) to the corpus. A test whose source changed loses its label.",
		func(fs *flag.FlagSet) { fs.String("repo", "", "repo name recorded on new entries") })
	sheetFlags = flags("corpus sheet", "cull corpus sheet [--unlabeled] [--split dev|holdout|all]",
		"Write a markdown labeling sheet to stdout. Fill in label: keep|cut|review, then run cull label --sheet.",
		func(fs *flag.FlagSet) {
			fs.Bool("unlabeled", false, "only entries without a label")
			fs.String("split", "dev", "dev, holdout or all")
		})
	statsFlags = flags("corpus stats", "cull corpus stats [--json]", "Count entries by split and label.",
		func(fs *flag.FlagSet) { fs.Bool("json", false, "JSON output") })
	labelFlags = flags("label", "cull label <id> keep|cut|review [--note s] | cull label --sheet <file>",
		"Label corpus entries one at a time, or all at once from a filled-in sheet (all or nothing).",
		func(fs *flag.FlagSet) {
			fs.String("note", "", "why (single label)")
			fs.String("by", os.Getenv("USER"), "who is labeling")
			fs.String("sheet", "", "labeling sheet to read")
		})
	evalFlags = flags("eval", "cull eval [--rubric v1|path] [--against v1|path] [--split dev|holdout|all] --egress",
		"Judge every labeled corpus entry with Jev, apply the rubric's policy, and score predictions against labels.\n"+
			"Sends test source to "+endpoint+": requires --egress. The key comes from TYPESAFE_API_KEY:\n"+
			"  creel exec TYPESAFE_API_KEY -- cull eval --egress",
		func(fs *flag.FlagSet) {
			fs.String("rubric", rubric.Default, "rubric version (v<N>) or file")
			fs.String("against", "", "baseline rubric to compare with")
			fs.String("split", "dev", "dev, holdout or all")
			fs.String("model", "jev-latest", "TypeSafe model")
			fs.Bool("refresh", false, "ignore cached answers")
			fs.Int("concurrency", 6, "parallel Jev calls")
			fs.Bool("egress", false, "allow sending test source to TypeSafe")
			fs.Bool("dry-run", false, "print each state that would be sent; send nothing")
			fs.Bool("json", false, "JSON output")
		})
)

func commands() []tools.Command {
	return []tools.Command{
		{
			Name: "corpus", Group: "corpus", Synopsis: "import|sheet|stats ...", Summary: "manage the labeled corpus",
			Help: "cull corpus import <cases.jsonl> [--repo name]\ncull corpus sheet [--unlabeled] [--split dev|holdout|all]\ncull corpus stats [--json]",
			Run:  runCorpus,
		},
		{Name: "label", Group: "corpus", Synopsis: "<id> keep|cut|review | --sheet <file>", Summary: "label corpus entries", NewFlags: labelFlags, Run: runLabel},
		{Name: "eval", Group: "eval", Summary: "score a rubric against the labeled corpus", NewFlags: evalFlags, Run: runEval},
	}
}

func openStore() (*corpus.Store, error) { return corpus.Open(corpus.DefaultPath()) }

func runCorpus(args []string, out, errw io.Writer) error {
	if len(args) == 0 {
		return tools.UsageError{Msg: "corpus needs import, sheet or stats"}
	}
	sub, args := args[0], args[1:]
	switch sub {
	case "import":
		fs := importFlags()
		pos, err := parse(fs, args, out)
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return tools.UsageError{Msg: "corpus import takes one cases.jsonl file"}
		}
		f, err := os.Open(pos[0])
		if err != nil {
			return err
		}
		defer f.Close()
		tcs, err := corpus.ReadCases(f)
		if err != nil {
			return fmt.Errorf("%s: %w", pos[0], err)
		}
		if repo := str(fs, "repo"); repo != "" {
			for i := range tcs {
				if tcs[i].Repo == "" {
					tcs[i].Repo = repo
				}
			}
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		r := s.Import(tcs)
		if err := s.Save(); err != nil {
			return err
		}
		fmt.Fprintf(out, "added %d, updated %d (labels cleared), unchanged %d\n", r.Added, r.Updated, r.Unchanged)
		return nil
	case "sheet":
		fs := sheetFlags()
		if _, err := parse(fs, args, out); err != nil {
			return err
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		entries, err := inSplit(s.Entries(), str(fs, "split"))
		if err != nil {
			return err
		}
		if boolFlag(fs, "unlabeled") {
			var keep []corpus.Entry
			for _, e := range entries {
				if e.Label == "" {
					keep = append(keep, e)
				}
			}
			entries = keep
		}
		return corpus.WriteSheet(out, entries)
	case "stats":
		fs := statsFlags()
		if _, err := parse(fs, args, out); err != nil {
			return err
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		stats := map[string]map[string]int{"dev": {}, "holdout": {}}
		for _, e := range s.Entries() {
			l := string(e.Label)
			if l == "" {
				l = "unlabeled"
			}
			stats[e.Split][l]++
		}
		if boolFlag(fs, "json") {
			return tools.PrintJSON(out, stats)
		}
		for _, split := range []string{"dev", "holdout"} {
			st := stats[split]
			fmt.Fprintf(out, "%-8s keep %d  cut %d  review %d  unlabeled %d\n", split, st["keep"], st["cut"], st["review"], st["unlabeled"])
		}
		return nil
	}
	return tools.UsageError{Msg: fmt.Sprintf("unknown corpus command %q (import, sheet, stats)", sub)}
}

func inSplit(entries []corpus.Entry, split string) ([]corpus.Entry, error) {
	switch split {
	case "all":
		return entries, nil
	case "dev", "holdout":
		var out []corpus.Entry
		for _, e := range entries {
			if e.Split == split {
				out = append(out, e)
			}
		}
		return out, nil
	}
	return nil, tools.UsageError{Msg: fmt.Sprintf("--split %q: want dev, holdout or all", split)}
}

func runLabel(args []string, out, errw io.Writer) error {
	fs := labelFlags()
	pos, err := parse(fs, args, out)
	if err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	by, now := str(fs, "by"), time.Now()
	if sheet := str(fs, "sheet"); sheet != "" {
		f, err := os.Open(sheet)
		if err != nil {
			return err
		}
		labels, err := corpus.ReadSheet(f)
		f.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", sheet, err)
		}
		// ApplySheet is all or nothing and nothing is saved on failure.
		if err := s.ApplySheet(labels, by, now); err != nil {
			return err
		}
		if err := s.Save(); err != nil {
			return err
		}
		fmt.Fprintf(out, "labeled %d\n", len(labels))
		return nil
	}
	if len(pos) != 2 {
		return tools.UsageError{Msg: "label takes <id> keep|cut|review, or --sheet <file>"}
	}
	v, err := policy.ParseVerdict(pos[1])
	if err != nil {
		return tools.UsageError{Msg: err.Error()}
	}
	if err := s.Label(pos[0], v, by, str(fs, "note"), now); err != nil {
		return err
	}
	if err := s.Save(); err != nil {
		return err
	}
	fmt.Fprintln(out, "labeled 1")
	return nil
}

type evalOutput struct {
	Rubric         string        `json:"rubric"`
	Against        string        `json:"against,omitempty"`
	Split          string        `json:"split"`
	Metrics        eval.Metrics  `json:"metrics"`
	AgainstMetrics *eval.Metrics `json:"against_metrics,omitempty"`
	Changes        []eval.Change `json:"changes,omitempty"`
	Rows           []eval.Row    `json:"rows"`
}

func runEval(args []string, out, errw io.Writer) error {
	fs := evalFlags()
	if _, err := parse(fs, args, out); err != nil {
		return err
	}
	split := str(fs, "split")
	s, err := openStore()
	if err != nil {
		return err
	}
	entries, err := inSplit(s.Entries(), split)
	if err != nil {
		return err
	}
	if split == "holdout" {
		fmt.Fprintln(errw, "warning: holdout is for the final check; do not tune rubrics on it")
	}
	r, err := rubric.Load(str(fs, "rubric"))
	if err != nil {
		return err
	}
	var base *rubric.Rubric
	if a := str(fs, "against"); a != "" {
		b, err := rubric.Load(a)
		if err != nil {
			return err
		}
		base = &b
	}

	if boolFlag(fs, "dry-run") {
		for _, e := range entries {
			if e.Label != "" {
				if err := tools.PrintJSON(out, map[string]any{"id": e.ID, "state": e.State}); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if !boolFlag(fs, "egress") {
		return tools.Exitf(2, "eval sends test source to %s; pass --egress to allow it (or --dry-run to see what would be sent)", endpoint)
	}
	key := os.Getenv("TYPESAFE_API_KEY")
	if key == "" {
		return tools.Exitf(2, "TYPESAFE_API_KEY is not set").WithHint("creel exec TYPESAFE_API_KEY -- cull eval --egress ...")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	conc, _ := strconv.Atoi(str(fs, "concurrency"))
	opt := judge.Options{
		Model: str(fs, "model"), Rubric: r, Concurrency: conc, Refresh: boolFlag(fs, "refresh"),
		Cache: &judge.Cache{Dir: filepath.Join(tools.CacheDir("cull"), "answers")},
	}
	ev := jev.NewClient(key)
	res := evalOutput{Rubric: r.Version, Split: split}
	if res.Rows, err = eval.Run(ctx, ev, entries, opt); err != nil {
		return evalErr(err)
	}
	res.Metrics = eval.Score(res.Rows)
	if base != nil {
		opt.Rubric = *base
		baseRows, err := eval.Run(ctx, ev, entries, opt)
		if err != nil {
			return evalErr(err)
		}
		m := eval.Score(baseRows)
		res.Against, res.AgainstMetrics = base.Version, &m
		res.Changes = eval.Compare(baseRows, res.Rows)
	}
	if boolFlag(fs, "json") {
		return tools.PrintJSON(out, res)
	}
	printEval(out, res)
	return nil
}

func evalErr(err error) error {
	if errors.Is(err, jev.ErrUnauthorized) {
		return tools.Exitf(2, "%v", err).WithHint("creel exec TYPESAFE_API_KEY -- cull eval --egress ...")
	}
	return tools.Exitf(2, "eval stopped: %v (completed answers are cached)", err)
}

func printMetrics(w io.Writer, name, split string, m eval.Metrics) {
	fmt.Fprintf(w, "rubric %s  split %s  scored %d  errors %d  models %s\n", name, split, m.N, m.Errors, strings.Join(m.Models, ","))
	fmt.Fprintf(w, "  false cuts    %.3f (%d/%d keep-labeled)\n", m.FalseCutRate, m.FalseCuts, m.KeepN)
	fmt.Fprintf(w, "  cut precision %.3f (%d/%d)  recall %.3f (%d/%d)\n", m.CutPrecision, m.TruePosCut, m.PredCutN, m.CutRecall, m.TruePosCut, m.CutN)
	fmt.Fprintf(w, "  review agree  %.3f (%d/%d)\n", m.ReviewAgreement, m.ReviewAgree, m.ReviewN)
	vs := []policy.Verdict{policy.Keep, policy.Cut, policy.Review}
	fmt.Fprintf(w, "  label\\pred    keep   cut  review\n")
	for _, l := range vs {
		fmt.Fprintf(w, "  %-12s %5d %5d %7d\n", l, m.Confusion[l][policy.Keep], m.Confusion[l][policy.Cut], m.Confusion[l][policy.Review])
	}
}

func printEval(w io.Writer, res evalOutput) {
	printMetrics(w, res.Rubric, res.Split, res.Metrics)
	if res.AgainstMetrics == nil {
		return
	}
	fmt.Fprintln(w)
	printMetrics(w, res.Against, res.Split, *res.AgainstMetrics)
	fmt.Fprintf(w, "\nfalse cuts %s → %s: %d → %d\n", res.Against, res.Rubric, res.AgainstMetrics.FalseCuts, res.Metrics.FalseCuts)
	fmt.Fprintf(w, "changed: %d\n", len(res.Changes))
	for _, c := range res.Changes {
		fmt.Fprintf(w, "  %s  label %s  %s → %s\n", c.ID, c.Label, c.From, c.To)
	}
}
