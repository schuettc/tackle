package runs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// write lays out a synthetic project under a temp dir.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func needJust(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just is not on PATH")
	}
}

func find(t *testing.T, root string) []Check {
	t.Helper()
	got, err := Find(root)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// pick returns the one check of kind whose argv joins to argv.
func pick(t *testing.T, got []Check, kind, argv string) Check {
	t.Helper()
	var hit []Check
	for _, c := range got {
		if c.Kind == kind && strings.Join(c.Argv, " ") == argv {
			hit = append(hit, c)
		}
	}
	if len(hit) != 1 {
		t.Fatalf("want exactly one %s %q, got %d in %+v", kind, argv, len(hit), got)
	}
	return hit[0]
}

func hasFrom(c Check, sub string) bool {
	for _, f := range c.From {
		if strings.Contains(f, sub) {
			return true
		}
	}
	return false
}

func TestWorkflowRunForms(t *testing.T) {
	root := write(t, map[string]string{
		".github/workflows/ci.yml": `name: ci
jobs:
  py:
    runs-on: x
    steps:
      - uses: actions/checkout@v4
      - run: pip install -r req.txt
      - run: venv/bin/ruff check src
      - run: venv/bin/pytest -q -n 8 -m "not slow and not solver"
  js:
    runs-on: x
    defaults:
      run:
        working-directory: web
    steps:
      - run: npm ci
      - run: npx jest
        working-directory: infra/cdk
      - run: |
          cd pkg && FOO=1 go test ./... -race
          echo done
      - run: python -m pytest tests/unit -k fast
`,
	})
	got := find(t, root)

	py := pick(t, got, "pytest", `venv/bin/pytest -q -n 8 -m not slow and not solver`)
	if py.Dir != "." {
		t.Errorf("pytest dir = %q", py.Dir)
	}
	if len(py.From) != 1 || py.From[0] != ".github/workflows/ci.yml:9 run" {
		t.Errorf("pytest from = %v", py.From)
	}
	if c := pick(t, got, "jest", "npx jest"); c.Dir != "infra/cdk" {
		t.Errorf("jest dir = %q", c.Dir)
	}
	g := pick(t, got, "go", "go test ./... -race")
	if g.Dir != "web/pkg" {
		t.Errorf("go dir = %q (job default dir + cd)", g.Dir)
	}
	if !hasFrom(g, "ci.yml:20 run") {
		t.Errorf("go from = %v (block scalar line numbers)", g.From)
	}
	pick(t, got, "pytest", "python -m pytest tests/unit -k fast")

	for _, c := range got {
		if c.Kind == "unknown" {
			t.Errorf("checkout, installs and linters must not be listed: %+v", c)
		}
		if strings.Contains(strings.Join(c.Argv, " "), "ruff") || strings.Contains(strings.Join(c.Argv, " "), "pip") {
			t.Errorf("linter or install listed: %+v", c)
		}
	}
}

func TestUsesAndUnparseable(t *testing.T) {
	root := write(t, map[string]string{
		".github/workflows/ci.yml": `jobs:
  gate:
    steps:
      - uses: actions/checkout@v4
      - uses: example/tools-actions/go-ci@v1
        with:
          x: y
      - uses: ./.github/workflows/checks.yml
      - run: scripts/run-suite.sh --fast
      - run: scripts/export-locks.sh
      - run: make test
      - run: make build
      - run: ${{ matrix.cmd }}
`,
	})
	got := find(t, root)
	u := pick(t, got, "unknown", "uses example/tools-actions/go-ci@v1")
	if u.Note == "" || !hasFrom(u, "ci.yml:5 uses") {
		t.Errorf("uses check = %+v", u)
	}
	pick(t, got, "unknown", "scripts/run-suite.sh --fast")
	pick(t, got, "unknown", "make test")
	for _, c := range got {
		a := strings.Join(c.Argv, " ")
		if strings.Contains(a, "export-locks") || strings.Contains(a, "make build") || strings.Contains(a, "checkout") {
			t.Errorf("not a test command, must be omitted: %+v", c)
		}
	}
}

func TestExpressionInCommandIsUnknown(t *testing.T) {
	root := write(t, map[string]string{
		".github/workflows/ci.yml": `jobs:
  a:
    steps:
      - run: pytest ${{ matrix.args }}
`,
	})
	got := find(t, root)
	c := pick(t, got, "unknown", "pytest ${{ matrix.args }}")
	if c.Note == "" {
		t.Error("an unknown check says why")
	}
}

const justDirs = `set shell := ["bash", "-euo", "pipefail", "-c"]
web := "app/web"

verify: prepare gate extra

verify-slow: probe

prepare:

gate:
    #!/usr/bin/env bash
    set -euo pipefail
    f="$HOME/x/local.sh"
    bash "$f"

extra: _deps
    cd {{ web }} && npm test
    cd {{ web }} && npm run --silent typecheck

_deps:
    cd {{ web }} && { [ -d node_modules ] || npm ci; }

probe: _deps
    go build -o bin/x ./cmd/x
    cd {{ web }} && KIT_BROWSER=required npm run --silent probe

only group: _deps
    cd {{ web }} && PROBE_ONLY={{ group }} node probe.mjs

nested:
    just extra
    just only apply

loop-a:
    just loop-b

loop-b:
    just loop-a
`

func justFixture() map[string]string {
	return map[string]string{
		"justfile": justDirs,
		".github/workflows/ci.yml": `jobs:
  a:
    steps:
      - run: just verify
      - run: just verify-slow
      - run: just nested
      - run: just loop-a
`,
		"app/web/package.json": `{"scripts":{"test":"node --test *.test.ts","probe":"node probe.mjs","typecheck":"tsc"}}`,
		"app/web/probe.mjs":    "import { a } from './helper.mjs';\nimport './side.mjs'\nconst b = await import('./lazy.js');\nconst c = require('./cjs.cjs');\nexport * from './re.mjs';\nimport x from 'pkg';\n",
		"app/web/helper.mjs":   "import { z } from './deep/z.mjs'\nimport { back } from './probe.mjs'\n",
		"app/web/side.mjs":     "",
		"app/web/lazy.js":      "",
		"app/web/cjs.cjs":      "",
		"app/web/re.mjs":       "",
		"app/web/deep/z.mjs":   "import {\n  q,\n  r,\n} from '../outside.mjs'\n",
		"app/web/outside.mjs":  "",
	}
}

func TestJustRecipesFollowedAndMerged(t *testing.T) {
	needJust(t)
	got := find(t, write(t, justFixture()))

	nt := pick(t, got, "node-test", "node --test *.test.ts")
	if nt.Dir != "app/web" {
		t.Errorf("node-test dir = %q (cd via just variable, then the package dir)", nt.Dir)
	}
	// reached from verify, and from `just nested` -> `just extra`: all origins kept
	if len(nt.From) < 2 {
		t.Errorf("merged origins = %v", nt.From)
	}
	if !hasFrom(nt, "ci.yml:4 run > justfile verify > justfile extra > app/web/package.json test") {
		t.Errorf("chain missing: %v", nt.From)
	}
	if !hasFrom(nt, "justfile nested > justfile extra") {
		t.Errorf("just call chain missing: %v", nt.From)
	}

	probe := pick(t, got, "script", "node probe.mjs")
	if probe.Dir != "app/web" {
		t.Errorf("script dir = %q", probe.Dir)
	}
	// the entry plus local imports, transitively, nothing outside the project's own files
	want := []string{
		"app/web/cjs.cjs", "app/web/deep/z.mjs", "app/web/helper.mjs", "app/web/lazy.js",
		"app/web/outside.mjs", "app/web/probe.mjs", "app/web/re.mjs", "app/web/side.mjs",
	}
	if strings.Join(probe.Files, ",") != strings.Join(want, ",") {
		t.Errorf("files = %v\nwant    %v", probe.Files, want)
	}
	if !hasFrom(probe, "justfile verify-slow > justfile probe > app/web/package.json probe") {
		t.Errorf("probe from = %v", probe.From)
	}
	// the recipe parameter is bound from `just only apply`
	pick(t, got, "script", "node probe.mjs") // same (kind, dir, argv) as the npm one: one check

	// the shebang recipe's downloaded script
	gate := pick(t, got, "unknown", `bash $f`)
	if gate.Note == "" || !hasFrom(gate, "justfile gate") {
		t.Errorf("gate = %+v", gate)
	}
	// loops terminate, typecheck and npm ci are not checks
	for _, c := range got {
		a := strings.Join(c.Argv, " ")
		if strings.Contains(a, "tsc") || strings.Contains(a, "npm ci") || strings.Contains(a, "go build") {
			t.Errorf("not a check: %+v", c)
		}
	}
}

func TestJustWithoutJustIsNotUnderstood(t *testing.T) {
	old := dumpJust
	dumpJust = func(string) ([]byte, error) { return nil, errors.New("exec: \"just\": not found") }
	defer func() { dumpJust = old }()
	root := write(t, map[string]string{
		"justfile": "test:\n    go test ./...\n",
		".github/workflows/ci.yml": `jobs:
  a:
    steps:
      - run: just test
`,
	})
	got := find(t, root)
	c := pick(t, got, "unknown", "just test")
	if !strings.Contains(c.Note, "just") {
		t.Errorf("note = %q", c.Note)
	}
}

func TestLefthook(t *testing.T) {
	needJust(t)
	root := write(t, map[string]string{
		"justfile": "verify:\n    go test ./...\n",
		"lefthook.yml": `pre-commit:
  commands:
    gofmt:
      glob: "*.go"
      run: |
        test -z "$(gofmt -l {staged_files})" || { gofmt -l {staged_files}; exit 1; }
pre-push:
  commands:
    verify:
      run: just verify
    unit:
      root: svc/
      run: pytest -q
`,
	})
	got := find(t, root)
	g := pick(t, got, "go", "go test ./...")
	if !hasFrom(g, "lefthook.yml:10 pre-push verify > justfile verify") {
		t.Errorf("from = %v", g.From)
	}
	p := pick(t, got, "pytest", "pytest -q")
	if p.Dir != "svc" {
		t.Errorf("lefthook root: dir = %q", p.Dir)
	}
	if len(got) != 2 {
		t.Errorf("gofmt is a formatter: %+v", got)
	}
}

func TestNpmForms(t *testing.T) {
	root := write(t, map[string]string{
		".github/workflows/ci.yml": `jobs:
  a:
    steps:
      - run: npm test
        working-directory: sub/inner
      - run: pnpm run unit
      - run: yarn lint
      - run: npx vitest run src
        working-directory: front
      - run: npm run missing
`,
		"package.json":           `{"scripts":{"unit":"vitest run","lint":"eslint ."}}`,
		"sub/package.json":       `{"scripts":{"test":"npm run build && jest --ci","build":"tsc"}}`,
		"sub/inner/readme.md":    "",
		"front/package.json":     `{"scripts":{}}`,
		"sub/inner/ignored.json": "{}",
	})
	got := find(t, root)
	j := pick(t, got, "jest", "jest --ci")
	if j.Dir != "sub" {
		t.Errorf("npm runs scripts in the package root: dir = %q", j.Dir)
	}
	if !hasFrom(j, "ci.yml:4 run > sub/package.json test") {
		t.Errorf("from = %v", j.From)
	}
	if c := pick(t, got, "vitest", "vitest run"); c.Dir != "." {
		t.Errorf("pnpm run dir = %q", c.Dir)
	}
	if c := pick(t, got, "vitest", "npx vitest run src"); c.Dir != "front" {
		t.Errorf("vitest dir = %q", c.Dir)
	}
	if m := pick(t, got, "unknown", "npm run missing"); !strings.Contains(m.Note, "missing") {
		t.Errorf("a script that is not there is listed, not dropped: %+v", m)
	}
	if len(got) != 4 {
		t.Errorf("want 4 checks, got %+v", got)
	}
}

func TestEnvPrefixCdChainsAndSubshell(t *testing.T) {
	root := write(t, map[string]string{
		".github/workflows/ci.yml": `jobs:
  a:
    steps:
      - run: |
          (cd one && go test ./a) && go test ./b
          cd two/three && A=1 B="x y" go test -run 'T 1' ./...
          pytest > out.txt 2>&1
`,
	})
	got := find(t, root)
	if c := pick(t, got, "go", "go test ./a"); c.Dir != "one" {
		t.Errorf("subshell dir = %q", c.Dir)
	}
	if c := pick(t, got, "go", "go test ./b"); c.Dir != "." {
		t.Errorf("after the subshell dir = %q", c.Dir)
	}
	if c := pick(t, got, "go", "go test -run T 1 ./..."); c.Dir != "two/three" {
		t.Errorf("cd chain dir = %q", c.Dir)
	}
	if c := pick(t, got, "pytest", "pytest"); c.Dir != "two/three" {
		t.Errorf("redirects are not argv, dir persists across lines: %+v", c)
	}
}

func TestNodeScriptDirsAndExclusions(t *testing.T) {
	root := write(t, map[string]string{
		".github/workflows/ci.yml": `jobs:
  a:
    steps:
      - run: node node_modules/playwright-core/cli.js install chromium
      - run: node -e "console.log(1)"
      - run: node --import tsx --test a.test.ts
      - run: node tools/run.mjs
`,
		"tools/run.mjs": "import './dep.mjs'",
		"tools/dep.mjs": "",
	})
	got := find(t, root)
	pick(t, got, "node-test", "node --import tsx --test a.test.ts")
	s := pick(t, got, "script", "node tools/run.mjs")
	if strings.Join(s.Files, ",") != "tools/dep.mjs,tools/run.mjs" {
		t.Errorf("files = %v", s.Files)
	}
	if len(got) != 2 {
		t.Errorf("installs of tool binaries and -e are not checks: %+v", got)
	}
}

func TestEmptyProject(t *testing.T) {
	got := find(t, t.TempDir())
	if len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}
