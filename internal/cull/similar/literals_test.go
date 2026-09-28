package similar

import (
	"reflect"
	"testing"
)

func TestLiteralsPython(t *testing.T) {
	body := "def test_values():\n" +
		"    \"\"\"docstring \"z\" 5 should be ignored.\"\"\"\n" +
		"    # a comment \"x\"\n" +
		"    a = 'a'\n" +
		"    b = \"b\"\n" +
		"    c = 3\n" +
		"    d = 2.5\n"
	got := Literals("python", body)
	want := []string{"a", "b", "3", "2.5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Literals(python) = %v, want %v", got, want)
	}
}

func TestLiteralsGo(t *testing.T) {
	body := "func TestValues(t *testing.T) {\n" +
		"\t// a line comment `nope`\n" +
		"\tz := `goodbye`\n" +
		"\tn := 7\n" +
		"}\n"
	got := Literals("go", body)
	want := []string{"goodbye", "7"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Literals(go) = %v, want %v", got, want)
	}
}

func TestDistinguishing(t *testing.T) {
	body := func(v string) string {
		return "def test():\n    cfg = 'cfg'\n    x = '" + v + "'\n"
	}
	bodies := []string{body("a"), body("b"), body("c")}
	rows := Distinguishing("python", bodies)
	want := [][]string{{"a"}, {"b"}, {"c"}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("Distinguishing = %v, want %v", rows, want)
	}

	// An exact duplicate pair: identical literals in both bodies, so
	// nothing distinguishes them from each other; both rows are empty.
	dup := []string{body("a"), body("a")}
	dupRows := Distinguishing("python", dup)
	for i, row := range dupRows {
		if len(row) != 0 {
			t.Errorf("Distinguishing dup row %d = %v, want empty", i, row)
		}
	}
}

// TestLiteralsDropsOnlyTheSignature (I-2): decorators, inline test.each
// tables and one-liner bodies keep their literals; only the signature
// (Python def header, Go func line up to "{", TS title) is dropped.
func TestLiteralsDropsOnlyTheSignature(t *testing.T) {
	cases := []struct {
		name, lang, body string
		want             []string
	}{
		{"python parametrize", "python",
			"@pytest.mark.parametrize(\"x,y\", [(1, \"a\"), (2, \"b\")])\ndef test_p(x, y):\n    assert f(x) == y\n",
			[]string{"x,y", "1", "a", "2", "b"}},
		{"python multi-line signature", "python",
			"def test_d(\n    a: int = 5,\n    b: str = \"s\",\n) -> None:\n    assert a == 6\n",
			[]string{"6"}},
		{"python async one-liner", "python",
			"async def test_o(): assert g(\"q\") == 3\n",
			[]string{"q", "3"}},
		{"go one-liner", "go",
			"func TestA(t *testing.T) { check(t, \"a\", 2) }",
			[]string{"a", "2"}},
		{"go doc comment", "go",
			"// TestB checks \"x\".\nfunc TestB(t *testing.T) {\n\tcheck(t, \"b\")\n}",
			[]string{"b"}},
		{"ts test.each", "typescript",
			"test.each([[\"a\", 1], [\"b\", 2]])(\"adds %s\", (x, n) => {\n  expect(f(x)).toBe(n);\n})",
			[]string{"a", "1", "b", "2"}},
		{"ts leading comment", "typescript",
			"// note \"c\"\ntest(\"title 3\", () => {\n  expect(g(\"v\")).toBe(4);\n})",
			[]string{"v", "4"}},
		{"ts one-liner", "typescript",
			"it('t 9', () => expect(h('w')).toBe(5))",
			[]string{"w", "5"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Literals(c.lang, c.body); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Literals(%s) = %q, want %q", c.lang, got, c.want)
			}
		})
	}
}
