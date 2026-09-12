package creel

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want args
		err  bool
	}{
		{"bare", nil, args{}, false},
		{"name only", []string{"OPENAI_API_KEY"}, args{name: "OPENAI_API_KEY"}, false},
		{"name + dest + status", []string{"K", "--dest", "config/.env", "--status-file", "/tmp/s"},
			args{name: "K", dest: "config/.env", statusFile: "/tmp/s"}, false},
		{"eq form", []string{"K", "--dest=.env", "--status-file=/tmp/s"},
			args{name: "K", dest: ".env", statusFile: "/tmp/s"}, false},
		{"event-file", []string{"K", "--event-file", "/tmp/e.json"},
			args{name: "K", eventFile: "/tmp/e.json"}, false},
		{"event-file eq", []string{"K", "--event-file=/tmp/e.json"},
			args{name: "K", eventFile: "/tmp/e.json"}, false},
		{"dangling event-file", []string{"--event-file"}, args{}, true},
		{"flags before name", []string{"--dest", ".env", "K"}, args{name: "K", dest: ".env"}, false},
		{"dangling dest", []string{"--dest"}, args{}, true},
		{"unknown flag", []string{"--nope"}, args{}, true},
		{"two names", []string{"A", "B"}, args{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.argv)
			if tt.err {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseArgs(%v) = %+v, want %+v", tt.argv, got, tt.want)
			}
		})
	}
}

func TestRunRejectsInvalidNameAndWritesStatus(t *testing.T) {
	dir := t.TempDir()
	status := filepath.Join(dir, "status")
	var out, errbuf bytes.Buffer

	// An invalid arg-provided name must fail fast (no TUI) and record an error
	// token for the harness.
	code := run(dir, []string{"BAD-NAME", "--status-file", status}, &out, &errbuf)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	b, err := os.ReadFile(status)
	if err != nil {
		t.Fatalf("status not written: %v", err)
	}
	if got := string(b); got == "" || got[:6] != "error:" {
		t.Fatalf("status = %q, want an error: token", got)
	}
}

func TestFinishWritesToken(t *testing.T) {
	dir := t.TempDir()
	status := filepath.Join(dir, "s")
	var errbuf bytes.Buffer

	code := finish(&errbuf, status, "", Result{Action: Added, Name: "K", Dest: "/tmp/.env"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	b, _ := os.ReadFile(status)
	if string(b) != "added\n" {
		t.Fatalf("token = %q, want added", string(b))
	}
}

func TestFinishWritesEventOnSaveNotOnCancel(t *testing.T) {
	dir := t.TempDir()
	var errbuf bytes.Buffer

	// A real save writes a value-free event with name, dest, and action.
	evt := filepath.Join(dir, "save.json")
	finish(&errbuf, "", evt, Result{Action: Updated, Name: "STRIPE_KEY", Dest: "/p/.env"})
	b, err := os.ReadFile(evt)
	if err != nil {
		t.Fatalf("event not written: %v", err)
	}
	got := string(b)
	for _, want := range []string{`"name":"STRIPE_KEY"`, `"dest":"/p/.env"`, `"action":"updated"`} {
		if !bytes.Contains(b, []byte(want)) {
			t.Fatalf("event %q missing %q", got, want)
		}
	}

	// A cancel is not a save: no event file.
	cancelEvt := filepath.Join(dir, "cancel.json")
	finish(&errbuf, "", cancelEvt, Result{Action: Cancelled, Name: "K", Dest: "/p/.env"})
	if _, err := os.Stat(cancelEvt); !os.IsNotExist(err) {
		t.Fatalf("cancel wrote an event file, want none (err=%v)", err)
	}
}
