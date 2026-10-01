// Package cases defines TestCase, one extracted test: the unit every cull
// stage passes along, and the JSONL line format extractors emit.
package cases

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// DefaultMaxContextBytes caps the setup and callee source sent with one test
// (and a group's total test source). 64 KB fit every test of a large Python
// suite (nfl-dk, 2026-09-30: largest 53 KB); at 24 KB, 179 of its 3,147 tests
// lost a callee and went to review for that alone.
const DefaultMaxContextBytes = 64000

// Span is a byte range [Start, End) in the test's file.
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Callee is non-test code the test calls, resolved to its source.
type Callee struct {
	Symbol string `json:"symbol"`
	File   string `json:"file"`
	Source string `json:"source"`
}

// TestCase is one test with the context Jev needs to judge it.
type TestCase struct {
	ID        string   `json:"id"`   // lang:relpath:qualified-name
	Hash      string   `json:"hash"` // HashBody(Body)
	Lang      string   `json:"lang"`
	Framework string   `json:"framework"`
	File      string   `json:"file"`
	Name      string   `json:"name"`
	Parent    string   `json:"parent,omitempty"`
	Body      string   `json:"body"`
	Context   string   `json:"context"`
	Repo      string   `json:"repo,omitempty"`
	Span      Span     `json:"span"`
	Callees   []Callee `json:"callees"`
	Truncated bool     `json:"truncated"`
	// PinsSetting: the test runs none of the project's code; it only reads a
	// project setting or class and compares it to fixed values. Set by the
	// extractors; not part of the state Jev sees.
	PinsSetting bool `json:"pins_setting,omitempty"`
}

// HashBody identifies a test's exact source.
func HashBody(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Group is a set of near-duplicate tests from one file, judged together.
type Group struct {
	ID        string     `json:"id"`
	Lang      string     `json:"lang"`
	Framework string     `json:"framework"`
	File      string     `json:"file"`
	Tests     []TestCase `json:"tests"`
}

// GroupID is a stable id for a set of member test ids, independent of order.
func GroupID(memberIDs []string) string {
	ids := append([]string(nil), memberIDs...)
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	return "group:" + hex.EncodeToString(sum[:])[:16]
}
