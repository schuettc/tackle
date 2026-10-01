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
			Name: "serve", Group: "check", Synopsis: "[path] [--no-open] [--port N] [--stop]",
			Summary:  "open the review page (starts the local server if needed)",
			NewFlags: serveFlags, Run: runServe,
		},
		{
			Name: "channel", Group: "check", Synopsis: "",
			Summary:  "MCP server for an agent session: check, apply, review and Court's answers as events",
			NewFlags: channelFlags, Run: runChannel(stdin),
		},
		{
			Name: "init", Group: "setup", Synopsis: "[path]",
			Summary:  "store your TypeSafe key, allow egress for the project, ignore .cull/ (terminal only)",
			NewFlags: initFlags, Run: runInit(stdin),
		},
		{
			Name: "doctor", Group: "setup", Synopsis: "[path]",
			Summary:  "check what cull needs: key, egress, python3, node, test command, serve, agent setup",
			NewFlags: doctorFlags, Run: runDoctor,
		},
		{
			Name: "judge", Group: "judge", Synopsis: "[file|-]",
			Summary:  "judge tests or duplicate groups with Jev (plumbing; JSONL in, JSONL out)",
			NewFlags: judgeFlags, Run: runJudge(stdin),
		},
	}
}
