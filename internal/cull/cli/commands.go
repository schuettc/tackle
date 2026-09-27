package cli

import (
	"io"

	tools "github.com/schuettc/tools-common"
)

func commands(stdin io.Reader) []tools.Command {
	return []tools.Command{
		{
			Name: "judge", Group: "judge", Synopsis: "[file|-]",
			Summary:  "judge tests or duplicate groups with Jev (plumbing; JSONL in, JSONL out)",
			NewFlags: judgeFlags, Run: runJudge(stdin),
		},
	}
}
