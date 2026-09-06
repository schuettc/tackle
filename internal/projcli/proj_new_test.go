package projcli

import (
	"bytes"
	"slices"
	"testing"
)

func TestSplitCommandTail(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantHead    []string
		wantCommand []string
		wantHasCmd  bool
	}{
		{
			name:     "no command",
			args:     []string{"x/y", "--agent", "pi", "--no-sidebar"},
			wantHead: []string{"x/y", "--agent", "pi", "--no-sidebar"},
		},
		{
			name:        "--command with spaces and leading-dash args",
			args:        []string{"x/y", "--json", "--command", "hail", "shim", "--id", "a b c", "-x"},
			wantHead:    []string{"x/y", "--json"},
			wantCommand: []string{"hail", "shim", "--id", "a b c", "-x"},
			wantHasCmd:  true,
		},
		{
			name:        "bare -- terminator",
			args:        []string{"x/y", "--", "echo", "-n", "hi there"},
			wantHead:    []string{"x/y"},
			wantCommand: []string{"echo", "-n", "hi there"},
			wantHasCmd:  true,
		},
		{
			name:       "--command with empty tail",
			args:       []string{"x/y", "--command"},
			wantHead:   []string{"x/y"},
			wantHasCmd: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			head, command, hasCmd := splitCommandTail(c.args)
			if !slices.Equal(head, c.wantHead) {
				t.Errorf("head = %q, want %q", head, c.wantHead)
			}
			if !slices.Equal(command, c.wantCommand) {
				t.Errorf("command = %q, want %q", command, c.wantCommand)
			}
			if hasCmd != c.wantHasCmd {
				t.Errorf("hasCmd = %v, want %v", hasCmd, c.wantHasCmd)
			}
		})
	}
}

func TestEncodeNewJSONShape(t *testing.T) {
	var buf bytes.Buffer
	err := encodeNewJSON(&buf, newJSON{
		Name:    "tools-workspace/fix-tests",
		Project: "tools-workspace",
		Work:    "fix-tests",
		Socket:  "proj-tools-workspace",
		Dir:     "/Users/court/GitHub/schuettc/tools-workspace",
		Pane:    "%12",
		Created: true,
	})
	if err != nil {
		t.Fatalf("encodeNewJSON: %v", err)
	}
	want := `{"name":"tools-workspace/fix-tests","project":"tools-workspace","work":"fix-tests","socket":"proj-tools-workspace","dir":"/Users/court/GitHub/schuettc/tools-workspace","pane":"%12","created":true}` + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("json =\n  %s\nwant\n  %s", got, want)
	}
}
