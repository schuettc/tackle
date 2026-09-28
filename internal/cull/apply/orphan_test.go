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

func TestOrphanedHelpersNonGoLangIsNoop(t *testing.T) {
	got := OrphanedHelpers([]byte("def helper(): pass"), []byte(""), "python", []string{"helper()"})
	if got != nil {
		t.Fatalf("OrphanedHelpers for python = %v, want nil", got)
	}
}
