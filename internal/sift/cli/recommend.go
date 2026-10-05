package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/recommend"
	"github.com/schuettc/tackle/internal/sift/store"
	tools "github.com/schuettc/tools-common"
)

var nextFlags = flags("next", "sift next [--round N] [--json]",
	"The recommending loop, from a terminal: prints the next file of the round (default the latest)\n"+
		"that has findings and no recommendation yet: its key, where it is, the commit it was read\n"+
		"at, its budget, its content at the audit with that content's base hash, its findings, and\n"+
		"the guidance for writing its recommendation. When every file has one, says the round is\n"+
		"ready for review. Exit 0, or 2 on error (a file that changed since the audit is an error:\n"+
		"run sift check again).",
	func(fs *flag.FlagSet) {
		fs.Int64("round", 0, "the round (default: the latest)")
		fs.Bool("json", false, "print the file as JSON")
	})

var proposeFlags = flags("propose", "sift propose [--round N] < recommendation.json",
	"Stores file recommendations, all or nothing: one JSON object, several (JSON lines), or an\n"+
		"array, each {file, base, content, findings: [{row, did: fixed|kept, how}], links, summary}.\n"+
		"file and links take a key from sift next or the file's path. Refused, with the reason:\n"+
		"a base that is not the file's at the audit, a finding left out or unexplained, a certain\n"+
		"finding kept, a link the other file does not return (recommend linked files in one batch),\n"+
		"content the same as the base while something is fixed, unknown fields. A new recommendation\n"+
		"drops the decisions on its file and the files linked to it. Exit 0 stored, 2 refused.",
	func(fs *flag.FlagSet) {
		fs.Int64("round", 0, "the round (default: the latest)")
	})

func runNext(args []string, out, _ io.Writer) error {
	fs := nextFlags()
	pos, err := parse(fs, args, out)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return tools.UsageError{Msg: "next takes no arguments"}
	}
	ctx := context.Background()
	s, err := store.Open(ctx, store.Path())
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	defer func() { _ = s.Close() }()
	n, err := recommend.Next(ctx, s, fs.Lookup("round").Value.(flag.Getter).Get().(int64))
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	if boolFlag(fs, "json") {
		return tools.PrintJSON(out, n)
	}
	if n.Done {
		_, _ = fmt.Fprintf(out, "round %d: every file has a recommendation: the round is ready for review (sift serve)\n", n.Round)
		return nil
	}
	w := func(format string, a ...any) { _, _ = fmt.Fprintf(out, format, a...) }
	w("round %d: %d file(s) to recommend; this is the first.\n\n", n.Round, n.Left)
	w("file    %s\npath    %s\n", n.File, n.Path)
	if n.Repo != "" {
		w("repo    %s at %s (%s)\n", n.Repo, n.Ref, n.Commit)
	}
	w("class   %s, %d bytes, budget %d\nbase    %s\n\nfindings:\n", n.Class, n.Size, n.Budget, n.Base)
	for _, f := range n.Findings {
		mark := ""
		if f.Certain {
			mark = " (certain: fix it)"
		}
		at := f.Lines
		if at == "" {
			at = "file"
		}
		w("  %s  %-6s %s: %s%s\n", f.Row, at, f.Check, f.Summary, mark)
		if f.Passage != "" {
			w("         %s\n", strings.ReplaceAll(strings.TrimSpace(f.Passage), "\n", "\n         "))
		}
	}
	w("\n%s\n\nTo store it: %s\n\n--- content at the audit ---\n%s", n.Guidance, n.Propose, n.Content)
	if !strings.HasSuffix(n.Content, "\n") {
		w("\n")
	}
	return nil
}

func runPropose(stdin io.Reader) func(args []string, out, errw io.Writer) error {
	return func(args []string, out, _ io.Writer) error {
		fs := proposeFlags()
		pos, err := parse(fs, args, out)
		if err != nil {
			return err
		}
		if len(pos) > 0 {
			return tools.UsageError{Msg: "propose takes no arguments; the recommendation comes on stdin"}
		}
		recs, err := readRecs(stdin)
		if err != nil {
			return tools.Exitf(2, "%v", err)
		}
		ctx := context.Background()
		s, err := store.Open(ctx, store.Path())
		if err != nil {
			return tools.Exitf(2, "%v", err)
		}
		defer func() { _ = s.Close() }()
		res, err := recommend.Propose(ctx, s, fs.Lookup("round").Value.(flag.Getter).Get().(int64), recs)
		if err != nil {
			return tools.Exitf(2, "%v", err)
		}
		_, _ = fmt.Fprintf(out, "%d recommendation(s) stored; %d file(s) left to recommend", res.Stored, res.Left)
		if res.Cleared > 0 {
			_, _ = fmt.Fprintf(out, "; %d decision(s) dropped because their recommendation changed", res.Cleared)
		}
		if res.Left == 0 {
			_, _ = fmt.Fprint(out, "; the round is ready for review")
		}
		_, _ = fmt.Fprintln(out)
		return nil
	}
}

// readRecs reads one JSON object, several in a row, or an array of them,
// refusing unknown fields.
func readRecs(r io.Reader) ([]rec.Rec, error) {
	b, err := io.ReadAll(io.LimitReader(r, 64<<20))
	if err != nil {
		return nil, err
	}
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil, errors.New("no recommendation on stdin")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if b[0] == '[' {
		var out []rec.Rec
		if err := dec.Decode(&out); err != nil {
			return nil, fmt.Errorf("recommendations: %v", strings.TrimPrefix(err.Error(), "json: "))
		}
		return out, nil
	}
	var out []rec.Rec
	for dec.More() {
		var one rec.Rec
		if err := dec.Decode(&one); err != nil {
			return nil, fmt.Errorf("recommendation %d: %v", len(out)+1, strings.TrimPrefix(err.Error(), "json: "))
		}
		out = append(out, one)
	}
	return out, nil
}
