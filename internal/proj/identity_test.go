package proj

import "testing"

func TestSlugAndValid(t *testing.T) {
	if got := SlugWork("nfl  cutover run"); got != "nfl-cutover-run" {
		t.Fatalf("slug = %q", got)
	}
	if !ValidWork("nfl-4_x") || ValidWork("bad/name") || ValidWork("dot.ted") || ValidWork("") {
		t.Fatal("ValidWork wrong")
	}
}

func TestSessionNameSanitizesTmuxTargetChars(t *testing.T) {
	// tmux target grammar is session:window.pane, so a '.' or ':' in the name makes
	// it unaddressable by "=name" — has-session misses it and EnsureSession then
	// errors "duplicate session". Project names come from dir basenames (e.g.
	// ".claude"), so those two chars must not reach the session name.
	cases := []struct{ project, work, want string }{
		{".claude", "work", "-claude/work"},
		{"a.b:c", "x", "a-b-c/x"},
		{"proj", "plain-work", "proj/plain-work"},
		{"proj", "und_er", "proj/und_er"},
	}
	for _, c := range cases {
		if got := SessionName(c.project, c.work); got != c.want {
			t.Fatalf("SessionName(%q, %q) = %q, want %q", c.project, c.work, got, c.want)
		}
	}
}

func TestProjectFromSocketAndAlias(t *testing.T) {
	if ProjectFromSocket("/tmp/tmux-501/proj-tools-workspace") != "tools-workspace" {
		t.Fatal("ProjectFromSocket")
	}
	if ProjectFromSocket("/tmp/tmux-501/default") != "" {
		t.Fatal("non-proj socket must be empty")
	}
	if AliasFor("/x/proj-tw", "tackle") != "tw/tackle" {
		t.Fatal("AliasFor")
	}
	if AliasFor("/x/default", "tackle") != "" {
		t.Fatal("alias empty when no project")
	}
}
