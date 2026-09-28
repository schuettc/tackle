package apply

import (
	"fmt"
	"strings"
)

// shellQuote wraps s in POSIX single quotes, escaping any embedded single
// quotes as '"'"' so the result is always safe to embed in a shell command.
// The output is copy-pasteable into any POSIX-compatible shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// branchDeleteLocalCmd renders the exact command to delete a local branch.
func branchDeleteLocalCmd(clonePath, branch string) string {
	return fmt.Sprintf("git -C %s branch -D %s", shellQuote(clonePath), shellQuote(branch))
}

// branchDeleteRemoteCmd renders the exact command to delete a remote branch.
func branchDeleteRemoteCmd(clonePath, remote, branch string) string {
	return fmt.Sprintf("git -C %s push %s --delete %s",
		shellQuote(clonePath), shellQuote(remote), shellQuote(branch))
}

// worktreeRemoveCmd renders the exact command to remove a linked worktree.
func worktreeRemoveCmd(clonePath, worktreePath string) string {
	return fmt.Sprintf("git -C %s worktree remove %s",
		shellQuote(clonePath), shellQuote(worktreePath))
}

// repoArchiveCmd renders the exact command to archive a GitHub repository.
func repoArchiveCmd(repo string) string {
	return fmt.Sprintf("gh repo archive %s --yes", shellQuote(repo))
}

// repoDeleteCmd renders the exact command to delete a GitHub repository.
func repoDeleteCmd(repo string) string {
	return fmt.Sprintf("gh repo delete %s --yes", shellQuote(repo))
}

// prCloseCmd renders the exact command to close a pull request with a comment.
func prCloseCmd(n int, repo, comment string) string {
	return fmt.Sprintf("gh pr close %d -R %s --comment %s",
		n, shellQuote(repo), shellQuote(comment))
}

// prMergeCmd renders the exact command to merge a pull request.
func prMergeCmd(n int, repo string) string {
	return fmt.Sprintf("gh pr merge %d -R %s", n, shellQuote(repo))
}

// issueCloseCmd renders the exact command to close an issue with a comment.
func issueCloseCmd(n int, repo, comment string) string {
	return fmt.Sprintf("gh issue close %d -R %s --comment %s",
		n, shellQuote(repo), shellQuote(comment))
}
