package timing

import (
	"math"
	"os"
	"testing"
)

func open(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.001 }

func goPkg(t *testing.T, r GoResult, name string) GoPackage {
	t.Helper()
	for _, p := range r.Packages {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("package %q not in %+v", name, r.Packages)
	return GoPackage{}
}

func TestParseGoPassFailSkipNoTests(t *testing.T) {
	r := ParseGo(open(t, "go_pass_fail.jsonl"))
	a := goPkg(t, r, "example.com/fx/alpha")
	if a.Status != "pass" || !near(a.Seconds, 0.528) {
		t.Errorf("alpha = %+v", a)
	}
	var slow *TestTime
	for i := range a.Tests {
		if a.Tests[i].Name == "TestSlow" {
			slow = &a.Tests[i]
		}
	}
	if slow == nil || !near(slow.Seconds, 0.3) || slow.Status != "pass" {
		t.Errorf("TestSlow = %+v in %+v", slow, a.Tests)
	}
	if a.Subtests != 2 {
		t.Errorf("subtests = %d, want 2", a.Subtests)
	}
	// median over top-level tests: TestFast 0, TestSlow 0.3, TestTable 0.04, TestSkipped 0 (skipped ones are left out)
	if !near(a.MedianSeconds, 0.04) {
		t.Errorf("median = %v, want 0.04", a.MedianSeconds)
	}
	if a.Top != 3 {
		t.Errorf("top-level ran = %d, want 3", a.Top)
	}
	b := goPkg(t, r, "example.com/fx/beta")
	if b.Status != "fail" || !near(b.Seconds, 0.19) {
		t.Errorf("beta = %+v", b)
	}
	g := goPkg(t, r, "example.com/fx/gamma")
	if !g.NoTests || g.Status != "skip" {
		t.Errorf("gamma = %+v", g)
	}
}

func TestParseGoBuildFailure(t *testing.T) {
	r := ParseGo(open(t, "go_build_fail.jsonl"))
	if len(r.Packages) != 1 {
		t.Fatalf("packages = %+v", r.Packages)
	}
	p := r.Packages[0]
	if p.Name != "example.com/fx/bad" || p.Status != "fail" || !p.BuildFailed || len(p.Tests) != 0 {
		t.Errorf("pkg = %+v", p)
	}
}

func TestParseGoIgnoresNoise(t *testing.T) {
	r := ParseGo(stringsReader("go: downloading x\n{not json\n{\"Action\":\"pass\",\"Package\":\"p\",\"Elapsed\":1.5}\n"))
	if len(r.Packages) != 1 || !near(r.Packages[0].Seconds, 1.5) {
		t.Errorf("r = %+v", r)
	}
}

func pyFile(t *testing.T, r PyResult, name string) PyFile {
	t.Helper()
	for _, f := range r.Files {
		if f.File == name {
			return f
		}
	}
	t.Fatalf("file %q not in %+v", name, r.Files)
	return PyFile{}
}

func TestParsePytestDurationsParametrizedAndSummary(t *testing.T) {
	r := ParsePytest(open(t, "pytest_plain.txt"))
	if r.Passed != 4 || r.Skipped != 1 || r.Failed != 0 || !near(r.Seconds, 5.02) {
		t.Errorf("summary = %+v", r)
	}
	ship := pyFile(t, r, "tests/test_ship.py")
	if !near(ship.Seconds, 3.3) || !near(ship.SetupSeconds, 2.5) || !near(ship.SetupShare, 2.5/3.3) {
		t.Errorf("ship = %+v", ship)
	}
	shop := pyFile(t, r, "tests/test_shop.py")
	if !near(shop.Seconds, 1.6) {
		t.Errorf("shop = %+v", shop)
	}
	found := false
	for _, tt := range r.Tests {
		if tt.Name == "tests/test_shop.py::test_discount[bulk order]" && near(tt.Seconds, 1.2) {
			found = true
		}
	}
	if !found {
		t.Errorf("parametrized id with a space missing: %+v", r.Tests)
	}
}

func TestParsePytestFailure(t *testing.T) {
	r := ParsePytest(open(t, "pytest_fail.txt"))
	if r.Passed != 1 || r.Failed != 1 || !near(r.Seconds, 0.65) {
		t.Errorf("summary = %+v", r)
	}
	if f := pyFile(t, r, "tests/test_a.py"); !near(f.Seconds, 0.6) {
		t.Errorf("file = %+v", f)
	}
}

func TestParsePytestXdist(t *testing.T) {
	r := ParsePytest(open(t, "pytest_xdist.txt"))
	if r.Passed != 3 || !near(r.Seconds, 4.8) {
		t.Errorf("summary = %+v", r)
	}
	y := pyFile(t, r, "tests/test_y.py")
	if !near(y.Seconds, 4.0) || !near(y.SetupShare, 0.75) {
		t.Errorf("y = %+v", y)
	}
	x := pyFile(t, r, "tests/test_x.py")
	if !near(x.Seconds, 1.0) || !near(x.SetupShare, 0.25) {
		t.Errorf("x = %+v", x)
	}
	if r.Files[0].File != "tests/test_y.py" {
		t.Errorf("files not by time: %+v", r.Files)
	}
}

func TestParsePytestNoTests(t *testing.T) {
	r := ParsePytest(open(t, "pytest_none.txt"))
	if len(r.Files) != 0 || r.Passed != 0 || !near(r.Seconds, 0.01) {
		t.Errorf("r = %+v", r)
	}
}
