package calc

// Add returns the sum of a and b.
func Add(a, b int) int {
	return a + b
}

// Parse pretends to parse s, returning its length.
func Parse(s string) (int, error) {
	return len(s), nil
}
