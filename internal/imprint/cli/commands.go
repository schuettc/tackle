package cli

import (
	"io"

	tools "github.com/subaud-tools/tools-common"
)

func commands(stdin io.Reader) []tools.Command {
	return []tools.Command{
		{
			Name: "skills", Group: "setup", Synopsis: "install [--agent NAME]…", Subcommands: []string{"install"},
			Summary:  "install the imprint skill for each agent harness, the version this binary was built with",
			NewFlags: skillsFlags, Run: runSkills,
		},
		{
			Name: "channel", Group: "agent", Synopsis: "",
			Summary:  "MCP server for an agent session: npm login, show, publish, trust, deprecate, expect and status, with progress as messages",
			NewFlags: channelFlags, Run: runChannel(stdin),
		},
	}
}
