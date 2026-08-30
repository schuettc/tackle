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
