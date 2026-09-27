package calc

import "testing"

// TestUnicodeFirst references héllo ✓ in its body.
func TestUnicodeFirst(t *testing.T) {
	s := "héllo ✓"
	if len(s) == 0 {
		t.Fatal("empty")
	}
}

// TestUnicodeSecond runs after multi-byte content to check span offsets.
func TestUnicodeSecond(t *testing.T) {
	if Add(1, 1) != 2 {
		t.Fatal("bad")
	}
}
