package apply

import (
	"testing"
)

// TestShellSplitRestoreCommand verifies that ShellSplit (the exported shared
// parser) correctly handles a restore command with a quoted path containing a
// space and a single-quoted branch name, exactly as RestoreFor produces.
func TestShellSplitRestoreCommand(t *testing.T) {
	// A typical restore command produced by RestoreFor for branch-delete-local:
	// git -C '/home/court/my repo' branch 'feat/thing' deadbeef1234
	cmd := "git -C '/home/court/my repo' branch 'feat/thing' deadbeef1234"
	words, err := ShellSplit(cmd)
	if err != nil {
		t.Fatalf("ShellSplit(%q): %v", cmd, err)
	}
	want := []string{"git", "-C", "/home/court/my repo", "branch", "feat/thing", "deadbeef1234"}
	if len(words) != len(want) {
		t.Fatalf("len(words) = %d; want %d; got %v", len(words), len(want), words)
	}
	for i, w := range want {
		if words[i] != w {
			t.Errorf("words[%d] = %q; want %q", i, words[i], w)
		}
	}
}

// TestShellSplitDoubleQuotes verifies that double-quoted tokens are handled.
func TestShellSplitDoubleQuotes(t *testing.T) {
	cmd := `gh pr close 7 -R "schuettc/hail" --comment "Closing."`
	words, err := ShellSplit(cmd)
	if err != nil {
		t.Fatalf("ShellSplit: %v", err)
	}
	want := []string{"gh", "pr", "close", "7", "-R", "schuettc/hail", "--comment", "Closing."}
	if len(words) != len(want) {
		t.Fatalf("got %v want %v", words, want)
	}
	for i := range want {
		if words[i] != want[i] {
			t.Errorf("[%d] got %q want %q", i, words[i], want[i])
		}
	}
}

// TestShellSplitUnterminatedSingleQuote verifies that an unterminated single
// quote returns an error.
func TestShellSplitUnterminatedSingleQuote(t *testing.T) {
	_, err := ShellSplit("git -C '/home/court")
	if err == nil {
		t.Fatal("expected error for unterminated single quote")
	}
}

// TestShellSplitWorksAsGitCommand verifies that gitCommand (which calls
// ShellSplit) correctly parses a restore command with a quoted path.
func TestShellSplitWorksAsGitCommand(t *testing.T) {
	// RestoreFor produces commands like: git -C '/repo/path with space' branch 'feat/x' <tip>
	cmd := "git -C '/repo/my clone' branch 'feat/x' abc123"
	dir, args, err := gitCommand(cmd)
	if err != nil {
		t.Fatalf("gitCommand: %v", err)
	}
	if dir != "/repo/my clone" {
		t.Errorf("dir = %q; want /repo/my clone", dir)
	}
	if len(args) != 3 || args[0] != "branch" || args[1] != "feat/x" || args[2] != "abc123" {
		t.Errorf("args = %v; want [branch feat/x abc123]", args)
	}
}
