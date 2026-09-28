package check

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/extract"
	"github.com/schuettc/tackle/internal/cull/judge"
	"github.com/schuettc/tackle/internal/cull/rubric"
	"github.com/schuettc/tackle/internal/cull/similar"
	"github.com/schuettc/tackle/internal/cull/verify"
	tools "github.com/schuettc/tools-common"
)

// GroupVerifyTimeout is the per-command test timeout `cull check --group`
// uses, matching apply.DefaultTimeout (check must never import apply, so
// this is a separate constant with the same value).
const GroupVerifyTimeout = 15 * time.Minute

// GroupCheck is what `cull check --group <id>` found.
type GroupCheck struct {
	ID            string          `json:"id"`
	File          string          `json:"file"`
	OriginalsGone bool            `json:"originals_gone"`
	StillPresent  []string        `json:"still_present,omitempty"`
	NewTests      []string        `json:"new_tests,omitempty"`
	MissingRows   []string        `json:"missing_rows,omitempty"`
	Flagged       []string        `json:"flagged,omitempty"`
	Unjudged      []string        `json:"unjudged,omitempty"`
	Verify        []verify.Result `json:"verify,omitempty"`
	OK            bool            `json:"ok"`
}

// LoadReport reads <root>/.cull/last.json, the report `cull check` wrote.
// It duplicates apply.Load's logic rather than importing it: apply imports
// check, and check must never import apply.
func LoadReport(root string) (Report, error) {
	path := filepath.Join(root, ".cull", "last.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Report{}, fmt.Errorf("no .cull/last.json; run cull check first")
		}
		return Report{}, err
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return Report{}, err
	}
	return r, nil
}

