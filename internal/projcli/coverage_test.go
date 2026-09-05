package projcli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCommandsJSONCoversEveryCommand(t *testing.T) {
	out, code := captureStdout(t, []string{"commands", "--json"})
	if code != 0 {
		t.Fatalf("proj commands --json: got exit %d, want 0", code)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	names := map[string]bool{}
	for _, c := range got {
		names[c["name"].(string)] = true
	}
	for _, want := range []string{"list", "current", "new", "sidebar", "help", "man", "commands", "version"} {
		if !names[want] {
			t.Fatalf("commands --json missing %q; got %v", want, names)
		}
	}
}

func TestManRenders(t *testing.T) {
	out, code := captureStdout(t, []string{"man"})
	if code != 0 {
		t.Fatalf("proj man: got exit %d, want 0", code)
	}
	if !strings.Contains(out, ".TH PROJ 1") {
		t.Fatalf("man output wrong: %s", out)
	}
}
