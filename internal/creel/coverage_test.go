package creel

import (
	"bytes"
	"strings"
	"testing"
)

// creel registers no subcommands of its own (single-purpose TUI launcher with
// a hand-rolled parseArgs), but v0.3.0's tools.New auto-registers version/
// help/update/man/commands. The switch in run() must route "man" (and
// "commands") to app.Dispatch for them to be reachable at all.
func TestManRenders(t *testing.T) {
	dir := t.TempDir()
	var out, errbuf bytes.Buffer
	if code := run(dir, []string{"man"}, &out, &errbuf); code != 0 {
		t.Fatalf("creel man: got exit %d, want 0 (stderr=%s)", code, errbuf.String())
	}
	if !strings.Contains(out.String(), ".TH CREEL 1") {
		t.Fatalf("man output wrong: %s", out.String())
	}
}
