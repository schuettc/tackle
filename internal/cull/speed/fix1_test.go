package speed

import (
	"fmt"
	"strings"
	"testing"
)

func TestSetenvOneFindingPerPackage(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module x\n")
	write(t, root, "p/p_test.go", `package p

import "testing"

func envA(t *testing.T) { t.Setenv("A", "1") }
func envB(t *testing.T) { t.Setenv("B", "1") }
func envC(t *testing.T) { envB(t) }

func TestA(t *testing.T) { envA(t) }
func TestB(t *testing.T) { envA(t); envC(t) }
func TestC(t *testing.T) { envA(t) }
func TestD(t *testing.T) { t.Setenv("D", "1") }
func TestE(t *testing.T) { t.Setenv("E", "1") }
func TestF(t *testing.T) {}
`)
	fs := scan(t, root, "p/p_test.go")
	if len(fs) != 1 {
		t.Fatalf("want one finding, got %v", brief(fs))
	}
	d := fs[0].Detail
	if !strings.Contains(d, "envA calls t.Setenv") || !strings.Contains(d, "used by 3 of 6 tests in p") ||
		!strings.Contains(d, "so none of them can run in parallel") || !strings.Contains(d, "1 other helpers also call t.Setenv") {
		t.Errorf("detail = %q", d)
	}
}

func TestSetenvServeShape(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module x\n")
	write(t, root, "testgit/testgit.go", `package testgit

import "testing"

func Env(t *testing.T) { t.Setenv("HOME", t.TempDir()) }
`)
	write(t, root, "apptest/apptest.go", `package apptest

import (
	"testing"

	"x/testgit"
)

func New(t *testing.T) int {
	testgit.Env(t)
	t.Setenv("PORT", "1")
	return 1
}
`)
	body := "package serve\n\nimport (\n\t\"testing\"\n\n\t\"x/apptest\"\n)\n\n"
	for i := 0; i < 5; i++ {
		body += fmt.Sprintf("func TestS%d(t *testing.T) { apptest.New(t) }\n", i)
	}
	body += "func TestPlain(t *testing.T) {}\n"
	write(t, root, "serve/serve_test.go", body)
	fs := scan(t, root, "serve/serve_test.go")
	if len(fs) != 1 || fs[0].File != "apptest/apptest.go" || !strings.Contains(fs[0].Detail, "New calls t.Setenv") ||
		!strings.Contains(fs[0].Detail, "5 of 6 tests in serve") {
		t.Errorf("%v %+v", brief(fs), fs)
	}
}

func TestSetenvOnlyDirectNeedsHalf(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module x\n")
	write(t, root, "a/a_test.go", `package a

import "testing"

func TestA(t *testing.T) { t.Setenv("A", "1") }
func TestB(t *testing.T) { t.Setenv("A", "1") }
func TestC(t *testing.T) {}
func TestD(t *testing.T) {}
func TestE(t *testing.T) {}
`)
	expect(t, scan(t, root, "a/a_test.go"))
	write(t, root, "b/b_test.go", `package b

import "testing"

func TestA(t *testing.T) { t.Setenv("A", "1") }
func TestB(t *testing.T) { t.Setenv("A", "1") }
func TestC(t *testing.T) {}
func TestD(t *testing.T) {}
`)
	if fs := scan(t, root, "b/b_test.go"); len(fs) != 1 {
		t.Errorf("%v", brief(fs))
	}
}

func TestSetenvReceiverMustBeTestingParam(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module x\n")
	write(t, root, "c/c_test.go", `package c

import "testing"

type cfg struct{}

func (cfg) Setenv(k, v string) {}

func apply(c cfg) { c.Setenv("A", "1") }

func TestA(t *testing.T) { apply(cfg{}) }
func TestB(t *testing.T) { var e cfg; e.Setenv("A", "1") }
func TestC(t *testing.T) { apply(cfg{}) }
`)
	expect(t, scan(t, root, "c/c_test.go"))
	write(t, root, "d/d_test.go", `package d

import "testing"

func env(tb testing.TB) { tb.Setenv("A", "1") }

func TestA(tt *testing.T) { env(tt) }
func TestB(x *testing.T) { x.Run("s", func(q *testing.T) { q.Setenv("A", "1") }) }
`)
	if fs := scan(t, root, "d/d_test.go"); len(fs) != 1 {
		t.Errorf("%v", brief(fs))
	}
}

