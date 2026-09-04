package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/schuettc/tackle/internal/creel"
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

	code := finish(&errbuf, status, creel.Result{Action: creel.Added, Name: "K", Dest: "/tmp/.env"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	b, _ := os.ReadFile(status)
	if string(b) != "added\n" {
		t.Fatalf("token = %q, want added", string(b))
	}
}
