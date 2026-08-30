package proj

import "testing"

func TestQueryEmptyOnBadSocket(t *testing.T) {
	// A socket that cannot exist yields "" (degradation), never a panic.
	if got := Query("/nonexistent/proj-nope", "=nope", "#{session_name}"); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}
