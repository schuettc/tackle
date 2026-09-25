package item

import "testing"

func TestKeyRoundTrip(t *testing.T) {
	cases := []string{
		"repo:schuettc/hail",
		"pr:elidickinson/pi-claude-bridge#97",
		"issue:schuettc/muster#112",
		"branch:schuettc/hail@feat/client",
		"branch:schuettc/hail@we@ird",
		"worktree:mbp:/Users/c/GitHub/worktrees/x y",
	}
	for _, s := range cases {
		k, err := ParseKey(s)
		if err != nil {
			t.Fatalf("ParseKey(%q): %v", s, err)
		}
		if k.String() != s {
			t.Errorf("String() = %q, want %q", k.String(), s)
		}
		back, err := KeyFromFile(k.File())
		if err != nil || back != k {
			t.Errorf("KeyFromFile(%q) = %+v, %v; want %+v", k.File(), back, err, k)
		}
	}
}

func TestParseKeyLowercasesRepo(t *testing.T) {
	k, err := ParseKey("pr:Sreetej510/Pi-Extensions#1")
	if err != nil || k.String() != "pr:sreetej510/pi-extensions#1" || k.Repo() != "sreetej510/pi-extensions" {
		t.Fatalf("got %q (%v)", k.String(), err)
	}
	b, _ := ParseKey("branch:Schuettc/Hail@Feat/X")
	if b.String() != "branch:schuettc/hail@Feat/X" {
		t.Fatalf("branch case: %q", b.String())
	}
}

func TestParseKeyRejects(t *testing.T) {
	for _, s := range []string{"", "repo", "repo:", "repo:hail", "repo:a/b/c", "pr:a/b", "pr:a/b#0",
		"pr:a/b#x", "branch:a/b", "branch:a/b@", "worktree:mbp:relative", "worktree::/x", "tag:a/b"} {
		if _, err := ParseKey(s); err == nil {
			t.Errorf("ParseKey(%q) accepted", s)
		}
	}
}

func TestFilePaths(t *testing.T) {
	want := map[string]string{
		"repo:schuettc/hail":      "items/repo/schuettc/hail.toml",
		"pr:a/b#12":               "items/pr/a/b/12.toml",
		"issue:a/b#3":             "items/issue/a/b/3.toml",
		"branch:a/b@feat/x":       "items/branch/a/b/feat%2Fx.toml",
		"worktree:mbp:/Users/c/w": "items/worktree/mbp/%2FUsers%2Fc%2Fw.toml",
	}
	for s, f := range want {
		k, err := ParseKey(s)
		if err != nil {
			t.Fatal(err)
		}
		if k.File() != f {
			t.Errorf("%s: File() = %s, want %s", s, k.File(), f)
		}
	}
}

func TestKeyFromFileRejects(t *testing.T) {
	for _, f := range []string{"items/repo/a.toml", "items/pr/a/b/x.toml", "items/tag/a/b.toml",
		"notes/repo/a/b.toml", "items/repo/a/b.txt", "items/worktree/m/relative.toml"} {
		if _, err := KeyFromFile(f); err == nil {
			t.Errorf("KeyFromFile(%q) accepted", f)
		}
	}
}

func TestConstructors(t *testing.T) {
	if RepoKey("Schuettc/Hail").String() != "repo:schuettc/hail" ||
		PRKey("A/B", 7).String() != "pr:a/b#7" ||
		IssueKey("A/B", 8).String() != "issue:a/b#8" ||
		BranchKey("A/B", "Main").String() != "branch:a/b@Main" ||
		WorktreeKey("mbp", "/x").String() != "worktree:mbp:/x" {
		t.Fatal("constructor mismatch")
	}
}
