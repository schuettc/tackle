package cli

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/schuettc/tackle/internal/sift/apply"
	"github.com/schuettc/tackle/internal/sift/profile"
	"github.com/schuettc/tackle/skills"
	tools "github.com/schuettc/tools-common"
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
	body, err := skills.FS.ReadFile("sift/SKILL.md")
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	var targets []profile.Profile
	named := *fs.Lookup("agent").Value.(*roots)
	for _, n := range named {
		p, ok := profile.Builtin(n)
		if !ok {
			return tools.UsageError{Msg: fmt.Sprintf("unknown agent %q: claude-code, codex or pi", n)}
		}
		targets = append(targets, p)
	}
	if len(named) == 0 {
		for _, p := range profile.Builtins() {
			if st, err := os.Stat(p.HomeDir()); err == nil && st.IsDir() {
				targets = append(targets, p)
			}
		}
	}
	if len(targets) == 0 {
		return tools.Exitf(2, "no agent harness found: name one with --agent")
	}
	failed := 0
	for _, p := range targets {
		dir := p.Path(p.Skills[0])
		dest, err := installSkill(dir, "sift", body)
		if err != nil {
			failed++
			_, _ = fmt.Fprintf(errw, "%-12s %v\n", p.Label+":", err)
			continue
		}
		_, _ = fmt.Fprintf(out, "%-12s %s\n", p.Label+":", dest)
	}
	if failed > 0 {
		return tools.Exitf(1, "%d harness(es) not installed", failed)
	}
	return nil
}

// installSkill writes body as dir/name/SKILL.md. dir may be a symlink (a
// dotfiles install links the whole directory) and is made when missing;
// name and SKILL.md beneath it may not be symlinks, and an existing
// SKILL.md must be the named skill's own. Every write goes through an
// os.Root on dir.
func installSkill(dir, name string, body []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(real)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	rel := filepath.Join(name, profile.SkillFile)
	dest := filepath.Join(dir, rel)
	st, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := root.Mkdir(name, 0o755); err != nil {
			return "", err
		}
	case err != nil:
		return "", err
	case st.Mode()&os.ModeSymlink != 0 || !st.IsDir():
		return "", fmt.Errorf("%s is not a plain directory: not sift's to write", filepath.Join(dir, name))
	}
	st, err = root.Lstat(rel)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return "", err
	case !st.Mode().IsRegular():
		return "", fmt.Errorf("%s is not a regular file: not sift's to replace", dest)
	default:
		old, err := root.ReadFile(rel)
		if err != nil {
			return "", err
		}
		if skillName(old) != name {
			return "", fmt.Errorf("%s is another skill (name: %q), not sift's to replace", dest, skillName(old))
		}
	}
	// A fresh temporary file (O_EXCL, random name) renamed into place:
	// nothing already beside SKILL.md is written through.
	if err := apply.WriteFile(root, filepath.ToSlash(rel), string(body)); err != nil {
		return "", err
	}
	return dest, nil
}

// skillName is the name in a skill's front matter ("" for none).
func skillName(b []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(b))
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return ""
	}
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "---" {
			return ""
		}
		if v, ok := strings.CutPrefix(l, "name:"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}
