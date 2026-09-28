package cli

import (
	"strings"
	"testing"
)

// TestBriefBadMaxReturnsError verifies that passing a non-integer value via
// --max to 'casebook brief' produces a non-zero exit code instead of silently
// using the default (the old behaviour before the fmt.Sscanf check was added).
func TestBriefBadMaxReturnsError(t *testing.T) {
	e := setup(t)
	// Initialize casebook so the brief command can open the app and reach the
	// --max parsing step (before the fix it silently used 8 when Sscanf failed).
	e.ok("init", e.remote, "--machine", "mbp", "--user", "schuettc", "--root", e.root)

	code, _, errw := e.run("", "brief", "--cwd", e.clone, "--max", "notanumber")
	if code == 0 {
		t.Fatal("expected non-zero exit for --max=notanumber, got 0")
	}
	if !strings.Contains(errw, "--max") {
		t.Errorf("expected error mentioning --max, got: %q", errw)
	}
}
