// Package cases defines TestCase, one extracted test: the unit every cull
// stage passes along, and the JSONL line format extractors emit.
package cases

import (
	"crypto/sha256"
	"encoding/hex"
)

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
}

// HashBody identifies a test's exact source.
func HashBody(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}
