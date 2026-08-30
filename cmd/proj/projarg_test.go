package main

import "testing"

func TestParseProjectArgs(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantProject string
		wantAgent   string
		wantErr     bool
	}{
		{"bare project", []string{"tools"}, "tools", "", false},
		{"claude then project", []string{"--claude", "tools"}, "tools", "claude", false},
		{"project then claude", []string{"tools", "--claude"}, "tools", "claude", false},
		{"pi flag", []string{"--pi", "tools"}, "tools", "pi", false},
		{"cursor flag", []string{"--cursor", "tools"}, "tools", "cursor", false},
		{"agent only, no project", []string{"--claude"}, "", "claude", false},
		{"unknown flag", []string{"--bogus", "tools"}, "", "", true},
		{"two projects", []string{"a", "b"}, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project, agent, err := parseProjectArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got project=%q agent=%q", project, agent)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if project != tc.wantProject {
				t.Errorf("project: got %q, want %q", project, tc.wantProject)
			}
			if agent != tc.wantAgent {
				t.Errorf("agent: got %q, want %q", agent, tc.wantAgent)
			}
		})
	}
}
