package apply

import (
	"github.com/schuettc/tackle/internal/cull/extract/python"
	"github.com/schuettc/tackle/internal/cull/extract/ts"
)

// Tidy removes now-unused imports from src, a file apply has just removed
// one or more tests from, dispatching by lang: "go" uses TidyGo (goimports
// or a go/ast fallback); "python" and "typescript" shell out to that
// language's embedded extractor helper in --tidy mode. Any other lang, or
// a helper that can't run (no python3 / no node / no typescript package
// found), leaves src unchanged with no error and no removals -- apply's
// Preflight/verify step protects the file either way. before is the
// pre-edit file: the Python and TypeScript helpers remove only imports it
// used that src no longer uses.
func Tidy(root, relpath, lang string, before, src []byte) ([]byte, []string, error) {
	switch lang {
	case "go":
		return TidyGo(src)
	case "python":
		return python.Tidy(root, relpath, before, src)
	case "typescript":
		return ts.Tidy(root, relpath, before, src)
	default:
		return src, nil, nil
	}
}
