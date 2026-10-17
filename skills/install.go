package skills

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/subaud-tools/tackle/internal/sift/apply"
	"github.com/subaud-tools/tackle/internal/sift/profile"
	tools "github.com/subaud-tools/tools-common"
)

// Install writes the named skill this binary was built with as
// <skills dir>/<name>/SKILL.md for each agent named (claude-code, codex,
// pi), or, with none named, for each harness whose home directory exists.
// It prints one line per harness to out and each failure to errw, and
// fails with exit code 2 when there is no harness and 1 when any harness
// was not installed.
func Install(name string, agents []string, out, errw io.Writer) error {
	body, err := FS.ReadFile(name + "/SKILL.md")
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	var targets []profile.Profile
	for _, n := range agents {
		p, ok := profile.Builtin(n)
		if !ok {
			return tools.UsageError{Msg: fmt.Sprintf("unknown agent %q: claude-code, codex or pi", n)}
		}
		targets = append(targets, p)
	}
	if len(agents) == 0 {
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
		dest, err := installSkill(dir, name, body)
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
		return "", fmt.Errorf("%s is not a plain directory: not %s's to write", filepath.Join(dir, name), name)
	}
	st, err = root.Lstat(rel)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return "", err
	case !st.Mode().IsRegular():
		return "", fmt.Errorf("%s is not a regular file: not %s's to replace", dest, name)
	default:
		old, err := root.ReadFile(rel)
		if err != nil {
			return "", err
		}
		if skillName(old) != name {
			return "", fmt.Errorf("%s is another skill (name: %q), not %s's to replace", dest, skillName(old), name)
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
