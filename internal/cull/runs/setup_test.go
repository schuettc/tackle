package runs

import (
	"reflect"
	"strings"
	"testing"
)

const probeJustfile = `_deps:
    cd web && npm ci
    cd web && npm run --silent kit-types

probe: _deps
    go build -o bin/x ./cmd/x
    cd web && X="$PWD/../bin/x" node probe.mjs
`

func TestJustCheckCarriesDependencyAndEarlierLinesAsSetup(t *testing.T) {
	needJust(t)
	root := write(t, map[string]string{
		"justfile":                 probeJustfile,
		"web/probe.mjs":            "",
		"web/package.json":         `{"scripts":{"kit-types":"echo"}}`,
		".github/workflows/ci.yml": "jobs:\n  a:\n    steps:\n      - run: just probe\n",
	})
	got := find(t, root)
	c := pick(t, got, "script", "node probe.mjs")
	if c.Dir != "web" || !reflect.DeepEqual(c.Env, []string{"X=$PWD/../bin/x"}) {
		t.Fatalf("check = %+v", c)
	}
	var argvs [][]string
	for _, s := range c.Setup {
		argvs = append(argvs, s.Argv)
	}
	want := [][]string{{"npm", "ci"}, {"npm", "run", "--silent", "kit-types"}, {"go", "build", "-o", "bin/x", "./cmd/x"}}
	if !reflect.DeepEqual(argvs, want) {
		t.Fatalf("setup = %v, want %v", argvs, want)
	}
	if c.Setup[0].Dir != "web" || c.Setup[2].Dir != "." || c.Setup[2].From == "" {
		t.Errorf("setup = %+v", c.Setup)
	}
}

func TestWorkflowRunStepSetupAndTestsAreNotSetup(t *testing.T) {
	got := wfRun(t, "npm ci && npx jest\ngo test ./... && npx vitest", nil)
	j := pick(t, got, "jest", "npx jest")
	if len(j.Setup) != 1 || !reflect.DeepEqual(j.Setup[0].Argv, []string{"npm", "ci"}) {
		t.Fatalf("jest setup = %+v", j.Setup)
	}
	v := pick(t, got, "vitest", "npx vitest")
	for _, s := range v.Setup {
		if s.Argv[0] == "go" {
			t.Errorf("a test check became setup: %+v", v.Setup)
		}
	}
}

func TestSetupSkipsConditionalsAndPipelines(t *testing.T) {
	got := wfRun(t, "[ -d node_modules ] || npm ci\nmake build | tee log\nnpx jest", nil)
	j := pick(t, got, "jest", "npx jest")
	for _, s := range j.Setup {
		t.Errorf("unexpected setup %+v", s)
	}
}

func TestSetupFirstSeenWinsOnMerge(t *testing.T) {
	got := wfRun(t, "npm ci\nnpx jest\nnpm run build\nnpx jest", nil)
	j := pick(t, got, "jest", "npx jest")
	if len(j.Setup) != 1 || j.Setup[0].Argv[1] != "ci" {
		t.Fatalf("setup = %+v", j.Setup)
	}
}

func TestLefthookCommandSetup(t *testing.T) {
	root := write(t, map[string]string{"lefthook.yml": "pre-push:\n  commands:\n    t:\n      run: npm ci && npx jest\n"})
	j := pick(t, find(t, root), "jest", "npx jest")
	if len(j.Setup) != 1 {
		t.Fatalf("setup = %+v", j.Setup)
	}
}

func TestSiblingCheckDependenciesDoNotSetUpEachOther(t *testing.T) {
	needJust(t)
	root := write(t, map[string]string{
		"justfile":                 "all: a b\n\na:\n    touch x\n    node a.mjs\n\nb:\n    node b.mjs\n",
		"a.mjs":                    "",
		"b.mjs":                    "",
		".github/workflows/ci.yml": "jobs:\n  j:\n    steps:\n      - run: just all\n",
	})
	got := find(t, root)
	if b := pick(t, got, "script", "node b.mjs"); len(b.Setup) != 0 {
		t.Errorf("b setup = %+v", b.Setup)
	}
	if a := pick(t, got, "script", "node a.mjs"); len(a.Setup) != 1 {
		t.Errorf("a setup = %+v", a.Setup)
	}
}

func setupArgvs(c Check) []string {
	var out []string
	for _, s := range c.Setup {
		out = append(out, strings.Join(s.Argv, " "))
	}
	return out
}

func TestSetupOnlyUnconditionalCommands(t *testing.T) {
	body := "for d in a b; do echo $d; done\n" +
		"if [ ! -d node_modules ]; then npm install; fi\n" +
		"if ! command -v foo; then curl -o foo x; fi\n" +
		"test -d dist && rm -rf dist\n" +
		"go build -o bin/x ./cmd/x\n" +
		"go test ./..."
	c := pick(t, wfRun(t, body, nil), "go", "go test ./...")
	if got := setupArgvs(c); !reflect.DeepEqual(got, []string{"go build -o bin/x ./cmd/x"}) {
		t.Fatalf("setup = %v", got)
	}
}

func TestSetupMultilineBlocksAndChains(t *testing.T) {
	body := "if [ -z \"$CI\" ]\nthen\n  npm install\nelif x\nthen\n  yarn\nelse\n  pnpm i\nfi\n" +
		"while true; do\n  sleep 1\ndone\n" +
		"case $X in\n a) prep a;;\nesac\n" +
		"function f {\n  prep f\n}\n" +
		"a1 || b1 && c1\n" +
		"which foo && prep foo\n" +
		"command -v bar && prep bar\n" +
		"command prep ok\n" +
		"prep last\nnpx jest"
	c := pick(t, wfRun(t, body, nil), "jest", "npx jest")
	if got := setupArgvs(c); !reflect.DeepEqual(got, []string{"prep ok", "prep last"}) {
		t.Fatalf("setup = %v", got)
	}
}

func TestJustIgnoredFailureLineIsNotSetup(t *testing.T) {
	needJust(t)
	root := write(t, map[string]string{
		"justfile":                 "probe:\n    -rm -f x\n    prep dep\n    node a.mjs\n",
		"a.mjs":                    "",
		".github/workflows/ci.yml": "jobs:\n  j:\n    steps:\n      - run: just probe\n",
	})
	c := pick(t, find(t, root), "script", "node a.mjs")
	if got := setupArgvs(c); !reflect.DeepEqual(got, []string{"prep dep"}) {
		t.Fatalf("setup = %v", got)
	}
}
