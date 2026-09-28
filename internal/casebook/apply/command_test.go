package apply

import (
	"strings"
	"testing"
)

func TestShellQuoteBasic(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "'hello'"},
		{"feat/my-branch", "'feat/my-branch'"},
		{"/Users/me/repos/myrepo", "'/Users/me/repos/myrepo'"},
		{"schuettc/myrepo", "'schuettc/myrepo'"},
	}
	for _, tt := range tests {
		got := shellQuote(tt.input)
		if got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestShellQuoteSingleQuote(t *testing.T) {
	// A branch name with a single quote must be escaped correctly.
	got := shellQuote("it's-a-branch")
	// Should produce: 'it'"'"'s-a-branch'
	want := `'it'"'"'s-a-branch'`
	if got != want {
		t.Errorf("shellQuote(\"it's-a-branch\") = %q, want %q", got, want)
	}
}

func TestShellQuoteSpaceInPath(t *testing.T) {
	// A path with a space must be quoted.
	got := shellQuote("/Users/my user/repos/myrepo")
	want := "'/Users/my user/repos/myrepo'"
	if got != want {
		t.Errorf("shellQuote with space = %q, want %q", got, want)
	}
}

func TestBranchDeleteCommandIsExact(t *testing.T) {
	// Verify the full command format for a branch name with a single quote.
	// Branch name: feat/it's-done (contains a single quote)
	cmd := branchDeleteLocalCmd("/Users/me/repos/myrepo", "feat/it's-done", "deadbeef")
	// The command must be copy-pasteable.
	if !strings.HasPrefix(cmd, "git -C ") {
		t.Errorf("command missing 'git -C': %q", cmd)
	}
	// The branch name single quote must be escaped.
	if !strings.Contains(cmd, `'"'"'`) {
		t.Errorf("single quote in branch name not properly escaped in: %q", cmd)
	}
}

func TestBranchDeleteRemoteCommandIsExact(t *testing.T) {
	cmd := branchDeleteRemoteCmd("/Users/me/repos/myrepo", "origin", "feat/my branch", "deadbeef")
	// Branch name has a space — the ref-bearing args must be quoted.
	want := "git -C '/Users/me/repos/myrepo' push '--force-with-lease=refs/heads/feat/my branch:deadbeef' 'origin' ':refs/heads/feat/my branch'"
	if cmd != want {
		t.Errorf("branchDeleteRemoteCmd =\n  %q\nwant\n  %q", cmd, want)
	}
}

func TestWorktreeRemoveCommandIsExact(t *testing.T) {
	cmd := worktreeRemoveCmd("/Users/me/repos/myrepo", "/Users/me/worktrees/feat-branch")
	want := "git -C '/Users/me/repos/myrepo' worktree remove '/Users/me/worktrees/feat-branch'"
	if cmd != want {
		t.Errorf("worktreeRemoveCmd = %q, want %q", cmd, want)
	}
}

func TestRepoArchiveCommandIsExact(t *testing.T) {
	cmd := repoArchiveCmd("schuettc/myrepo")
	want := "gh repo archive 'schuettc/myrepo' --yes"
	if cmd != want {
		t.Errorf("repoArchiveCmd = %q, want %q", cmd, want)
	}
}

func TestRepoDeleteCommandIsExact(t *testing.T) {
	cmd := repoDeleteCmd("schuettc/myrepo")
	want := "gh repo delete 'schuettc/myrepo' --yes"
	if cmd != want {
		t.Errorf("repoDeleteCmd = %q, want %q", cmd, want)
	}
}

func TestPRCloseCommandIsExact(t *testing.T) {
	cmd := prCloseCmd(42, "schuettc/myrepo", "no activity for 6 months")
	want := "gh pr close 42 -R 'schuettc/myrepo' --comment 'no activity for 6 months'"
	if cmd != want {
		t.Errorf("prCloseCmd = %q, want %q", cmd, want)
	}
}

func TestPRMergeCommandIsExact(t *testing.T) {
	cmd := prMergeCmd(7, "schuettc/myrepo")
	want := "gh pr merge 7 -R 'schuettc/myrepo'"
	if cmd != want {
		t.Errorf("prMergeCmd = %q, want %q", cmd, want)
	}
}

func TestIssueCloseCommandIsExact(t *testing.T) {
	cmd := issueCloseCmd(13, "schuettc/myrepo", "resolved upstream")
	want := "gh issue close 13 -R 'schuettc/myrepo' --comment 'resolved upstream'"
	if cmd != want {
		t.Errorf("issueCloseCmd = %q, want %q", cmd, want)
	}
}
