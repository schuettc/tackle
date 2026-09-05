package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCommandsJSONCoversEveryCommand(t *testing.T) {
	isolate(t)
	var out bytes.Buffer
	if code := run(t.TempDir(), []string{"commands", "--json"}, &out); code != 0 {
		t.Fatalf("scratch commands --json: got exit %d, want 0", code)
	}
	var got []map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	names := map[string]bool{}
	for _, c := range got {
		names[c["name"].(string)] = true
	}
	for _, want := range []string{"path", "print", "append", "help", "man", "commands", "version"} {
		if !names[want] {
			t.Fatalf("commands --json missing %q; got %v", want, names)
		}
	}
}

func TestManRenders(t *testing.T) {
	isolate(t)
	var out bytes.Buffer
	if code := run(t.TempDir(), []string{"man"}, &out); code != 0 {
		t.Fatalf("scratch man: got exit %d, want 0", code)
	}
	if !strings.Contains(out.String(), ".TH SCRATCH 1") {
		t.Fatalf("man output wrong: %s", out.String())
	}
}