func TestSetTimeoutOnlyResolveIsAWait(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.mjs", `await new Promise((resolve) => setTimeout(resolve, 300));
await new Promise((r) => setTimeout(() => r(), 400));
await new Promise((r) => setTimeout(() => r(1), 500));
await new Promise((r) => { child.kill(); setTimeout(() => child.kill('SIGKILL'), 2000); r(); });
await new Promise((r) => setTimeout(() => { cleanup(); r(); }, 3000));
`)
	expect(t, scan(t, root, "a.mjs"),
		"a.mjs:1 fixed_wait 0.3",
		"a.mjs:2 fixed_wait 0.4",
		"a.mjs:3 fixed_wait 0.5",
	)
}

func TestSleepHelpers(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.mjs", `const sleep = (ms) => new Promise(r => setTimeout(r, ms));
function nap(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
async function run() {
  await sleep(500);
  await sleep(0.5*1000);
  await nap(2000);
  other.sleep(9000);
}
`)
	expect(t, scan(t, root, "a.mjs"),
		"a.mjs:6 fixed_wait 0.5",
		"a.mjs:7 fixed_wait -",
		"a.mjs:8 fixed_wait 2",
	)
}

func TestSwallowedAtCatchLine(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.mjs", `await pg
  .waitForSelector('#a')
  .catch(() => {});
await pg
  .waitForSelector('#b') // cull: keep
  .catch(() => {});
await pg
  .waitForSelector('#c')
  // cull: keep
  .catch(() => {});
await pg.waitForSelector('#d')
  .catch(() => {}); // cull: keep
`)
	expect(t, scan(t, root, "a.mjs"), "a.mjs:3 swallowed_wait -")
}

func TestDigestKeepsRareKindsInTop(t *testing.T) {
	var fs []Finding
	for i := 0; i < 30; i++ {
		s := float64(i + 1)
		fs = append(fs, Finding{Kind: KindFixedWait, File: "w", Line: i + 1, Seconds: &s})
	}
	fs = append(fs, Finding{Kind: KindScreenshot, File: "s", Line: 1},
		Finding{Kind: KindSwallowed, File: "z", Line: 1},
		Finding{Kind: KindMisplaced, File: "z", Line: 2},
		Finding{Kind: KindSetenv, File: "z", Line: 3})
	d := Digest(fs)
	if len(d.Top) != 20 {
		t.Fatalf("top = %d", len(d.Top))
	}
	var kinds []string
	for _, f := range d.Top[:4] {
		kinds = append(kinds, f.Kind)
	}
	if strings.Join(kinds, ",") != "setenv_blocks_parallel,misplaced_timeout,swallowed_wait,fixed_wait" {
		t.Errorf("top kinds = %v", kinds)
	}
	if d.Top[3].Seconds == nil || *d.Top[3].Seconds != 30 {
		t.Errorf("fixed waits are by seconds desc: %+v", d.Top[3])
	}
}

func TestScanSkipsUnreadableFile(t *testing.T) {
	root := t.TempDir()
	write(t, root, "ok.mjs", "await pg.waitForTimeout(100);\n")
	write(t, root, "dir.mjs/x", "")
	var warn strings.Builder
	fs, err := ScanTo(root, []string{"dir.mjs", "ok.mjs"}, nil, &warn)
	if err != nil || len(fs) != 1 || !strings.Contains(warn.String(), "dir.mjs") {
		t.Errorf("err=%v fs=%v warn=%q", err, brief(fs), warn.String())
	}
}
