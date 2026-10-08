package speed

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func scan(t *testing.T, root string, files ...string) []Finding {
	t.Helper()
	fs, err := Scan(root, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

// brief renders findings as "file:line kind seconds".
func brief(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		s := "-"
		if f.Seconds != nil {
			s = fmt.Sprint(*f.Seconds)
		}
		out = append(out, fmt.Sprintf("%s:%d %s %s", f.File, f.Line, f.Kind, s))
	}
	return out
}

func expect(t *testing.T, fs []Finding, want ...string) {
	t.Helper()
	got := brief(fs)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("findings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestGoFixedWaits(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module x\n")
	write(t, root, "a/a_test.go", `package a

import (
	"testing"
	"time"
)

func TestA(t *testing.T) {
	time.Sleep(200 * time.Millisecond)
	time.Sleep(2 * time.Second)
	time.Sleep(time.Second)
	d := 3 * time.Second
	time.Sleep(d)
	time.Sleep(1 * time.Second) // cull: keep
	// cull: keep
	time.Sleep(1 * time.Second)
	_ = "time.Sleep(9 * time.Second)"
	// time.Sleep(9 * time.Second)
}
`)
	expect(t, scan(t, root, "a/a_test.go"),
		"a/a_test.go:9 fixed_wait 0.2",
		"a/a_test.go:10 fixed_wait 2",
		"a/a_test.go:11 fixed_wait 1",
		"a/a_test.go:13 fixed_wait -",
	)
}

func TestGoSetenvThroughChain(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module x\n")
	write(t, root, "helper/helper.go", `package helper

import "testing"

func New(t *testing.T) int {
	return inner(t)
}

func inner(t *testing.T) int {
	return deep(t)
}

func deep(t *testing.T) int {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USER", "x")
	return 1
}
`)
	write(t, root, "svc/svc_test.go", `package svc

import (
	"testing"

	"x/helper"
)

func TestOne(t *testing.T)   { helper.New(t) }
func TestTwo(t *testing.T)   { helper.New(t) }
func TestThree(t *testing.T) { helper.New(t) }
func TestFour(t *testing.T)  { _ = 4 }
`)
	write(t, root, "other/other_test.go", `package other

import "testing"

func TestA(t *testing.T) { t.Setenv("A", "1") }
func TestB(t *testing.T) {}
func TestC(t *testing.T) {}
func TestD(t *testing.T) {}
func TestE(t *testing.T) {}
`)
	fs := scan(t, root, "svc/svc_test.go", "other/other_test.go")
	expect(t, fs, "helper/helper.go:5 setenv_blocks_parallel -")
	if !strings.Contains(fs[0].Detail, "3 of 4") || !strings.Contains(fs[0].Detail, "svc") {
		t.Errorf("detail = %q", fs[0].Detail)
	}
}

func TestGoSetenvKeep(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module x\n")
	write(t, root, "p/p_test.go", `package p

import "testing"

func env(t *testing.T) {
	// cull: keep
	t.Setenv("A", "1")
}

func TestA(t *testing.T) { env(t) }
func TestB(t *testing.T) { env(t) }
`)
	expect(t, scan(t, root, "p/p_test.go"))
}

func TestPythonFindings(t *testing.T) {
	root := t.TempDir()
	write(t, root, "tests/test_a.py", `import time
from time import sleep


def test_a(page):
    time.sleep(0.5)
    time.sleep(2)
    time.sleep(n)
    time.sleep(1)  # cull: keep
    # cull: keep
    time.sleep(1)
    s = "time.sleep(9)"
    # time.sleep(9)
    """
    time.sleep(9)
    """
    page.screenshot(path="x.png")
`)
	expect(t, scan(t, root, "tests/test_a.py"),
		"tests/test_a.py:6 fixed_wait 0.5",
		"tests/test_a.py:7 fixed_wait 2",
		"tests/test_a.py:8 fixed_wait -",
		"tests/test_a.py:17 screenshot -",
	)
}

func TestJSFindings(t *testing.T) {
	root := t.TempDir()
	write(t, root, "web/probe.mjs", `import { x } from './x.mjs';
async function run(pg) {
  await pg.waitForTimeout(250);
  await pg.waitForTimeout(1500); // cull: keep
  // cull: keep
  await pg.waitForTimeout(1500);
  await new Promise((r) => setTimeout(r, 2000));
  setTimeout(() => {}, 9000);
  await pg.waitForSelector('#a').catch(() => {});
  await pg.click('#b').catch(() => {});
  await pg.waitForFunction(() => true, { timeout: 5000 });
  await pg.waitForFunction((n) => n, 3, { timeout: 5000, polling: 50 });
  await pg.screenshot({ path: 'a.png' });
  const s = "pg.waitForTimeout(9999)";
  const t = `+"`pg.waitForTimeout(${9999})`"+`;
  // pg.waitForTimeout(9999)
  /* pg.waitForTimeout(9999)
     pg.screenshot() */
  try {
    await waitForReady(pg);
  } catch (e) {
    // ignored
  }
  try {
    await waitForReady(pg);
  } catch (e) {
    console.log(e);
  }
  await expect(pg.locator('#c')).toBeVisible({ timeout: 100 }).catch(() => undefined);
  const re = /waitForTimeout\(5\)/;
}
`)
	expect(t, scan(t, root, "web/probe.mjs"),
		"web/probe.mjs:3 fixed_wait 0.25",
		"web/probe.mjs:7 fixed_wait 2",
		"web/probe.mjs:9 swallowed_wait -",
		"web/probe.mjs:11 misplaced_timeout -",
		"web/probe.mjs:13 screenshot -",
		"web/probe.mjs:20 swallowed_wait -",
		"web/probe.mjs:29 swallowed_wait -",
	)
}

func TestJSMisplacedOnlyTwoArgs(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.mjs", "pg.waitForFunction(fn, { timeout: 1 });\npg.waitForFunction(fn, arg, { timeout: 1 });\npg.waitForFunction(fn, { polling: 5 });\n")
	expect(t, scan(t, root, "a.mjs"), "a.mjs:1 misplaced_timeout -")
}

func TestEnclosingTest(t *testing.T) {
	root := t.TempDir()
	body := "def test_a():\n    time.sleep(1)\n\n\ndef test_b():\n    time.sleep(2)\n"
	write(t, root, "t.py", body)
	split := strings.Index(body, "def test_b")
	fs, err := Scan(root, []string{"t.py"}, map[string][]Span{"t.py": {{ID: "t.py::test_a", Start: 0, End: split}, {ID: "t.py::test_b", Start: split, End: len(body)}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 || fs[0].Test != "t.py::test_a" || fs[1].Test != "t.py::test_b" {
		t.Errorf("%+v", fs)
	}
}

func TestDigest(t *testing.T) {
	two, one := 2.0, 1.0
	fs := []Finding{
		{Kind: KindScreenshot, File: "a", Line: 1},
		{Kind: KindFixedWait, File: "a", Line: 2, Seconds: &one},
		{Kind: KindFixedWait, File: "a", Line: 3, Seconds: &two},
		{Kind: KindSetenv, File: "b", Line: 1},
	}
	d := Digest(fs)
	if d.FixedWaitSeconds != 3 || d.Counts[KindFixedWait] != 2 || d.Counts[KindScreenshot] != 1 {
		t.Errorf("%+v", d)
	}
	var order []string
	for _, f := range d.Top {
		order = append(order, brief([]Finding{f})[0])
	}
	want := "b:1 setenv_blocks_parallel -|a:3 fixed_wait 2|a:2 fixed_wait 1|a:1 screenshot -"
	if strings.Join(order, "|") != want {
		t.Errorf("top = %s\nwant %s", strings.Join(order, "|"), want)
	}
	many := make([]Finding, 30)
	for i := range many {
		many[i] = Finding{Kind: KindScreenshot, File: "a", Line: i + 1}
	}
	if len(Digest(many).Top) != 20 {
		t.Error("top is capped at 20")
	}
	var sb strings.Builder
	WriteText(&sb, d)
	if !strings.Contains(sb.String(), "a:3  fixed_wait") {
		t.Errorf("text:\n%s", sb.String())
	}
}
