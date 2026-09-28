package check

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// groupFixture writes a go.mod, .cull.toml (egress + a deterministic
// test_command), and a file with three near-duplicate tests (values
// "a", "b", "c") that check.Run groups and judges as consolidate. It
// returns the root and the group's id.
func groupFixture(t *testing.T) (root, groupID string) {
	t.Helper()
	root = t.TempDir()
	goModule(t, root)
	writeFile(t, root, ".cull.toml", "egress = true\ntest_command = \"true\"\n")
	writeFile(t, root, "pkg/calc_test.go", groupSrc("a", "b", "c"))

	f := &fakeEval{consolidate: map[string]bool{"TestFooA": true, "TestFooB": true, "TestFooC": true}}
	report, err := Run(context.Background(), f, Options{Path: root, Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Groups) != 1 {
		t.Fatalf("groups = %+v, want 1", report.Groups)
	}
	return root, report.Groups[0].ID
}

// groupSrc renders the three near-duplicate tests with the given
// distinguishing values.
func groupSrc(a, b, c string) string {
	tmpl := "func TestFoo%s(t *testing.T) {\n\tcfg := \"cfg\"\n\t_ = cfg\n\tval := \"%s\"\n\t_ = val\n\ty := 1\n\t_ = y\n}\n"
	return "package pkg\n\n" +
		fmt.Sprintf(tmpl, "A", a) + "\n" +
		fmt.Sprintf(tmpl, "B", b) + "\n" +
		fmt.Sprintf(tmpl, "C", c)
}

// tableSrc renders one table-driven test covering the given values.
func tableSrc(name string, values ...string) string {
	var b strings.Builder
	b.WriteString("package pkg\n\nfunc " + name + "(t *testing.T) {\n\tcases := []string{")
	for i, v := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("\"" + v + "\"")
	}
	b.WriteString("}\n\tfor _, val := range cases {\n\t\tcfg := \"cfg\"\n\t\t_ = cfg\n\t\t_ = val\n\t\ty := 1\n\t\t_ = y\n\t}\n}\n")
	return b.String()
}

