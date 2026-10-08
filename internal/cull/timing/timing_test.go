package timing

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func stringsReader(s string) *strings.Reader { return strings.NewReader(s) }

func TestAddTimingFlags(t *testing.T) {
	cases := []struct {
		kind string
		in   []string
		want []string
	}{
		{"go", []string{"go", "test", "./..."}, []string{"go", "test", "-json", "-count=1", "./..."}},
		{"go", []string{"go", "test", "-race", "-count=3", "./..."}, []string{"go", "test", "-json", "-race", "-count=3", "./..."}},
		{"go", []string{"go", "test", "-json", "./..."}, []string{"go", "test", "-count=1", "-json", "./..."}},
		{"pytest", []string{"pytest", "-n", "8", "-m", "slow"}, []string{"pytest", "-n", "8", "-m", "slow", "--durations=0", "-vv", "-p", "no:cacheprovider"}},
		{"jest", []string{"npx", "jest"}, []string{"npx", "jest"}},
	}
	for _, c := range cases {
		if got := addTimingFlags(c.kind, c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: got %v, want %v", c.in, got, c.want)
		}
	}
}

func gitInit(t *testing.T, root string) {
	t.Helper()
	for _, a := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.email=a@b", "-c", "user.name=n", "commit", "-qm", "x"}} {
		cmd := exec.Command("git", a...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func needTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skip(name + " not on PATH")
	}
}

const ciYML = `name: ci
on: push
jobs:
  t:
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
      - run: node check.mjs
      - run: make test
`

func slowModule(t *testing.T, testBody string) string {
	t.Helper()
	needTool(t, "node")
	needTool(t, "go")
	needTool(t, "git")
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/tm\n\ngo 1.22\n")
	write(t, root, "slow/slow_test.go", testBody)
	write(t, root, ".github/workflows/ci.yml", ciYML)
	write(t, root, "check.mjs", "process.exit(0)\n")
	gitInit(t, root)
	return root
}

const slowTest = `package slow

import (
	"testing"
	"time"
)

func TestQuick(t *testing.T) {}

func TestSlow(t *testing.T) { time.Sleep(400 * time.Millisecond) }
`

func TestRunEndToEndGoModule(t *testing.T) {
	root := slowModule(t, slowTest)
	rep, err := Run(context.Background(), Options{Root: root, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	var gc *CheckResult
	for i := range rep.Checks {
		if rep.Checks[i].Kind == "go" {
			gc = &rep.Checks[i]
		}
	}
	if gc == nil {
		t.Fatalf("no go check: %+v", rep.Checks)
	}
	if !gc.OK || gc.Exit != 0 || gc.Seconds < 0.4 {
		t.Errorf("go check = %+v", gc)
	}
	if !strings.Contains(strings.Join(gc.Argv, " "), "-json") || !strings.Contains(strings.Join(gc.Argv, " "), "-count=1") {
		t.Errorf("argv = %v", gc.Argv)
	}
	if len(gc.Packages) != 1 || gc.Packages[0].Name != "example.com/tm/slow" || gc.Packages[0].Share < 0.99 {
		t.Errorf("packages = %+v", gc.Packages)
	}
	if len(rep.Slowest) == 0 || !strings.HasSuffix(rep.Slowest[0].Name, "TestSlow") || rep.Slowest[0].Seconds < 0.4 {
		t.Errorf("slowest = %+v", rep.Slowest)
	}
	var script *CheckResult
	for i := range rep.Checks {
		if rep.Checks[i].Kind == "script" {
			script = &rep.Checks[i]
		}
	}
	if script == nil || !script.OK {
		t.Errorf("script check = %+v", script)
	}
	if len(rep.NotRun) != 1 || !strings.HasPrefix(rep.NotRun[0], "not run: ") {
		t.Errorf("not run = %v", rep.NotRun)
	}
	if rep.TotalSeconds < gc.Seconds || !rep.OK {
		t.Errorf("total %v ok %v", rep.TotalSeconds, rep.OK)
	}
	var text strings.Builder
	WriteText(&text, rep)
	for _, want := range []string{"total", "example.com/tm/slow", "TestSlow", "not run: "} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("text missing %q:\n%s", want, text.String())
		}
	}
}

