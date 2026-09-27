package similar

import (
	"testing"

	"github.com/schuettc/tackle/internal/cull/cases"
)

func TestNormPython(t *testing.T) {
	body := "def test_add():\n" +
		"    \"\"\"Docstring here, should be ignored.\"\"\"\n" +
		"    # a comment\n" +
		"    x = 40  # inline comment\n" +
		"    y = \"hello world\"\n" +
		"    z = 'goodbye'\n" +
		"    assert add(x, 2.5) == y\n"
	// Expected computed by running group.py's norm() on this exact body
	// via python3 -c (see task-4-report.md for the transcript).
	want := "x = N y = S z = S assert add(x, N) == y"
	got := Norm("python", body)
	if got != want {
		t.Errorf("Norm(python) = %q, want %q", got, want)
	}
}

func TestNormGo(t *testing.T) {
	body := "func TestAdd(t *testing.T) {\n" +
		"\t// a line comment\n" +
		"\t/* a block\n" +
		"\t   comment */\n" +
		"\tx := 40 // inline comment\n" +
		"\ty := \"hello world\"\n" +
		"\tz := `goodbye`\n" +
		"\tif add(x, 2.5) != y {\n" +
		"\t\tt.Fail()\n" +
		"\t}\n" +
		"}\n"
	want := "x := N y := S z := S if add(x, N) != y { t.Fail() } }"
	got := Norm("go", body)
	if got != want {
		t.Errorf("Norm(go) = %q, want %q", got, want)
	}
}

func TestRatioMatchesDifflib(t *testing.T) {
	// Ratios computed by python3's difflib.SequenceMatcher(None, a,
	// b).ratio() (default autojunk=True), rounded to 4 decimals. See
	// task-4-report.md for the exact commands and output.
	longRepeated := func(ch byte, n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = ch
		}
		return string(b)
	}
	cases := []struct {
		name string
		a, b string
		want float64
	}{
		{"identical", "hello world", "hello world", 1.0},
		{"disjoint", "abcdef", "ghijkl", 0.0},
		{"similar_short", "x = N y = S assert add(x, N) == y", "x = N y = S assert add(x, N) == z", 0.9697},
		{"partial_overlap", "the quick brown fox jumps over the lazy dog", "the quick brown fox leaps over the lazy dog", 0.9302},
		{"empty_vs_nonempty", "", "nonempty", 0.0},
		{
			"autojunk_long", // len(b) = 250 >= 200, "A" is popular (> 250//100+1 = 3 times)
			longRepeated('A', 50) + "B" + longRepeated('A', 50),
			longRepeated('A', 250),
			0.2849,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Ratio(c.a, c.b)
			// round to 4 decimals for comparison
			gotR := float64(int(got*10000+0.5)) / 10000
			if gotR != c.want {
				t.Errorf("Ratio(%q, %q) = %v, want %v", c.name, c.name, gotR, c.want)
			}
		})
	}
}

func mkCase(id, file, body string) cases.TestCase {
	return cases.TestCase{
		ID:        id,
		Lang:      "python",
		Framework: "pytest",
		File:      file,
		Body:      body,
	}
}

func TestGroupsNoChaining(t *testing.T) {
	// a and b are similar; b and c are similar; a and c are not. No
	// chaining means a,b,c should NOT all end up in one group: c only
	// joins a cluster if it's similar to EVERY member.
	body := func(tail string) string {
		return "def test():\n    x = 1\n    y = 2\n    z = 3\n    w = 4\n    v = 5\n    u = 6\n    assert x == " + tail + "\n"
	}
	a := mkCase("a", "f.py", body("1"))
	b := mkCase("b", "f.py", body("2"))
	c := mkCase("c", "f.py", "def test():\n    q = 99\n    r = 'totally different'\n    s = [1,2,3,4,5,6,7]\n    return q\n")

	groups := Groups([]cases.TestCase{a, b, c})
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d: %+v", len(groups), groups)
	}
	if len(groups[0].Tests) != 2 {
		t.Fatalf("expected group of 2 (no chaining to c), got %d", len(groups[0].Tests))
	}
	ids := []string{groups[0].Tests[0].ID, groups[0].Tests[1].ID}
	if ids[0] != "a" || ids[1] != "b" {
		t.Errorf("expected members [a b] sorted by id, got %v", ids)
	}
}

func TestGroupsSameFileOnly(t *testing.T) {
	body := "def test():\n    x = 1\n    y = 2\n    z = 3\n    w = 4\n    v = 5\n    u = 6\n    assert x == 1\n"
	a := mkCase("a", "f.py", body)
	b := mkCase("b", "g.py", body) // identical body, different file
	groups := Groups([]cases.TestCase{a, b})
	if len(groups) != 0 {
		t.Fatalf("expected 0 groups (different files), got %d: %+v", len(groups), groups)
	}
}

func TestGroupsMinLength(t *testing.T) {
	// Bodies whose normalized form is < 40 chars never group, even if
	// identical.
	short := "def t():\n    x = 1\n"
	a := mkCase("a", "f.py", short)
	b := mkCase("b", "f.py", short)
	groups := Groups([]cases.TestCase{a, b})
	if len(groups) != 0 {
		t.Fatalf("expected 0 groups (bodies below min length), got %d: %+v", len(groups), groups)
	}
}

func TestGroupIDStable(t *testing.T) {
	body := "def test():\n    x = 1\n    y = 2\n    z = 3\n    w = 4\n    v = 5\n    u = 6\n    assert x == 1\n"
	a := mkCase("a", "f.py", body)
	b := mkCase("b", "f.py", body)

	g1 := Groups([]cases.TestCase{a, b})
	g2 := Groups([]cases.TestCase{b, a})
	if len(g1) != 1 || len(g2) != 1 {
		t.Fatalf("expected 1 group in each run, got %d and %d", len(g1), len(g2))
	}
	if g1[0].ID != g2[0].ID {
		t.Errorf("GroupID not stable across member order: %q != %q", g1[0].ID, g2[0].ID)
	}
	want := cases.GroupID([]string{"a", "b"})
	if g1[0].ID != want {
		t.Errorf("GroupID = %q, want %q", g1[0].ID, want)
	}
	if g1[0].Lang != "python" || g1[0].Framework != "pytest" || g1[0].File != "f.py" {
		t.Errorf("group metadata not copied from members: %+v", g1[0])
	}
}
