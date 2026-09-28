package check

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/discover"
	"github.com/schuettc/tackle/internal/cull/extract"
	_ "github.com/schuettc/tackle/internal/cull/extract/golang"
	_ "github.com/schuettc/tackle/internal/cull/extract/python"
	_ "github.com/schuettc/tackle/internal/cull/extract/ts"
	"github.com/schuettc/tackle/internal/cull/judge"
	"github.com/schuettc/tackle/internal/cull/policy"
	"github.com/schuettc/tackle/internal/cull/rubric"
	"github.com/schuettc/tackle/internal/cull/similar"
	tools "github.com/schuettc/tools-common"
)

// Endpoint is the Jev HTTP endpoint check refuses to reach without egress.
const Endpoint = "https://api.typesafe.ai"

// Options configures one Run.
type Options struct {
	Path    string // project path or subdirectory; "" means "."
	Diff    string // base ref; "" means suite mode
	DryRun  bool
	Refresh bool
	Stdout  io.Writer // dry-run states go here (as `cull judge --dry-run`); defaults to io.Discard
	Stderr  io.Writer // one "cull: skipped <file>: <reason>" line per skipped file (dry-run too); defaults to io.Discard
}

// ResolveConfig finds the project root for path and loads its .cull.toml,
// gating on egress unless dryRun. cli/check.go calls this before deciding
// whether the API key is needed, so a missing or misconfigured .cull.toml
// is reported before a missing key ever is. Run calls it too, so both code
// paths gate identically.
func ResolveConfig(path string, dryRun bool) (root string, cfg Config, err error) {
	p := path
	if p == "" {
		p = "."
	}
	root, err = discover.Root(p)
	if err != nil {
		return "", Config{}, err
	}
	cfg, found, err := LoadConfig(root)
	if err != nil {
		return "", Config{}, err
	}
	if !dryRun && (!found || !cfg.Egress) {
		return "", Config{}, fmt.Errorf(
			"check sends test source to %s; set egress = true in .cull.toml to allow it (or --dry-run to see what would be sent)",
			Endpoint)
	}
	return root, cfg, nil
}