func TestRunFailureIsReportedAndRunContinues(t *testing.T) {
	root := slowModule(t, "package slow\n\nimport \"testing\"\n\nfunc TestBad(t *testing.T) { t.Fatal(\"boom\") }\n")
	rep, err := Run(context.Background(), Options{Root: root, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK {
		t.Error("run reported OK")
	}
	var gc, script *CheckResult
	for i := range rep.Checks {
		switch rep.Checks[i].Kind {
		case "go":
			gc = &rep.Checks[i]
		case "script":
			script = &rep.Checks[i]
		}
	}
	if gc == nil || gc.OK || gc.Exit != 1 || !strings.Contains(gc.Tail, "boom") {
		t.Errorf("go check = %+v", gc)
	}
	if script == nil || !script.OK {
		t.Errorf("the script check did not run after the failure: %+v", script)
	}
}

func TestRunTimeoutIsReported(t *testing.T) {
	needTool(t, "node")
	needTool(t, "git")
	root := t.TempDir()
	write(t, root, ".github/workflows/ci.yml", "name: ci\non: push\njobs:\n  t:\n    runs-on: x\n    steps:\n      - run: node nap.mjs\n")
	write(t, root, "nap.mjs", "setTimeout(() => {}, 30000)\n")
	gitInit(t, root)
	start := time.Now()
	rep, err := Run(context.Background(), Options{Root: root, Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("timeout not enforced")
	}
	if rep.OK || len(rep.Checks) != 1 || !rep.Checks[0].TimedOut {
		t.Errorf("rep = %+v", rep)
	}
}

func TestRunKeyNotInEnvironment(t *testing.T) {
	needTool(t, "node")
	needTool(t, "git")
	t.Setenv("TYPESAFE_API_KEY", "sekrit-key-value")
	root := t.TempDir()
	write(t, root, ".github/workflows/ci.yml", "name: ci\non: push\njobs:\n  t:\n    runs-on: x\n    steps:\n      - run: node env.mjs\n")
	write(t, root, "env.mjs", "if (process.env.TYPESAFE_API_KEY) { console.log('LEAK'); process.exit(3) }\n")
	gitInit(t, root)
	rep, err := Run(context.Background(), Options{Root: root, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK || len(rep.Checks) != 1 {
		t.Errorf("key reached the command: %+v", rep.Checks)
	}
}

func TestFallbackToVerifyPlan(t *testing.T) {
	needTool(t, "go")
	needTool(t, "git")
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/fb\n\ngo 1.22\n")
	write(t, root, "p/p_test.go", "package p\n\nimport \"testing\"\n\nfunc TestP(t *testing.T) {}\n")
	gitInit(t, root)
	rep, err := Run(context.Background(), Options{Root: root, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Checks) != 1 || rep.Checks[0].Kind != "go" || !rep.Checks[0].OK || len(rep.Checks[0].Packages) != 1 {
		t.Errorf("checks = %+v", rep.Checks)
	}
}

func TestHintsFromGoMedianAndSetenvFinding(t *testing.T) {
	body := `package slow

import (
	"testing"
	"time"
)

func setup(t *testing.T) { t.Setenv("X", "1"); time.Sleep(350 * time.Millisecond) }

func TestA(t *testing.T) { setup(t) }
func TestB(t *testing.T) { setup(t) }
func TestC(t *testing.T) { setup(t) }
`
	root := slowModule(t, body)
	rep, err := Run(context.Background(), Options{Root: root, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Hints) != 1 {
		t.Fatalf("hints = %+v", rep.Hints)
	}
	h := rep.Hints[0].Message
	if !strings.Contains(h, "3 tests average 0.3") || !strings.Contains(h, "setup calls t.Setenv") || !strings.Contains(h, "can't run in parallel") {
		t.Errorf("hint = %q", h)
	}
}

func TestPythonSetupHint(t *testing.T) {
	r := ParsePytest(open(t, "pytest_xdist.txt"))
	hints := pythonHints(r)
	if len(hints) != 1 || !strings.Contains(hints[0].Message, "tests/test_y.py") || !strings.Contains(hints[0].Message, "75%") {
		t.Errorf("hints = %+v", hints)
	}
}

func TestAddTimingFlagsPytestBeforeDashDashAndDropsQuiet(t *testing.T) {
	cases := []struct{ in, want []string }{
		{[]string{"pytest", "-q", "tests"}, []string{"pytest", "tests", "--durations=0", "-vv", "-p", "no:cacheprovider"}},
		{[]string{"pytest", "-qq", "-x"}, []string{"pytest", "-x", "--durations=0", "-vv", "-p", "no:cacheprovider"}},
		{[]string{"pytest", "-n", "2", "--", "a.py", "-q"}, []string{"pytest", "-n", "2", "--durations=0", "-vv", "-p", "no:cacheprovider", "--", "a.py", "-q"}},
	}
	for _, c := range cases {
		if got := addTimingFlags("pytest", c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: got %v, want %v", c.in, got, c.want)
		}
	}
}

func wfRoot(t *testing.T, steps string, files map[string]string) string {
	t.Helper()
	needTool(t, "node")
	needTool(t, "git")
	root := t.TempDir()
	write(t, root, ".github/workflows/ci.yml", "name: ci\non: push\njobs:\n  t:\n    runs-on: x\n    steps:\n"+steps)
	for k, v := range files {
		write(t, root, k, v)
	}
	gitInit(t, root)
	return root
}

func TestRunAppliesCheckEnvButNeverTheKey(t *testing.T) {
	root := wfRoot(t, "      - run: MY_VAR=hello TYPESAFE_API_KEY=zzz node env.mjs\n",
		map[string]string{"env.mjs": "if (process.env.MY_VAR !== 'hello' || process.env.TYPESAFE_API_KEY) process.exit(3)\n"})
	rep, err := Run(context.Background(), Options{Root: root, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK || len(rep.Checks) != 1 {
		t.Errorf("rep = %+v", rep)
	}
}

func TestRunExpandsShellWords(t *testing.T) {
	root := wfRoot(t, "      - run: |\n          WORD=ok node args.mjs $WORD *.txt 'a b'\n",
		map[string]string{"args.mjs": "const a = process.argv.slice(2).join('|'); if (a !== 'ok|x.txt|a b') { console.log(a); process.exit(3) }\n", "x.txt": ""})
	rep, err := Run(context.Background(), Options{Root: root, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK || len(rep.Checks) != 1 {
		t.Errorf("rep = %+v", rep)
	}
}

func TestRunNotRunWhenExpansionDependsOnElsewhere(t *testing.T) {
	root := wfRoot(t, "      - run: |\n          pkgs=./...\n          node args.mjs $pkgs\n",
		map[string]string{"args.mjs": "process.exit(3)\n"})
	rep, err := Run(context.Background(), Options{Root: root, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Checks) != 0 || len(rep.NotRun) != 1 || !strings.Contains(rep.NotRun[0], "depends on $pkgs set elsewhere in the step") {
		t.Errorf("rep = %+v", rep)
	}
}

func TestRunCancelledBeforeStart(t *testing.T) {
	root := wfRoot(t, "      - run: node a.mjs\n      - run: node b.mjs\n",
		map[string]string{"a.mjs": "", "b.mjs": ""})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep, err := Run(ctx, Options{Root: root, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range rep.NotRun {
		if strings.Contains(s, "not run: cancelled") {
			n++
		}
	}
	if len(rep.Checks) != 0 || n != 2 || rep.OK {
		t.Errorf("rep = %+v", rep)
	}
}

func TestRunCancelledDuringCheckSkipsTheRest(t *testing.T) {
	root := wfRoot(t, "      - run: node a.mjs\n      - run: node b.mjs\n",
		map[string]string{"a.mjs": "setTimeout(() => {}, 30000)\n", "b.mjs": ""})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(400 * time.Millisecond); cancel() }()
	rep, err := Run(ctx, Options{Root: root, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Checks) != 1 || rep.Checks[0].TimedOut || rep.OK {
		t.Errorf("checks = %+v", rep.Checks)
	}
	if len(rep.NotRun) != 1 || !strings.Contains(rep.NotRun[0], "not run: cancelled") {
		t.Errorf("not run = %v", rep.NotRun)
	}
}

func TestTimeoutKillsTheProcessGroup(t *testing.T) {
	root := wfRoot(t, "      - run: node nap.mjs\n", map[string]string{"nap.mjs": `import { spawn } from 'node:child_process'
spawn('sh', ['-c', 'sleep 3; echo late > late.txt'], { stdio: 'ignore' })
setTimeout(() => {}, 30000)
`})
	rep, err := Run(context.Background(), Options{Root: root, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Checks) != 1 || !rep.Checks[0].TimedOut {
		t.Fatalf("rep = %+v", rep)
	}
	time.Sleep(4 * time.Second)
	if _, err := os.Stat(filepath.Join(root, "late.txt")); err == nil {
		t.Error("the grandchild survived the timeout")
	}
}

func TestHintsWhenCheckDirIsBelowModuleRoot(t *testing.T) {
	needTool(t, "go")
	body := `package slow

import (
	"testing"
	"time"
)

func setup(t *testing.T) { t.Setenv("X", "1"); time.Sleep(350 * time.Millisecond) }

func TestA(t *testing.T) { setup(t) }
func TestB(t *testing.T) { setup(t) }
func TestC(t *testing.T) { setup(t) }
`
	root := wfRoot(t, "      - working-directory: mod/inner\n        run: go test ./...\n", map[string]string{
		"mod/go.mod": "module example.com/m\n\ngo 1.22\n", "mod/inner/slow/slow_test.go": body})
	rep, err := Run(context.Background(), Options{Root: root, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Hints) != 1 || !strings.Contains(rep.Hints[0].Message, "example.com/m/inner/slow") {
		t.Fatalf("hints = %+v checks=%+v", rep.Hints, rep.Checks)
	}
}
