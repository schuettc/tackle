package calc

import "testing"

// TestAdd checks addition.
func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatalf("bad sum")
	}
}

func TestParse(t *testing.T) {
	n := prepare()
	t.Run("quoted value", func(t *testing.T) {
		if n < 0 {
			t.Fatal("bad")
		}
		if _, err := Parse("a"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if _, err := Parse(""); err != nil {
			t.Fatal(err)
		}
	})
}

// prepare is a small test helper used by TestParse's subtests.
func prepare() int {
	return 1
}