// Run discovers, extracts, groups, judges and reports. It always uses the
// same code path as `cull judge` (JudgeAndDecide) so calibration measures
// what check ships. It writes <root>/.cull/last.json unless DryRun is set.
func Run(ctx context.Context, ev judge.Evaluator, opt Options) (Report, error) {
	stdout := opt.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := opt.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	p := opt.Path
	if p == "" {
		p = "."
	}
	root, cfg, err := ResolveConfig(p, opt.DryRun)
	if err != nil {
		return Report{}, err
	}

	sub, err := subPath(root, p)
	if err != nil {
		return Report{}, err
	}

	mode := "suite"
	var files []string
	var changes discover.Changes
	if opt.Diff != "" {
		mode = "diff"
		changes, err = discover.Diff(root, opt.Diff, sub, cfg.Exclude)
		if err != nil {
			return Report{}, err
		}
		for f := range changes {
			files = append(files, f)
		}
		sort.Strings(files)
	} else {
		files, err = discover.Suite(root, sub, cfg.Exclude)
		if err != nil {
			return Report{}, err
		}
	}

	allCases, skipped, err := extractAll(root, files, cfg.MaxContextBytes)
	if err != nil {
		return Report{}, err
	}
	for _, sk := range skipped {
		fmt.Fprintf(stderr, "cull: skipped %s: %s\n", sk.File, sk.Reason)
	}

	fileInv, err := fileInventory(root, allCases)
	if err != nil {
		return Report{}, err
	}

	keptCases := allCases
	groups := similar.Groups(allCases)
	if mode == "diff" {
		touched := map[string]bool{}
		keptCases = nil
		for _, tc := range allCases {
			start, end, err := spanLines(root, tc.File, tc.Span)
			if err != nil {
				return Report{}, err
			}
			if changes.Touches(tc.File, start, end) {
				keptCases = append(keptCases, tc)
				touched[tc.ID] = true
			}
		}
		var kept []cases.Group
		for _, g := range groups {
			for _, m := range g.Tests {
				if touched[m.ID] {
					kept = append(kept, g)
					break
				}
			}
		}
		groups = kept
	}

	if opt.DryRun {
		printDryRunStates(stdout, keptCases, groups, cfg.MaxContextBytes)
		return Report{
			Root: root, Mode: mode, Base: opt.Diff,
			Files: fileInv, Skipped: skipped, Summary: summarize(nil, nil, skipped),
		}, nil
	}

	testRubric, err := rubric.Load(cfg.TestRubric)
	if err != nil {
		return Report{}, err
	}
	groupRubric, err := rubric.Load(cfg.GroupRubric)
	if err != nil {
		return Report{}, err
	}

	cache := &judge.Cache{Dir: filepath.Join(tools.CacheDir("cull"), "answers")}
	jopt := judge.Options{Model: cfg.Model, Concurrency: cfg.Concurrency, Refresh: opt.Refresh, Cache: cache}

	testStates := make([]any, len(keptCases))
	testTruncated := make([]bool, len(keptCases))
	for i, tc := range keptCases {
		testStates[i] = judge.StateFor(tc)
		testTruncated[i] = tc.Truncated
	}
	testJudged, testResults, err := JudgeAndDecide(ctx, ev, testStates, testTruncated, testRubric, jopt)
	if err != nil {
		return Report{}, err
	}

	groupStates := make([]any, len(groups))
	groupTruncated := make([]bool, len(groups))
	for i, g := range groups {
		s := judge.GroupStateFor(g, cfg.MaxContextBytes)
		groupStates[i] = s
		groupTruncated[i] = s.Truncated
	}
	groupJudged, groupResults, err := JudgeAndDecide(ctx, ev, groupStates, groupTruncated, groupRubric, jopt)
	if err != nil {
		return Report{}, err
	}

	tests := make([]TestResult, len(keptCases))
	for i, tc := range keptCases {
		tr := TestResult{TestCase: tc, Model: testJudged[i].Model, Err: testJudged[i].Err}
		if testJudged[i].Err == "" {
			res := testResults[i]
			tr.Verdict, tr.Rule, tr.Reasons = string(res.Verdict), res.Rule, res.Reasons
		}
		tests[i] = tr
	}

	groupOut := make([]GroupResult, len(groups))
	for i, g := range groups {
		members := make([]string, len(g.Tests))
		memberHashes := make([]string, len(g.Tests))
		bodies := make([]string, len(g.Tests))
		for j, m := range g.Tests {
			members[j] = m.ID
			memberHashes[j] = m.Hash
			bodies[j] = m.Body
		}
		rows := similar.Distinguishing(g.Lang, bodies)
		gr := GroupResult{
			ID: g.ID, File: g.File, Members: members, MemberHashes: memberHashes, Rows: rows,
			Model: groupJudged[i].Model, Err: groupJudged[i].Err,
		}
		if groupJudged[i].Err == "" {
			res := groupResults[i]
			gr.Verdict, gr.Rule, gr.Reasons, gr.ExactDuplicate = string(res.Verdict), res.Rule, res.Reasons, res.ExactDuplicate
		}
		groupOut[i] = gr
	}

	report := Report{
		Root: root, Mode: mode, Base: opt.Diff,
		Tests: tests, Groups: groupOut, Files: fileInv, Skipped: skipped,
		Summary: summarize(tests, groupOut, skipped),
	}
	if err := writeLastJSON(root, report); err != nil {
		return report, err
	}
	return report, nil
}

