package cli

import (
	"io"

	tools "github.com/schuettc/tools-common"
)

func commands(stdin io.Reader) []tools.Command {
	return []tools.Command{
		{
			Name: "init", Group: "setup", Synopsis: "[--root DIR]… [--yes] [--force]",
			Summary:  "detect the agent harnesses you use and write sift's config (profiles, roots, budgets)",
			NewFlags: initFlags, Run: runInit(stdin),
		},
		{
			Name: "doctor", Group: "setup", Synopsis: "",
			Summary:  "check what sift needs: config, profiles found, roots readable, state; gh is optional",
			NewFlags: doctorFlags, Run: runDoctor,
		},
		{
			Name: "check", Group: "audit", Synopsis: "[--json]",
			Summary:  "audit the instruction files and record the round's findings as rows",
			NewFlags: checkFlags, Run: runCheck,
		},
	}
}
