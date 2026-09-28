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