// CheckGroup verifies an agent's rewrite of the near-duplicate group
// groupID (from <root>/.cull/last.json) into a table-driven test. It runs
// four checks, all evaluated (not stop-at-first):
//
//  1. originals gone: a member counts as present only if a current test
//     in the group's file has its id AND its hash.
//  2. at least one new test exists (a current test in the file whose
//     (id, hash) is not in the last check's inventory for that file), and
//     every member's distinguishing row values all appear in the literals
//     of one single new test (an empty row always passes).
//  3. the new tests, judged with the test rubric, and the file's current
//     tests re-grouped and judged with the group rubric: a new test judged
//     cut, or that lands in a group judged consolidate, is flagged.
//  4. the project's tests, scoped to the group's file, pass.
//
// Like `cull check`, non-dry-run gates on egress before doing anything
// else; --dry-run runs checks 1-2 only, prints (to opt.Stdout) the states
// check 3 would send, and skips checks 3 and 4 entirely (so it needs
// neither the egress config nor an API key).
func CheckGroup(ctx context.Context, ev judge.Evaluator, root, groupID string, opt Options) (GroupCheck, error) {
	stdout := opt.Stdout
	if stdout == nil {
		stdout = io.Discard
	}

	cfg, _, err := LoadConfig(root)
	if err != nil {
		return GroupCheck{}, err
	}
	if !opt.DryRun && !cfg.Egress {
		return GroupCheck{}, fmt.Errorf(
			"check sends test source to %s; set egress = true in .cull.toml to allow it (or --dry-run to see what would be sent)",
			Endpoint)
	}

	report, err := LoadReport(root)
	if err != nil {
		return GroupCheck{}, err
	}
	var group *GroupResult
	for i := range report.Groups {
		if report.Groups[i].ID == groupID {
			group = &report.Groups[i]
			break
		}
	}
	if group == nil {
		return GroupCheck{}, fmt.Errorf("unknown group %s", groupID)
	}
	file := group.File
	finfo, ok := report.Files[file]
	if !ok {
		return GroupCheck{}, fmt.Errorf("%s: not in last.json", file)
	}

	e := extract.ForFile(file)
	if e == nil {
		return GroupCheck{}, fmt.Errorf("%s: no extractor for this file", file)
	}
	res, err := e.Extract(root, []string{file}, cfg.MaxContextBytes)
	if err != nil {
		return GroupCheck{}, err
	}
	if len(res.Skipped) > 0 {
		return GroupCheck{}, fmt.Errorf("%s: %s", res.Skipped[0].File, res.Skipped[0].Reason)
	}
	current := res.Cases

	gc := GroupCheck{ID: group.ID, File: file}

	// Check 1: originals gone. A member counts as present only if a
	// current test has its id AND its hash.
	oldHash := map[string]string{}
	for i, m := range group.Members {
		if i < len(group.MemberHashes) {
			oldHash[m] = group.MemberHashes[i]
		}
	}
	curByID := map[string]cases.TestCase{}
	for _, tc := range current {
		curByID[tc.ID] = tc
	}
	for _, m := range group.Members {
		if tc, ok := curByID[m]; ok && tc.Hash == oldHash[m] {
			gc.StillPresent = append(gc.StillPresent, m)
		}
	}
	gc.OriginalsGone = len(gc.StillPresent) == 0

	// Check 2: at least one new test, and every member's distinguishing
	// row values all appear in one single new test's literals.
	oldSet := map[[2]string]bool{}
	for _, ft := range finfo.Tests {
		oldSet[[2]string{ft.ID, ft.Hash}] = true
	}
	var newCases []cases.TestCase
	for _, tc := range current {
		if !oldSet[[2]string{tc.ID, tc.Hash}] {
			newCases = append(newCases, tc)
			gc.NewTests = append(gc.NewTests, tc.ID)
		}
	}
	newLiterals := make([]map[string]bool, len(newCases))
	for i, nc := range newCases {
		set := map[string]bool{}
		for _, l := range similar.Literals(e.Lang(), nc.Body) {
			set[l] = true
		}
		newLiterals[i] = set
	}
	for i, m := range group.Members {
		var row []string
		if i < len(group.Rows) {
			row = group.Rows[i]
		}
		if len(row) == 0 {
			continue
		}
		found := false
		for _, set := range newLiterals {
			all := true
			for _, v := range row {
				if !set[v] {
					all = false
					break
				}
			}
			if all {
				found = true
				break
			}
		}
		if !found {
			gc.MissingRows = append(gc.MissingRows, m)
		}
	}

	if opt.DryRun {
		regrouped := similar.Groups(current)
		printDryRunStates(stdout, newCases, regrouped, cfg.MaxContextBytes)
		gc.OK = gc.OriginalsGone && len(gc.NewTests) > 0 && len(gc.MissingRows) == 0
		return gc, nil
	}

	// Check 3: judge the new tests, and re-group + judge the file's
	// current tests. A new test judged cut, or in a group judged
	// consolidate, is flagged.
	testRubric, err := rubric.Load(cfg.TestRubric)
	if err != nil {
		return gc, err
	}
	groupRubric, err := rubric.Load(cfg.GroupRubric)
	if err != nil {
		return gc, err
	}
	cache := &judge.Cache{Dir: filepath.Join(tools.CacheDir("cull"), "answers")}
	jopt := judge.Options{Model: cfg.Model, Concurrency: cfg.Concurrency, Refresh: opt.Refresh, Cache: cache}

	testStates := make([]any, len(newCases))
	testTruncated := make([]bool, len(newCases))
	for i, tc := range newCases {
		testStates[i] = judge.StateFor(tc)
		testTruncated[i] = tc.Truncated
	}
	testJudged, testResults, err := JudgeAndDecide(ctx, ev, testStates, testTruncated, testRubric, jopt)
	if err != nil {
		return gc, err
	}

	regrouped := similar.Groups(current)
	groupStates := make([]any, len(regrouped))
	groupTruncated := make([]bool, len(regrouped))
	for i, g := range regrouped {
		s := judge.GroupStateFor(g, cfg.MaxContextBytes)
		groupStates[i] = s
		groupTruncated[i] = s.Truncated
	}
	groupJudged, groupResults, err := JudgeAndDecide(ctx, ev, groupStates, groupTruncated, groupRubric, jopt)
	if err != nil {
		return gc, err
	}

	newSet := map[string]bool{}
	for _, id := range gc.NewTests {
		newSet[id] = true
	}

	// A per-item judge error must never be treated as "not flagged": an
	// unjudged new test (or a re-formed group containing one) is reported
	// separately and forces the run incomplete, not passed.
	unjudged := map[string]bool{}
	flagged := map[string]bool{}
	for i, tc := range newCases {
		if testJudged[i].Err != "" {
			unjudged[tc.ID] = true
			continue
		}
		if testResults[i].Verdict == "cut" {
			flagged[tc.ID] = true
		}
	}
	for i, g := range regrouped {
		if groupJudged[i].Err != "" {
			for _, m := range g.Tests {
				if newSet[m.ID] {
					unjudged[m.ID] = true
				}
			}
			continue
		}
		if groupResults[i].Verdict != "consolidate" {
			continue
		}
		for _, m := range g.Tests {
			if newSet[m.ID] {
				flagged[m.ID] = true
			}
		}
	}
	for _, id := range gc.NewTests {
		if unjudged[id] {
			gc.Unjudged = append(gc.Unjudged, id)
			continue
		}
		if flagged[id] {
			gc.Flagged = append(gc.Flagged, id)
		}
	}

	// Check 4: verify, scoped to the group's file.
	files := map[string]string{file: e.Lang()}
	cmds, err := verify.Plan(root, cfg.TestCommand, files)
	if err != nil {
		return gc, err
	}
	gc.Verify = verify.Run(ctx, cmds, GroupVerifyTimeout)

	verifyOK := true
	for _, r := range gc.Verify {
		if !r.OK {
			verifyOK = false
		}
	}
	gc.OK = gc.OriginalsGone && len(gc.NewTests) > 0 && len(gc.MissingRows) == 0 &&
		len(gc.Flagged) == 0 && len(gc.Unjudged) == 0 && verifyOK
	return gc, nil
}