// JudgeAndDecide judges states in parallel with ev under r and opt, then
// applies policy.Decide to every state that came back without an error.
// cli/judge.go and check.Run both call this, so `cull judge` and `cull
// check` judge through one code path.
func JudgeAndDecide(ctx context.Context, ev judge.Evaluator, states []any, truncated []bool, r rubric.Rubric, opt judge.Options) ([]judge.Judged, []policy.Result, error) {
	opt.Rubric = r
	js, err := judge.JudgeAll(ctx, ev, states, opt)
	if err != nil {
		return js, nil, err
	}
	results := make([]policy.Result, len(js))
	for i, j := range js {
		if j.Err == "" {
			results[i] = policy.Decide(r, j.Answers, truncated[i])
		}
	}
	return js, results, nil
}

// subPath returns root-relative path of p (project-relative sub dir for
// Suite). root came from discover.Root, which resolves symlinks via `git
// rev-parse --show-toplevel`; p usually did not go through that, so both
// sides are symlink-resolved before comparing (e.g. macOS's /tmp ->
// /private/tmp) — otherwise a plain filepath.Rel can wrongly climb out of
// root and back in, breaking Suite's walk.
func subPath(root, p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	rroot := root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		rroot = resolved
	}
	rel, err := filepath.Rel(rroot, abs)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", nil
	}
	return rel, nil
}

// fileInventory builds the per-file inventory (content hash and every
// extracted test, judged or not) that `cull apply` and `cull check
// --group` later use to prove what changed since this check: every file
// extracted in the run, keyed by the same relpath used in TestCase.File.
func fileInventory(root string, allCases []cases.TestCase) (map[string]FileInfo, error) {
	testsByFile := map[string][]FileTest{}
	var fileOrder []string
	for _, tc := range allCases {
		if _, ok := testsByFile[tc.File]; !ok {
			fileOrder = append(fileOrder, tc.File)
		}
		testsByFile[tc.File] = append(testsByFile[tc.File], FileTest{ID: tc.ID, Hash: tc.Hash})
	}
	files := make(map[string]FileInfo, len(fileOrder))
	for _, f := range fileOrder {
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		files[f] = FileInfo{SHA256: hex.EncodeToString(sum[:]), Tests: testsByFile[f]}
	}
	return files, nil
}

// extractAll runs each registered extractor once over the files it matches,
// in registry order (golang, python, ts), so results are deterministic.
func extractAll(root string, files []string, maxContext int) ([]cases.TestCase, []extract.Skipped, error) {
	var allCases []cases.TestCase
	var skipped []extract.Skipped
	for _, e := range extract.Registry() {
		var fs []string
		for _, f := range files {
			if e.Match(f) {
				fs = append(fs, f)
			}
		}
		if len(fs) == 0 {
			continue
		}
		res, err := e.Extract(root, fs, maxContext)
		if err != nil {
			return nil, nil, err
		}
		allCases = append(allCases, res.Cases...)
		skipped = append(skipped, res.Skipped...)
	}
	return allCases, skipped, nil
}

// spanLines converts a test's byte Span into 1-based inclusive line numbers
// by counting newlines before Start and before End-1 in the file's bytes.
func spanLines(root, relpath string, span cases.Span) (start, end int, err error) {
	data, err := os.ReadFile(filepath.Join(root, relpath))
	if err != nil {
		return 0, 0, err
	}
	lineAt := func(off int) int {
		if off < 0 {
			off = 0
		}
		if off > len(data) {
			off = len(data)
		}
		return bytes.Count(data[:off], []byte("\n")) + 1
	}
	start = lineAt(span.Start)
	endOff := span.End - 1
	if endOff < span.Start {
		endOff = span.Start
	}
	end = lineAt(endOff)
	return start, end, nil
}

// printDryRunStates prints each test's and group's Jev state as one JSON
// line, exactly what `cull judge --dry-run` prints for the same states.
func printDryRunStates(w io.Writer, tests []cases.TestCase, groups []cases.Group, maxContext int) {
	enc := json.NewEncoder(w)
	for _, tc := range tests {
		enc.Encode(map[string]any{"id": tc.ID, "state": judge.StateFor(tc)})
	}
	for _, g := range groups {
		enc.Encode(map[string]any{"id": g.ID, "state": judge.GroupStateFor(g, maxContext)})
	}
}
