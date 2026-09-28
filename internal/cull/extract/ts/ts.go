// Package ts extracts TypeScript tests (node:test/*.test.ts and
// Playwright's *.spec.ts, plus their .tsx variants) into cull TestCases by
// running an embedded Node helper (tsext.cjs) as a subprocess: the helper
// uses the TypeScript compiler's parser to do the AST walking (Phase 0's
// context/callee rules, unchanged) and prints one JSON object per test
// case (or a skip record) which this package turns into cases.TestCase /
// extract.Skipped.
package ts

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/extract"
)

//go:embed tsext.cjs
var helperSrc []byte

func init() {
	extract.Register(New)
}

// defaultMaxContext matches tsext.cjs's MAX_CTX.
const defaultMaxContext = 24000

type tsExtractor struct{}

// New builds the TypeScript extractor.
func New() extract.Extractor { return tsExtractor{} }

func (tsExtractor) Lang() string { return "typescript" }

// Match reports whether relpath is a node:test or Playwright test file:
// *.test.ts, *.spec.ts, *.test.tsx, or *.spec.tsx.
func (tsExtractor) Match(relpath string) bool {
	base := filepath.Base(filepath.ToSlash(relpath))
	for _, suf := range []string{".test.ts", ".spec.ts", ".test.tsx", ".spec.tsx"} {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	return false
}

// findTypescript locates a directory containing an installed "typescript"
// package: $CULL_TS if set, else the nearest node_modules/typescript
// walking up from root.
func findTypescript(root string) (string, bool) {
	if v := os.Getenv("CULL_TS"); v != "" {
		return v, true
	}
	dir, err := filepath.Abs(root)
	if err != nil {
		dir = root
	}
	for {
		cand := filepath.Join(dir, "node_modules", "typescript")
		if info, err := os.Stat(cand); err == nil && info.IsDir() {
			return cand, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

// helperLine is one line of the helper's JSONL output: either a skip
// record (Skip non-empty) or a TestCase's fields plus its byte span.
type helperLine struct {
	Skip      string         `json:"skip"`
	Reason    string         `json:"reason"`
	ID        string         `json:"id"`
	Lang      string         `json:"lang"`
	Framework string         `json:"framework"`
	File      string         `json:"file"`
	Name      string         `json:"name"`
	Parent    string         `json:"parent"`
	Body      string         `json:"body"`
	Context   string         `json:"context"`
	Callees   []cases.Callee `json:"callees"`
	Truncated bool           `json:"truncated"`
	Span      cases.Span     `json:"span"`
}

// Extract runs the embedded helper over relpaths and turns its JSONL
// output into TestCases/Skipped. If node is not on PATH, or no typescript
// package can be found ($CULL_TS or node_modules/typescript walking up
// from root), every relpath is reported as Skipped rather than erroring:
// a missing toolchain is expected in some environments, not a bug.
func (tsExtractor) Extract(root string, relpaths []string, maxContext int) (extract.Result, error) {
	if maxContext <= 0 {
		maxContext = defaultMaxContext
	}

	var res extract.Result
	if len(relpaths) == 0 {
		return res, nil
	}

	nodePath, err := exec.LookPath("node")
	if err != nil {
		for _, rel := range relpaths {
			res.Skipped = append(res.Skipped, extract.Skipped{
				File:   filepath.ToSlash(rel),
				Reason: "node not found",
			})
		}
		return res, nil
	}

	tsDir, ok := findTypescript(root)
	if !ok {
		reason := fmt.Sprintf("typescript not found under %s", root)
		for _, rel := range relpaths {
			res.Skipped = append(res.Skipped, extract.Skipped{
				File:   filepath.ToSlash(rel),
				Reason: reason,
			})
		}
		return res, nil
	}

	tmpDir, err := os.MkdirTemp("", "cull-tsext-*")
	if err != nil {
		return extract.Result{}, fmt.Errorf("extract/ts: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0o700); err != nil {
		return extract.Result{}, fmt.Errorf("extract/ts: chmod temp dir: %w", err)
	}
	helperPath := filepath.Join(tmpDir, "tsext.cjs")
	if err := os.WriteFile(helperPath, helperSrc, 0o600); err != nil {
		return extract.Result{}, fmt.Errorf("extract/ts: write helper: %w", err)
	}

	args := make([]string, 0, len(relpaths)+3)
	args = append(args, helperPath, tsDir, root)
	for _, rel := range relpaths {
		args = append(args, filepath.ToSlash(rel))
	}

	cmd := exec.Command(nodePath, args...)
	cmd.Env = extract.HelperEnv(maxContext)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return extract.Result{}, fmt.Errorf("extract/ts: run helper: %w (stderr: %s)", err, stderr.String())
	}

	scanner := bufio.NewScanner(&stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var hl helperLine
		if err := json.Unmarshal(line, &hl); err != nil {
			return extract.Result{}, fmt.Errorf("extract/ts: decode helper output %q: %w", line, err)
		}
		if hl.Skip != "" {
			res.Skipped = append(res.Skipped, extract.Skipped{File: hl.Skip, Reason: hl.Reason})
			continue
		}
		res.Cases = append(res.Cases, cases.TestCase{
			ID:        hl.ID,
			Hash:      cases.HashBody(hl.Body),
			Lang:      hl.Lang,
			Framework: hl.Framework,
			File:      hl.File,
			Name:      hl.Name,
			Parent:    hl.Parent,
			Body:      hl.Body,
			Context:   hl.Context,
			Span:      hl.Span,
			Callees:   hl.Callees,
			Truncated: hl.Truncated,
		})
	}
	if err := scanner.Err(); err != nil {
		return extract.Result{}, fmt.Errorf("extract/ts: read helper output: %w", err)
	}

	return res, nil
}

// tidyResult is the JSON the helper's --tidy mode prints on stdout.
type tidyResult struct {
	Source  *string  `json:"source"` // null: the helper refused (not UTF-8)
	Removed []string `json:"removed"`
}

// Tidy removes now-unused imports from a TypeScript test file's source: it
// runs the embedded helper as `node tsext.cjs --tidy <typescript-dir>
// <relpath>`, feeding src on stdin, and decodes its
// {"source":...,"removed":[...]} JSON reply. If node is not on PATH, or
// no typescript package can be found ($CULL_TS or node_modules/typescript
// walking up from root), src is returned unchanged with no error and no
// removals.
func Tidy(root, relpath string, before, src []byte) ([]byte, []string, error) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return src, nil, nil
	}

	tsDir, ok := findTypescript(root)
	if !ok {
		return src, nil, nil
	}

	tmpDir, err := os.MkdirTemp("", "cull-tsext-tidy-*")
	if err != nil {
		return nil, nil, fmt.Errorf("extract/ts: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("extract/ts: chmod temp dir: %w", err)
	}
	helperPath := filepath.Join(tmpDir, "tsext.cjs")
	if err := os.WriteFile(helperPath, helperSrc, 0o600); err != nil {
		return nil, nil, fmt.Errorf("extract/ts: write helper: %w", err)
	}

	beforePath := filepath.Join(tmpDir, "before")
	if err := os.WriteFile(beforePath, before, 0o600); err != nil {
		return nil, nil, fmt.Errorf("extract/ts: write before: %w", err)
	}
	cmd := exec.Command(nodePath, helperPath, "--tidy", tsDir, filepath.ToSlash(relpath), beforePath)
	cmd.Env = extract.HelperEnv(defaultMaxContext)
	cmd.Stdin = bytes.NewReader(src)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("extract/ts: run helper --tidy: %w (stderr: %s)", err, stderr.String())
	}

	var result tidyResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
		return nil, nil, fmt.Errorf("extract/ts: decode helper --tidy output %q: %w", stdout.Bytes(), err)
	}
	if result.Source == nil {
		return src, nil, nil
	}
	return []byte(*result.Source), result.Removed, nil
}
