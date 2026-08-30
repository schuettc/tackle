package main

import (
	"testing"
)

func TestSidebarArgv(t *testing.T) {
	got := sidebarArgv("/usr/local/bin/proj", "proj-myproject", "myproject-main", "/home/user/myproject")
	want := []string{
		"/usr/local/bin/proj",
		"sidebar",
		"myproject-main",
		"--socket",
		"proj-myproject",
		"--dir",
		"/home/user/myproject",
	}
	if len(got) != len(want) {
		t.Fatalf("sidebarArgv: got %v (len %d), want %v (len %d)", got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sidebarArgv[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}
