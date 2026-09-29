package apply

import (
	"fmt"

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
