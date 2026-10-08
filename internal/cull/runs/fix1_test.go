package runs

import (
	"strings"
	"testing"
)

// wfRun lays out a one-step workflow whose run block is body (and any extra files).
func wfRun(t *testing.T, body string, extra map[string]string) []Check {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("jobs:\n  a:\n    steps:\n      - run: |\n")
	for _, l := range strings.Split(body, "\n") {
		sb.WriteString("          " + l + "\n")
	}
	files := map[string]string{".github/workflows/ci.yml": sb.String()}
	for k, v := range extra {
		files[k] = v
	}
	return find(t, write(t, files))
}

func argvs(got []Check) []string {
	var out []string
	for _, c := range got {
		out = append(out, c.Kind+": "+strings.Join(c.Argv, " "))
	}
	return out
}

func TestTestWordsNeverExempt(t *testing.T) {
	got := wfRun(t, "make lint test\nmake lint\n./scripts/test-build.sh\n./scripts/export-locks.sh\nbash scripts/ci-lint.sh\ntask fmt", nil)
	want := []string{"unknown: make lint test", "unknown: ./scripts/test-build.sh", "unknown: bash scripts/ci-lint.sh"}
	if strings.Join(argvs(got), "|") != strings.Join(want, "|") {
		t.Errorf("got %v want %v", argvs(got), want)
	}
}

const pkgRoot = `{"scripts":{"test":"jest root"}}`

func npmFiles() map[string]string {
	return map[string]string{
		"package.json":     pkgRoot,
		"web/package.json": `{"scripts":{"test":"vitest run","e2e":"playwright test"}}`,
		"packages/a/x":     "",
	}
}

func TestNpmDirectoryFlags(t *testing.T) {
	for _, cmd := range []string{
		"npm test --prefix web", "npm --prefix web test", "npm test --prefix=web", "pnpm -C web test",
		"pnpm --filter web test", "npm test -w web", "npm run test --workspace web", "yarn --cwd web test",
	} {
		got := wfRun(t, cmd, npmFiles())
		if len(got) != 1 || got[0].Kind != "vitest" || got[0].Dir != "web" {
			t.Errorf("%q: got %+v", cmd, got)
		}
	}
	for _, cmd := range []string{"npm test -w packages/*", `pnpm --filter "@x/y..." test`, "pnpm --filter ./nope test", "npm test --prefix ../out", "npm test -w /abs"} {
		got := wfRun(t, cmd, npmFiles())
		if len(got) != 1 || got[0].Kind != "unknown" || got[0].Note == "" {
			t.Errorf("%q: want one unknown with a note, got %+v", cmd, got)
		}
	}
}

func TestNpmUnknownNeverSilent(t *testing.T) {
	got := wfRun(t, "npm run nope", map[string]string{"package.json": pkgRoot})
	if len(got) != 1 || got[0].Kind != "unknown" || !strings.Contains(got[0].Note, "nope") {
		t.Errorf("missing script: %+v", got)
	}
	got = wfRun(t, "npm test", nil)
	if len(got) != 1 || got[0].Kind != "unknown" || !strings.Contains(got[0].Note, "package.json") {
		t.Errorf("no package.json: %+v", got)
	}
	got = wfRun(t, "cd $SOME && pnpm test", map[string]string{"package.json": pkgRoot})
	if len(got) != 1 || got[0].Kind != "unknown" {
		t.Errorf("unknown dir: %+v", got)
	}
	got = wfRun(t, "pnpm install\nyarn build", map[string]string{"package.json": pkgRoot})
	if len(got) != 0 {
		t.Errorf("installs stay omitted: %+v", got)
	}
}

func TestWrappersPeeled(t *testing.T) {
	cmds := map[string]string{
		"env A=1 pytest -q":                        "pytest",
		"env -i A=1 pytest -q -x":                  "pytest",
		"timeout 60 go test ./...":                 "go",
		"timeout -s KILL 60 pytest p":              "pytest",
		"cross-env A=1 B=2 jest --ci":              "jest",
		"xvfb-run -a npx vitest run":               "vitest",
		"xvfb-run -s -ac pytest z":                 "pytest",
		"dotenv -e .env -- pytest x":               "pytest",
		"c8 --reporter lcov node --test a.test.js": "node-test",
		"nyc --reporter=text jest --ci2":           "jest",
		"nice -n 5 go test ./a":                    "go",
		"sudo -E pytest y":                         "pytest",
		"time go test ./time":                      "go",
	}
	var body []string
	for c := range cmds {
		body = append(body, c)
	}
	got := wfRun(t, strings.Join(body, "\n"), nil)
	if len(got) != len(cmds) {
		t.Errorf("got %v", argvs(got))
	}
	for c, kind := range cmds {
		pick(t, got, kind, c)
	}
}

func TestHeredocsAndMultilineQuotes(t *testing.T) {
	body := "cat > x.sh <<'EOF'\npytest -q\ngo test ./...\nEOF\ncat <<-EOT\n\tjest\n\tEOT\n" +
		"echo \"a\npytest -q\nb\"\npython -c \"\nimport os\nvitest run\n\"\ngo test ./after\nbash <<EOF\njest in\nEOF"
	got := wfRun(t, body, nil)
	if len(got) != 1 || strings.Join(got[0].Argv, " ") != "go test ./after" {
		t.Errorf("got %v", argvs(got))
	}
}

func TestShellCombinedFlags(t *testing.T) {
	got := wfRun(t, "bash -ec \"pytest -q\"\nsh -lc 'go test ./x'\nzsh -xec 'jest a'\nbash -o pipefail -c \"vitest run\"", nil)
	for _, w := range [][2]string{{"pytest", "pytest -q"}, {"go", "go test ./x"}, {"jest", "jest a"}, {"vitest", "vitest run"}} {
		pick(t, got, w[0], w[1])
	}
	if len(got) != 4 {
		t.Errorf("got %v", argvs(got))
	}
}

func TestPlaywrightCypressNeedTheTestSubcommand(t *testing.T) {
	got := wfRun(t, "npx playwright install chromium\nnpx playwright install-deps\nnpx playwright test e2e\nnpx cypress open\nnpx cypress run", nil)
	want := "unknown: npx playwright test e2e|unknown: npx cypress run"
	if strings.Join(argvs(got), "|") != want {
		t.Errorf("got %v", argvs(got))
	}
}

func TestOutsideTheProject(t *testing.T) {
	got := wfRun(t, "cd .. && pytest\ncd sub\nnode ../../x.mjs", nil)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	for _, c := range got {
		if c.Kind != "unknown" || !strings.Contains(c.Note, "outside the project") || len(c.Files) != 0 {
			t.Errorf("want unknown outside the project: %+v", c)
		}
	}
}

func TestNonBashShellStep(t *testing.T) {
	root := write(t, map[string]string{".github/workflows/ci.yml": `jobs:
  a:
    steps:
      - shell: pwsh
        run: pytest
      - shell: bash -e {0}
        run: go test ./b
`})
	got := find(t, root)
	if len(got) != 2 || got[0].Kind != "unknown" || !strings.Contains(got[0].Note, "shell pwsh not read") {
		t.Errorf("got %+v", got)
	}
	pick(t, got, "go", "go test ./b")
}

func TestJustFlagsWithoutRecipe(t *testing.T) {
	got := wfRun(t, "just --list\njust --fmt --unstable\njust --show verify\njust -q\njust --evaluate", nil)
	if len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}
