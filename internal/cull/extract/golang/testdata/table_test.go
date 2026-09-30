package calc

import "testing"

func TestTable(t *testing.T) {
	cases := []struct {
		name string
		in   int
	}{
		{"one", 1},
		{"two", 2},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if Add(tc.in, 0) != tc.in {
				t.Fatal("bad")
			}
		})
	}
}
