// Package python extracts pytest tests into cull TestCases by running an
// embedded Python helper (pyext.py) as a subprocess: the helper does the
// ast walking (Phase 0's fixture/context/callee rules, unchanged) and
// prints one JSON object per test case (or a skip record) which this
// package turns into cases.TestCase / extract.Skipped.
package python

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

//go:embed pyext.py
var helperSrc []byte

func init() {
	extract.Register(New)
}

// defaultMaxContext matches pyext.py's MAX_CTX.
const defaultMaxContext = 24000

// helperEnvFunc is extract.HelperEnv, indirected so tests can observe the
// environment Tidy computes for the subprocess.
var helperEnvFunc = extract.HelperEnv

type pythonExtractor struct{}

// New builds the Python extractor.
func New() extract.Extractor { return pythonExtractor{} }

func (pythonExtractor) Lang() string { return "python" }

// Match reports whether relpath is a pytest test file: test_*.py or
// *_test.py.
func (pythonExtractor) Match(relpath string) bool {
	base := filepath.Base(filepath.ToSlash(relpath))
	if !strings.HasSuffix(base, ".py") {
		return false
	}
	return strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py")
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
// output into TestCases/Skipped. If python3 is not on PATH, every relpath
// is reported as Skipped ("python3 not found") rather than erroring: a
// missing interpreter is expected in some environments, not a bug.
func (pythonExtractor) Extract(root string, relpaths []string, maxContext int) (extract.Result, error) {
	if maxContext <= 0 {
		maxContext = defaultMaxContext
	}

	var res extract.Result
	if len(relpaths) == 0 {
		return res, nil
	}

	pyPath, err := exec.LookPath("python3")
	if err != nil {
		for _, rel := range relpaths {
			res.Skipped = append(res.Skipped, extract.Skipped{
				File:   filepath.ToSlash(rel),
				Reason: "python3 not found",
			})
		}
		return res, nil
	}

	tmpDir, err := os.MkdirTemp("", "cull-pyext-*")
	if err != nil {
		return extract.Result{}, fmt.Errorf("extract/python: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0o700); err != nil {
		return extract.Result{}, fmt.Errorf("extract/python: chmod temp dir: %w", err)
	}
	helperPath := filepath.Join(tmpDir, "pyext.py")
	if err := os.WriteFile(helperPath, helperSrc, 0o600); err != nil {
		return extract.Result{}, fmt.Errorf("extract/python: write helper: %w", err)
	}

	args := make([]string, 0, len(relpaths)+2)
	args = append(args, helperPath, root)
	for _, rel := range relpaths {
		args = append(args, filepath.ToSlash(rel))
	}

	cmd := exec.Command(pyPath, args...)
	cmd.Env = extract.HelperEnv(maxContext)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return extract.Result{}, fmt.Errorf("extract/python: run helper: %w (stderr: %s)", err, stderr.String())
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
			return extract.Result{}, fmt.Errorf("extract/python: decode helper output %q: %w", line, err)
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
		return extract.Result{}, fmt.Errorf("extract/python: read helper output: %w", err)
	}

	return res, nil
}

// tidyResult is the JSON the helper's --tidy mode prints on stdout.
type tidyResult struct {
	Source  *string  `json:"source"` // null: the helper refused (not UTF-8)
	Removed []string `json:"removed"`
}

// Tidy removes now-unused imports from a Python test file's source: it
// runs the embedded helper as `python3 pyext.py --tidy <relpath>`,
// feeding src on stdin, and decodes its {"source":...,"removed":[...]}
// JSON reply. root is accepted for parity with the ts package (which
// needs it to locate the typescript package) but isn't otherwise used:
// tidy mode needs no cross-file resolution. If python3 is not on PATH,
// src is returned unchanged with no error and no removals.
func Tidy(root, relpath string, src []byte) ([]byte, []string, error) {
	_ = root
	pyPath, err := exec.LookPath("python3")
	if err != nil {
		return src, nil, nil
	}

	tmpDir, err := os.MkdirTemp("", "cull-pyext-tidy-*")
	if err != nil {
		return nil, nil, fmt.Errorf("extract/python: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("extract/python: chmod temp dir: %w", err)
	}
	helperPath := filepath.Join(tmpDir, "pyext.py")
	if err := os.WriteFile(helperPath, helperSrc, 0o600); err != nil {
		return nil, nil, fmt.Errorf("extract/python: write helper: %w", err)
	}

	cmd := exec.Command(pyPath, helperPath, "--tidy", filepath.ToSlash(relpath))
	cmd.Env = helperEnvFunc(defaultMaxContext)
	cmd.Stdin = bytes.NewReader(src)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("extract/python: run helper --tidy: %w (stderr: %s)", err, stderr.String())
	}

	var result tidyResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
		return nil, nil, fmt.Errorf("extract/python: decode helper --tidy output %q: %w", stdout.Bytes(), err)
	}
	if result.Source == nil {
		return src, nil, nil
	}
	return []byte(*result.Source), result.Removed, nil
}
