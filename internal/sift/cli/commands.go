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
			Summary:  "check what sift needs: config, profiles, roots, state; gh, the page server and channels are optional",
			NewFlags: doctorFlags, Run: runDoctor,
		},
		{
			Name: "check", Group: "audit", Synopsis: "[--json]",
			Summary:  "audit the instruction files and record the round's findings as rows",
			NewFlags: checkFlags, Run: runCheck,
		},
		{
			Name: "rows", Group: "audit", Synopsis: "add [--round N] < rows.jsonl", Subcommands: []string{"add"},
			Summary:  "merge the agent's proposals and intake rows (JSON lines on stdin) into the round",
			NewFlags: rowsFlags, Run: runRows(stdin),
		},
		{
			Name: "serve", Group: "review", Synopsis: "[--no-open] [--port N] [--stop]",
			Summary:  "open the review page (starts the local server if needed)",
			NewFlags: serveFlags, Run: runServe,
		},
		{
			Name: "wait", Group: "review", Synopsis: "[--timeout D]",
			Summary:  "wait until the user presses Send, then print what to do (for sessions without the sift channel)",
			NewFlags: waitFlags, Run: runWait,
		},
		{
			Name: "channel", Group: "review", Synopsis: "",
			Summary:  "MCP server for an agent session: check, review, apply, status, and the user's decisions as events",
			NewFlags: channelFlags, Run: runChannel(stdin),
		},
		{
			Name: "apply", Group: "review", Synopsis: "[--round N] [--dry-run] [--json]",
			Summary:  "write the certain fixes and approved rows as a branch per repo; a pull request with gh",
			NewFlags: applyFlags, Run: runApply,
		},
		{
			Name: "reconcile", Group: "review", Synopsis: "[--round N] [--json]",
			Summary:  "check each apply branch against its approved rows: missing, narrowed, extra hunks",
			NewFlags: reconcileFlags, Run: runReconcile,
		},
	}
}
