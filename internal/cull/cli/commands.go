package cli

import (
	"io"

	tools "github.com/schuettc/tools-common"
)

func commands(stdin io.Reader) []tools.Command {
	return []tools.Command{
		{
			Name: "check", Group: "check", Synopsis: "[path] [--diff base]",
			Summary:  "judge tests and near-duplicate groups (suite or diff), write .cull/last.json",
			NewFlags: checkFlags, Run: runCheck(stdin),
		},
		{
			Name: "apply", Group: "check", Synopsis: "--ids <id>… | --verdict cut",
			Summary:  "remove tests judged cut, tidy imports, verify; roll back on failure",
			NewFlags: applyFlags, Run: runApply,
		},
		{
			Name: "judge", Group: "judge", Synopsis: "[file|-]",
			Summary:  "judge tests or duplicate groups with Jev (plumbing; JSONL in, JSONL out)",
			NewFlags: judgeFlags, Run: runJudge(stdin),
		},
	}
}
