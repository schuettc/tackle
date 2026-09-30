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
// so it is returned in needsAgent instead, with the reason.
//
// With ids (verdictCut false), each id is looked up in the report: an id
// not present is refused ("not in last.json"); an id with a parent is
// refused (the caller must edit it by hand); otherwise it becomes a
// Target regardless of its verdict.
//
// Exactly one of ids or verdictCut is meant to be used per call; the
// caller (the apply command) enforces that they are mutually exclusive.
func Select(r check.Report, ids []string, verdictCut bool) (targets []Target, refused []Refusal, needsAgent []Refusal) {
	if verdictCut {
		for _, t := range r.Tests {
			if t.Verdict != "cut" {
				continue
			}
			if t.Parent != "" {
				needsAgent = append(needsAgent, Refusal{ID: t.ID, Reason: insideReason(t)})
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
			refused = append(refused, Refusal{ID: id, Reason: insideReason(t)})
			continue
		}
		targets = append(targets, Target{ID: t.ID, File: t.File, Lang: t.Lang, Hash: t.Hash, Span: t.Span})
	}
	return targets, refused, needsAgent
}

// HoldEmptiedFiles splits off every target in a file the targets would
// leave with no tests at all (each test in it is a target, or inside
// one): pytest and vitest fail on such a file, and deleting whole files
// is out of scope, so those are held back with the reason
// "would leave <file> with no tests".
func HoldEmptiedFiles(r check.Report, ts []Target) (keep []Target, held []Refusal) {
	targeted := map[string]bool{}
	for _, t := range ts {
		targeted[t.ID] = true
	}
	parent := map[string]string{}
	for _, t := range r.Tests {
		parent[t.ID] = t.Parent
	}
	removed := func(id string) bool {
		for n := 0; id != "" && n < 64; n++ {
			if targeted[id] {
				return true
			}
			id = parent[id]
		}
		return false
	}
	emptied := map[string]bool{}
	for _, t := range ts {
		if _, seen := emptied[t.File]; seen {
			continue
		}
		// Every test the file holds: check's inventory plus the report's
		// tests (which carry the parents).
		all := true
		for _, ft := range r.Files[t.File].Tests {
			if !removed(ft.ID) {
				all = false
			}
		}
		for _, tc := range r.Tests {
			if tc.File == t.File && !removed(tc.ID) {
				all = false
			}
		}
		emptied[t.File] = all
	}
	for _, t := range ts {
		if emptied[t.File] {
			held = append(held, Refusal{ID: t.ID, Reason: fmt.Sprintf("would leave %s with no tests", t.File)})
			continue
		}
		keep = append(keep, t)
	}
	return keep, held
}

func insideReason(t check.TestResult) string {
	return fmt.Sprintf("%s is inside %s; edit it by hand, then run cull check", t.ID, t.Parent)
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
