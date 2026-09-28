package apply

import (
	"reflect"
	"sort"
	"testing"
)

func TestOrphanedHelpers(t *testing.T) {
	before := `package a

func helper() int {
	return 42
}

func stillUsed() int {
	return 1
}

func TestA(t *testing.T) {
	if helper() != 42 {
		t.Fail()
	}
	if stillUsed() != 1 {
		t.Fail()
	}
}

func TestB(t *testing.T) {
	if stillUsed() != 1 {
		t.Fail()
	}
}
`
	removedBody := `func TestA(t *testing.T) {
	if helper() != 42 {
		t.Fail()
	}
	if stillUsed() != 1 {
		t.Fail()
	}
}`

	// after: TestA's span removed, TestB (which also calls stillUsed)
	// remains, so stillUsed is still referenced; helper is not.
	after := `package a

func helper() int {
	return 42
}

func stillUsed() int {
	return 1
}

func TestB(t *testing.T) {
	if stillUsed() != 1 {
		t.Fail()
	}
}
`

	got := OrphanedHelpers([]byte(before), []byte(after), "go", []string{removedBody})
	sort.Strings(got)
	want := []string{"helper"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OrphanedHelpers = %v, want %v", got, want)
	}
}

func TestOrphanedHelpersNoneRemoved(t *testing.T) {
	src := `package a

func helper() int { return 1 }

func TestA(t *testing.T) {
	if helper() != 1 {
		t.Fail()
	}
}
`
	got := OrphanedHelpers([]byte(src), []byte(src), "go", nil)
	if len(got) != 0 {
		t.Fatalf("OrphanedHelpers with nothing removed = %v, want none", got)
	}
}

func TestOrphanedHelpersUnknownLangIsNoop(t *testing.T) {
	got := OrphanedHelpers([]byte("def helper(): pass"), []byte(""), "ruby", []string{"helper()"})
	if got != nil {
		t.Fatalf("OrphanedHelpers for ruby = %v, want nil", got)
	}
}

// TestOrphanedHelpersPython (I-6): top-level def/class/NAME= helpers and
// fixtures (matched as parameters) used only by the removed test.
func TestOrphanedHelpersPython(t *testing.T) {
	keep := "def test_keep(shared):\n    assert shared == 1\n"
	removed := "def test_gone(db, shared):\n    assert make_user(db) == LIMIT\n    assert Box(shared)\n"
	head := "import pytest\n\nLIMIT = 3\n\n\n@pytest.fixture\ndef db():\n    return {}\n\n\n@pytest.fixture\ndef shared():\n    return 1\n\n\ndef make_user(d):\n    return 3\n\n\nclass Box:\n    pass\n\n\n"
	before := head + keep + "\n\n" + removed
	after := head + keep
	got := OrphanedHelpers([]byte(before), []byte(after), "python", []string{removed})
	want := []string{"Box", "LIMIT", "db", "make_user"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("orphans = %v, want %v", got, want)
	}
}

// TestOrphanedHelpersTS (I-6): top-level function/class/const/let/var,
// optionally exported.
func TestOrphanedHelpersTS(t *testing.T) {
	keep := "test(\"keep\", () => {\n  expect(shared()).toBe(1);\n});\n"
	removed := "test(\"gone\", () => {\n  expect(build(LIMIT)).toEqual(new Box(shared(), counter));\n})"
	head := "export const LIMIT = 3;\nlet counter = 0;\nexport function build(n: number) {\n  return n;\n}\nclass Box {\n  constructor(a: number, b: number) {}\n}\nfunction shared() {\n  return 1;\n}\n\n"
	before := head + keep + "\n" + removed + ";\n"
	after := head + keep
	got := OrphanedHelpers([]byte(before), []byte(after), "typescript", []string{removed})
	want := []string{"Box", "LIMIT", "build", "counter"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("orphans = %v, want %v", got, want)
	}
}