func TestGroupCheckPassesForGoodTable(t *testing.T) {
	root, groupID := groupFixture(t)
	writeFile(t, root, "pkg/calc_test.go", tableSrc("TestFooTable", "a", "b", "c"))

	f2 := &fakeEval{}
	gc, err := CheckGroup(context.Background(), f2, root, groupID, Options{Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if !gc.OriginalsGone {
		t.Errorf("OriginalsGone = false, StillPresent = %v", gc.StillPresent)
	}
	if len(gc.NewTests) != 1 {
		t.Fatalf("NewTests = %v, want 1", gc.NewTests)
	}
	if len(gc.MissingRows) != 0 {
		t.Errorf("MissingRows = %v, want none", gc.MissingRows)
	}
	if len(gc.Flagged) != 0 {
		t.Errorf("Flagged = %v, want none", gc.Flagged)
	}
	if len(gc.Verify) == 0 || !gc.Verify[0].OK {
		t.Errorf("Verify = %+v, want a passing result", gc.Verify)
	}
	if !gc.OK {
		t.Errorf("OK = false, gc = %+v", gc)
	}
}

func TestGroupCheckMissingRow(t *testing.T) {
	root, groupID := groupFixture(t)
	writeFile(t, root, "pkg/calc_test.go", tableSrc("TestFooTable", "a", "b"))

	f2 := &fakeEval{}
	gc, err := CheckGroup(context.Background(), f2, root, groupID, Options{Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if !gc.OriginalsGone {
		t.Errorf("OriginalsGone = false, want true")
	}
	if len(gc.MissingRows) != 1 {
		t.Fatalf("MissingRows = %v, want exactly the C member", gc.MissingRows)
	}
	if !strings.HasSuffix(gc.MissingRows[0], "TestFooC") {
		t.Errorf("MissingRows = %v, want TestFooC", gc.MissingRows)
	}
	if gc.OK {
		t.Errorf("OK = true, want false")
	}
}

func TestGroupCheckOriginalStillThere(t *testing.T) {
	root, groupID := groupFixture(t)
	// No rewrite at all: all three originals are still there unchanged.

	f2 := &fakeEval{}
	gc, err := CheckGroup(context.Background(), f2, root, groupID, Options{Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if gc.OriginalsGone {
		t.Errorf("OriginalsGone = true, want false")
	}
	if len(gc.StillPresent) != 3 {
		t.Errorf("StillPresent = %v, want all 3 members", gc.StillPresent)
	}
	if gc.OK {
		t.Errorf("OK = true, want false")
	}
}

func TestGroupCheckNoNewTest(t *testing.T) {
	root, groupID := groupFixture(t)
	// Originals removed, nothing added.
	writeFile(t, root, "pkg/calc_test.go", "package pkg\n")

	f2 := &fakeEval{}
	gc, err := CheckGroup(context.Background(), f2, root, groupID, Options{Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if !gc.OriginalsGone {
		t.Errorf("OriginalsGone = false, want true")
	}
	if len(gc.NewTests) != 0 {
		t.Errorf("NewTests = %v, want none", gc.NewTests)
	}
	if gc.OK {
		t.Errorf("OK = true, want false")
	}
}

// TestGroupCheckKeptNameCountsAsNew [RF 5]: a test whose name was kept but
// whose body changed (so its hash differs) counts as new, not as an
// original still present.
func TestGroupCheckKeptNameCountsAsNew(t *testing.T) {
	root, groupID := groupFixture(t)
	// Keep the name TestFooA, but give it a new body covering all 3 rows.
	src := "package pkg\n\nfunc TestFooA(t *testing.T) {\n\tcases := []string{\"a\", \"b\", \"c\"}\n\t" +
		"for _, val := range cases {\n\t\tcfg := \"cfg\"\n\t\t_ = cfg\n\t\t_ = val\n\t\ty := 1\n\t\t_ = y\n\t}\n}\n"
	writeFile(t, root, "pkg/calc_test.go", src)

	f2 := &fakeEval{}
	gc, err := CheckGroup(context.Background(), f2, root, groupID, Options{Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if !gc.OriginalsGone {
		t.Errorf("OriginalsGone = false, StillPresent = %v, want TestFooA (changed body) not counted as present", gc.StillPresent)
	}
	if len(gc.NewTests) != 1 || !strings.HasSuffix(gc.NewTests[0], "TestFooA") {
		t.Errorf("NewTests = %v, want the rewritten TestFooA", gc.NewTests)
	}
	if len(gc.MissingRows) != 0 {
		t.Errorf("MissingRows = %v, want none", gc.MissingRows)
	}
}

func TestGroupCheckFlagsCutNewTest(t *testing.T) {
	root, groupID := groupFixture(t)
	writeFile(t, root, "pkg/calc_test.go", tableSrc("TestFooTable", "a", "b", "c"))

	f2 := &fakeEval{cut: map[string]bool{"TestFooTable": true}}
	gc, err := CheckGroup(context.Background(), f2, root, groupID, Options{Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(gc.Flagged) != 1 || !strings.HasSuffix(gc.Flagged[0], "TestFooTable") {
		t.Fatalf("Flagged = %v, want TestFooTable", gc.Flagged)
	}
	if gc.OK {
		t.Errorf("OK = true, want false")
	}
}

// TestGroupCheckUnjudgedNewTest: a Jev error on the new test's judge call
// must not leave it counted as "not flagged". It must show up in
// Unjudged and force OK false.
func TestGroupCheckUnjudgedNewTest(t *testing.T) {
	root, groupID := groupFixture(t)
	writeFile(t, root, "pkg/calc_test.go", tableSrc("TestFooTable", "a", "b", "c"))

	f2 := &fakeEval{errOnTest: map[string]bool{"TestFooTable": true}}
	gc, err := CheckGroup(context.Background(), f2, root, groupID, Options{Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(gc.Unjudged) != 1 || !strings.HasSuffix(gc.Unjudged[0], "TestFooTable") {
		t.Fatalf("Unjudged = %v, want TestFooTable", gc.Unjudged)
	}
	if gc.OK {
		t.Errorf("OK = true, want false (unjudged new test)")
	}
}

// TestGroupCheckUnjudgedGroup: a Jev error on the re-formed group's judge
// call (containing new tests) must also land in Unjudged and force OK
// false, not silently pass as keep_separate.
func TestGroupCheckUnjudgedGroup(t *testing.T) {
	root, groupID := groupFixture(t)
	// Two new near-duplicate tests that re-group together.
	src := "package pkg\n\n" +
		"func TestBarX(t *testing.T) {\n\tcfg := \"cfg\"\n\t_ = cfg\n\tval := \"a\"\n\t_ = val\n\ty := 1\n\t_ = y\n}\n\n" +
		"func TestBarY(t *testing.T) {\n\tcfg := \"cfg\"\n\t_ = cfg\n\tval := \"b\"\n\t_ = val\n\ty := 1\n\t_ = y\n}\n"
	writeFile(t, root, "pkg/calc_test.go", src)

	f2 := &fakeEval{errOnGroup: map[string]bool{"TestBarX": true}}
	gc, err := CheckGroup(context.Background(), f2, root, groupID, Options{Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(gc.Unjudged) == 0 {
		t.Fatalf("Unjudged = %v, want the group's new tests present", gc.Unjudged)
	}
	if gc.OK {
		t.Errorf("OK = true, want false (unjudged group)")
	}
}

func TestGroupCheckUnknownGroup(t *testing.T) {
	root, _ := groupFixture(t)
	f2 := &fakeEval{}
	_, err := CheckGroup(context.Background(), f2, root, "group:doesnotexist", Options{})
	if err == nil || !strings.Contains(err.Error(), "group:doesnotexist") {
		t.Fatalf("err = %v, want it to name the unknown group", err)
	}
}

func TestGroupCheckEgressGate(t *testing.T) {
	root := t.TempDir()
	goModule(t, root)
	// No .cull.toml at all: egress is not set.
	f2 := &fakeEval{}
	_, err := CheckGroup(context.Background(), f2, root, "group:anything", Options{})
	if err == nil || !strings.Contains(err.Error(), "egress = true") {
		t.Fatalf("err = %v, want an egress gate message", err)
	}
	if f2.calls.Load() != 0 {
		t.Errorf("evaluator called %d times, want 0", f2.calls.Load())
	}
}
