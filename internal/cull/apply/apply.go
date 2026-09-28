// Package apply removes top-level tests that `cull check` judged cut. It
// loads the report `cull check` wrote (<root>/.cull/last.json), selects
// the tests to remove, preflights that nothing changed underneath since
// then, and (in later steps) removes their spans and tidies imports.
//
// apply imports check to read its report; check must never import apply.
package apply

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/check"
)

// Target is one test apply will remove: enough to prove, right before
// editing, that the file and the test's exact bytes are unchanged since
// `cull check` ran.
type Target struct {
	ID   string
	File string
	Lang string
	Hash string
	Span cases.Span
}

// Refusal is one test or id apply will not touch, and why.
type Refusal struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// Load reads <root>/.cull/last.json, the report `cull check` wrote.
func Load(root string) (check.Report, error) {
	path := filepath.Join(root, ".cull", "last.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return check.Report{}, fmt.Errorf("no .cull/last.json; run cull check first")
		}
		return check.Report{}, err
	}
	var r check.Report
	if err := json.Unmarshal(data, &r); err != nil {
		return check.Report{}, err
	}
	return r, nil
}

// Select picks the tests apply will remove from a loaded report.
//
// With verdictCut, every test whose verdict is cut is a candidate: one
// with no parent becomes a Target; one with a parent (it lives inside
// another top-level test/suite) needs a human or agent to edit by hand,
// so its id is returned in needsAgent instead.
//
// With ids (verdictCut false), each id is looked up in the report: an id
// not present is refused ("not in last.json"); an id with a parent is
// refused (the caller must edit it by hand); otherwise it becomes a
// Target regardless of its verdict.
//
// Exactly one of ids or verdictCut is meant to be used per call; the
// caller (the apply command) enforces that they are mutually exclusive.
func Select(r check.Report, ids []string, verdictCut bool) (targets []Target, refused []Refusal, needsAgent []string) {
	if verdictCut {
		for _, t := range r.Tests {
			if t.Verdict != "cut" {
				continue
			}
			if t.Parent != "" {
				needsAgent = append(needsAgent, t.ID)
				continue
			}
			targets = append(targets, Target{ID: t.ID, File: t.File, Lang: t.Lang, Hash: t.Hash, Span: t.Span})
		}
		return targets, refused, needsAgent
	}

	byID := make(map[string]check.TestResult, len(r.Tests))
	for _, t := range r.Tests {
		byID[t.ID] = t
	}
	for _, id := range ids {
		t, ok := byID[id]
		if !ok {
			refused = append(refused, Refusal{ID: id, Reason: "not in last.json"})
			continue
		}
		if t.Parent != "" {
			refused = append(refused, Refusal{ID: id, Reason: fmt.Sprintf("%s is inside %s; edit it by hand, then run cull check", id, t.Parent)})
			continue
		}
		targets = append(targets, Target{ID: t.ID, File: t.File, Lang: t.Lang, Hash: t.Hash, Span: t.Span})
	}
	return targets, refused, needsAgent
}

// Preflight re-checks, right before editing, that each target's file
// still has the sha256 recorded in the report and that the target's span
// still hashes to the test's recorded hash. Either mismatch refuses the
// target: the file changed since `cull check` ran, so its span offsets
// (and the report's understanding of what's at them) can no longer be
// trusted.
func Preflight(root string, r check.Report, ts []Target) []Refusal {
	const reason = "file changed since cull check; run cull check again"
	var refused []Refusal
	fileBytes := map[string][]byte{}
	fileOK := map[string]bool{}
	for _, t := range ts {
		data, checked := fileBytes[t.File]
		if !checked {
			d, err := os.ReadFile(filepath.Join(root, t.File))
			ok := false
			if err == nil {
				fi, present := r.Files[t.File]
				sum := sha256.Sum256(d)
				ok = present && hex.EncodeToString(sum[:]) == fi.SHA256
			}
			fileBytes[t.File] = d
			fileOK[t.File] = ok
			data = d
		}
		if !fileOK[t.File] {
			refused = append(refused, Refusal{ID: t.ID, Reason: reason})
			continue
		}
		if t.Span.Start < 0 || t.Span.End > len(data) || t.Span.Start > t.Span.End {
			refused = append(refused, Refusal{ID: t.ID, Reason: reason})
			continue
		}
		body := data[t.Span.Start:t.Span.End]
		if cases.HashBody(string(body)) != t.Hash {
			refused = append(refused, Refusal{ID: t.ID, Reason: reason})
			continue
		}
	}
	return refused
}
