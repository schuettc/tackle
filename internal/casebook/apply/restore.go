package apply

import (
	"fmt"
	"strings"

	"github.com/schuettc/tackle/internal/casebook/store"
)

// RestoreFor builds the restore record for a destructive casebook-lane step,
// using the live values captured by Check (chk). Each restore is a single
// deterministic command that reverses the step:
//
//   - branch-delete-local:  git -C <clone> branch <b> <tip>
//   - branch-delete-remote: git -C <clone> push <remote> <tip>:refs/heads/<b>
//   - worktree-remove:      git -C <clone> worktree add <path> <branch|tip>
//
// The second return is false for steps that have no restore.
func RestoreFor(step JobStep, chk Checked) (store.RestoreRecord, bool) {
	switch step.Action {
	case "branch-delete-local":
		dir, branch, err := parseLocalDelete(step.Command)
		if err != nil {
			return store.RestoreRecord{}, false
		}
		return store.RestoreRecord{
			Key:            step.Key,
			Action:         step.Action,
			Before:         chk.Tip,
			RestoreCommand: fmt.Sprintf("git -C %s branch %s %s", shellQuote(dir), shellQuote(branch), chk.Tip),
		}, true
	case "branch-delete-remote":
		dir, remote, branch, err := parseRemoteDelete(step.Command)
		if err != nil {
			return store.RestoreRecord{}, false
		}
		return store.RestoreRecord{
			Key:            step.Key,
			Action:         step.Action,
			Before:         chk.Tip,
			RestoreCommand: fmt.Sprintf("git -C %s push %s %s:refs/heads/%s", shellQuote(dir), shellQuote(remote), chk.Tip, branch),
		}, true
	case "worktree-remove":
		dir, path, err := parseWorktreeRemove(step.Command)
		if err != nil {
			return store.RestoreRecord{}, false
		}
		ref := chk.Branch
		if ref == "" {
			ref = chk.Tip
		}
		return store.RestoreRecord{
			Key:            step.Key,
			Action:         step.Action,
			Before:         chk.Tip,
			RestoreCommand: fmt.Sprintf("git -C %s worktree add %s %s", shellQuote(dir), shellQuote(path), shellQuote(ref)),
		}, true
	}
	return store.RestoreRecord{}, false
}

// AutoUndoable reports whether a restore command is one of the automatic,
// deterministic kinds that can be undone without human judgment (§5.5):
//   - branch recreate: "git -C <dir> branch <b> <tip>"
//   - remote branch recreate: "git -C <dir> push <remote> <tip>:refs/heads/<b>"
//   - worktree restore: "git -C <dir> worktree add <path> <ref>"
//   - gh repo unarchive
//   - gh pr reopen
//   - gh issue reopen
//
// A posted comment is never undoable. An empty restore is never undoable.
func AutoUndoable(restore string) bool {
	if restore == "" {
		return false
	}
	if strings.HasPrefix(restore, "git -C ") {
		// branch recreate:   "git -C <dir> branch <b> <tip>"
		// remote recreate:   "git -C <dir> push <remote> <tip>:refs/heads/<b>"
		// worktree restore:  "git -C <dir> worktree add <path> <ref>"
		return strings.Contains(restore, " branch ") ||
			strings.Contains(restore, " push ") ||
			strings.Contains(restore, " worktree ")
	}
	for _, prefix := range []string{
		"gh repo unarchive ",
		"gh pr reopen ",
		"gh issue reopen ",
	} {
		if strings.HasPrefix(restore, prefix) {
			return true
		}
	}
	return false
}
