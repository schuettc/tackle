package cli

import (
	"flag"
	"io"

	"github.com/subaud-tools/tackle/skills"
	tools "github.com/subaud-tools/tools-common"
)

var skillsFlags = flags("skills", "sift skills install [--agent NAME]…",
	"Installs the sift skill this binary was built with, as <skills dir>/sift/SKILL.md, for each\n"+
		"agent harness found (Claude Code, Codex, pi: by their home directories), or for each\n"+
		"--agent named (claude-code, codex, pi). The skills directory may be a symlink; nothing\n"+
		"beneath it may be. An existing SKILL.md there is replaced only when it is sift's own\n"+
		"(name: sift). Run it again after each update to keep the skill in step with the binary.",
	func(fs *flag.FlagSet) {
		fs.Var(new(roots), "agent", "a harness to install for (repeatable): claude-code, codex or pi")
	})

func runSkills(args []string, out, errw io.Writer) error {
	fs := skillsFlags()
	pos, err := parse(fs, args, out)
	if err != nil {
		return err
	}
	if len(pos) != 1 || pos[0] != "install" {
		return tools.UsageError{Msg: "expected: sift skills install [--agent NAME]…"}
	}
	return skills.Install("sift", *fs.Lookup("agent").Value.(*roots), out, errw)
}
