package cli

import (
	"flag"
	"io"
	"strings"

	"github.com/subaud-tools/tackle/skills"
	tools "github.com/subaud-tools/tools-common"
)

type agents []string

func (a *agents) String() string     { return strings.Join(*a, ",") }
func (a *agents) Set(v string) error { *a = append(*a, v); return nil }

var skillsFlags = flags("skills", "imprint skills install [--agent NAME]…",
	"Installs the imprint skill this binary was built with, as <skills dir>/imprint/SKILL.md, for\n"+
		"each agent harness found (Claude Code, Codex, pi: by their home directories), or for each\n"+
		"--agent named (claude-code, codex, pi). The skills directory may be a symlink; nothing\n"+
		"beneath it may be. An existing SKILL.md there is replaced only when it is imprint's own\n"+
		"(name: imprint). Run it again after each update to keep the skill in step with the binary.",
	func(fs *flag.FlagSet) {
		fs.Var(new(agents), "agent", "a harness to install for (repeatable): claude-code, codex or pi")
	})

func runSkills(args []string, out, errw io.Writer) error {
	fs := skillsFlags()
	pos, err := parse(fs, args, out)
	if err != nil {
		return err
	}
	if len(pos) != 1 || pos[0] != "install" {
		return tools.UsageError{Msg: "expected: imprint skills install [--agent NAME]…"}
	}
	return skills.Install("imprint", *fs.Lookup("agent").Value.(*agents), out, errw)
}
