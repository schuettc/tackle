// Package extract defines the language-extractor interface: turning source
// files into cull TestCases plus any files that could not be parsed.
package extract

import "github.com/schuettc/tackle/internal/cull/cases"

// Skipped is a file an Extractor could not process, and why.
type Skipped struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// Result is everything one Extract call produced.
type Result struct {
	Cases   []cases.TestCase `json:"cases"`
	Skipped []Skipped        `json:"skipped,omitempty"`
}

// Extractor turns test files in one language into TestCases.
type Extractor interface {
	// Lang is the TestCase.Lang value this extractor produces: "go",
	// "python", or "typescript".
	Lang() string
	// Match reports whether relpath (project-root-relative, '/'-separated)
	// is a test file this extractor handles.
	Match(relpath string) bool
	// Extract processes relpaths (all of which must satisfy Match) rooted
	// at root and returns their TestCases. It returns an error only for
	// environment failures (e.g. root does not exist); a file that fails
	// to parse is recorded in Result.Skipped instead.
	Extract(root string, relpaths []string, maxContext int) (Result, error)
}

// factories holds the known extractor constructors, in registration order
// (golang, python, ts, once all three exist). Each language package
// registers itself from an init func, so importing a language package for
// side effect is what populates Registry().
var factories []func() Extractor

// Register adds a language extractor's constructor to the registry.
// Language packages call this from an init func.
func Register(f func() Extractor) {
	factories = append(factories, f)
}

// Registry returns one instance of every known Extractor, golang, python, ts
// in that order. Callers must import the language packages (for side
// effect) to populate it.
func Registry() []Extractor {
	out := make([]Extractor, 0, len(factories))
	for _, f := range factories {
		out = append(out, f())
	}
	return out
}

// ForFile returns the Registry extractor whose Match(relpath) is true, or
// nil if none matches.
func ForFile(relpath string) Extractor {
	for _, e := range Registry() {
		if e.Match(relpath) {
			return e
		}
	}
	return nil
}
