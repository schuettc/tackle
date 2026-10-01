package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/schuettc/tackle/internal/creel"
	"github.com/schuettc/tackle/internal/cull/check"
	"github.com/schuettc/tackle/internal/cull/key"
	tools "github.com/schuettc/tools-common"
)

// Test seams: whether stdin is a terminal, and the masked key prompt.
var (
	isTerminal = func(r any) bool {
		f, ok := r.(*os.File)
		return ok && (isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd()))
	}
	promptSecret = func() (string, error) { return creel.PromptSecret("cull · TypeSafe key") }
)

var initFlags = flags("init", "cull init [path]",
	"Court's setup, run by you in a terminal (it refuses otherwise, so an agent cannot run it):\n"+
		"stores your TypeSafe key in cull's own key file (0600) when none is stored, asks whether\n"+
		"this project's test source may be sent to TypeSafe (writes egress = true to .cull.toml on\n"+
		"yes), and adds .cull/ to .gitignore. Never prints the key.", nil)

func runInit(stdin io.Reader) func(args []string, out, errw io.Writer) error {
	return func(args []string, out, errw io.Writer) error {
		fs := initFlags()
		pos, err := parse(fs, args, out)
		if err != nil {
			return err
		}
		if len(pos) > 1 {
			return tools.UsageError{Msg: "init takes at most one path"}
		}
		path := "."
		if len(pos) == 1 {
			path = pos[0]
		}
		if !isTerminal(stdin) {
			return tools.Exitf(2, "cull init needs a terminal: run cull init yourself, in a terminal")
		}
		root, _, err := check.ResolveConfig(path, true)
		if err != nil {
			return tools.Exitf(2, "%v", err)
		}

		if _, src, err := key.Load(); err == nil {
			_, _ = fmt.Fprintf(out, "key: already set (%s)\n", src)
		} else if errors.Is(err, key.ErrMissing) {
			v, err := promptSecret()
			if err != nil {
				return tools.Exitf(2, "no key saved: %v", err)
			}
			if err := key.Save(v); err != nil {
				return tools.Exitf(2, "key not saved: %v", err)
			}
			_, _ = fmt.Fprintf(out, "key: saved to %s\n", key.Path())
		} else {
			return tools.Exitf(2, "%v", err)
		}

		if err := initEgress(root, bufio.NewReader(stdin), out); err != nil {
			return tools.Exitf(2, "%v", err)
		}
		added, err := ensureGitignore(root)
		if err != nil {
			return tools.Exitf(2, "%v", err)
		}
		if added {
			_, _ = fmt.Fprintln(out, ".gitignore: added .cull/")
		} else {
			_, _ = fmt.Fprintln(out, ".gitignore: .cull/ already ignored")
		}
		return nil
	}
}

func initEgress(root string, in *bufio.Reader, out io.Writer) error {
	p := root + string(os.PathSeparator) + ".cull.toml"
	data, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if hasEgressKey(string(data)) {
		_, _ = fmt.Fprintln(out, "egress: already decided in .cull.toml")
		return nil
	}
	_, _ = fmt.Fprint(out, "Send this project's test source to TypeSafe to judge it? [y/N] ")
	line, _ := in.ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		_, _ = fmt.Fprintln(out, "egress: not enabled (.cull.toml unchanged)")
		return nil
	}
	s := string(data)
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	if err := tools.WriteFileAtomic(p, []byte(s+"egress = true\n"), 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "egress: enabled in .cull.toml")
	return nil
}

// hasEgressKey reports whether a top-level egress key is set in the TOML.
func hasEgressKey(toml string) bool {
	for _, l := range strings.Split(toml, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "[") {
			return false
		}
		if strings.HasPrefix(l, "egress") && strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(l, "egress")), "=") {
			return true
		}
	}
	return false
}

// ensureGitignore adds .cull/ to <root>/.gitignore when no line already
// ignores it; it reports whether it wrote.
func ensureGitignore(root string) (bool, error) {
	p := root + string(os.PathSeparator) + ".gitignore"
	data, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	for _, l := range strings.Split(string(data), "\n") {
		switch strings.TrimSpace(l) {
		case ".cull", ".cull/", "/.cull", "/.cull/":
			return false, nil
		}
	}
	s := string(data)
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return true, tools.WriteFileAtomic(p, []byte(s+".cull/\n"), 0o644)
}
