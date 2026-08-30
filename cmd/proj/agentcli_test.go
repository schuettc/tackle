package main

import (
	"testing"
)

func TestParseSidebarArgs(t *testing.T) {
	// missing session → error
	if _, err := parseSidebarArgs([]string{}); err == nil {
		t.Fatal("want error when session is missing")
	}
	// too many positional args → error
	if _, err := parseSidebarArgs([]string{"sess1", "extra"}); err == nil {
		t.Fatal("want error on extra positional arg")
	}
	// session only
	sa, err := parseSidebarArgs([]string{"my-session"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sa.session != "my-session" || sa.socket != "" || sa.dir != "" {
		t.Fatalf("unexpected result: %+v", sa)
	}
	// --socket and --dir flags parsed correctly
	sa, err = parseSidebarArgs([]string{"--socket", "proj-tools", "--dir", "/tmp/work", "my-session"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sa.session != "my-session" {
		t.Errorf("session: got %q, want %q", sa.session, "my-session")
	}
	if sa.socket != "proj-tools" {
		t.Errorf("socket: got %q, want %q", sa.socket, "proj-tools")
	}
	if sa.dir != "/tmp/work" {
		t.Errorf("dir: got %q, want %q", sa.dir, "/tmp/work")
	}
	// flags after positional
	sa, err = parseSidebarArgs([]string{"sess2", "--socket", "proj-x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sa.session != "sess2" || sa.socket != "proj-x" {
		t.Fatalf("unexpected result: %+v", sa)
	}
}

func TestParseNewArg(t *testing.T) {
	p, w, err := parseNewTarget("tools-workspace/nfl cutover")
	if err != nil {
		t.Fatal(err)
	}
	if p != "tools-workspace" || w != "nfl-cutover" {
		t.Fatalf("%q %q", p, w)
	}
	if _, _, err := parseNewTarget("noslash"); err == nil {
		t.Fatal("want error on missing /")
	}
	if _, _, err := parseNewTarget("p/bad.name"); err == nil {
		t.Fatal("want error on invalid work")
	}
}
