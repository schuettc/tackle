package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/profile"
	tools "github.com/schuettc/tools-common"
)

// isTerminal is a test seam: whether stdin is a terminal to ask on.
var isTerminal = func(r any) bool {
	f, ok := r.(*os.File)
	return ok && (isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd()))
}

// roots is a repeatable --root flag.
type roots []string

func (r *roots) String() string     { return strings.Join(*r, ",") }
func (r *roots) Set(v string) error { *r = append(*r, v); return nil }

var initFlags = flags("init", "sift init [--root DIR]… [--yes] [--force]",
	"Detects which agent harnesses are installed (Claude Code, Codex, pi: by their home\n"+
		"directories) and writes sift's config with their profiles on, the roots to audit and the\n"+
		"default budgets and windows. Lists the repos under the roots that have an origin/dev, for you\n"+
		"to give a [[repo]] base where pull requests go to dev; it never sets one itself. Roots are directories holding repos at any depth, or one repo;\n"+
		"without --root, the repo you are in (else the current directory). The profiles' global files\n"+
		"and skill directories are always audited. Asks before writing unless --yes; keeps an\n"+
		"existing config unless --force.",
	func(fs *flag.FlagSet) {
		fs.Var(new(roots), "root", "a directory to audit (repeatable)")
		tools.YesFlag(fs, "write the config without asking")
		fs.Bool("force", false, "replace an existing config")
	})

func runInit(stdin io.Reader) func(args []string, out, errw io.Writer) error {
	return func(args []string, out, errw io.Writer) error {
		fs := initFlags()
		pos, err := parse(fs, args, out)
		if err != nil {
			return err
		}
		if len(pos) > 0 {
			return tools.UsageError{Msg: "init takes no arguments (use --root DIR)"}
		}
		path := config.Path()
		if _, err := os.Stat(path); err == nil && !boolFlag(fs, "force") {
			_, _ = fmt.Fprintf(out, "config: already at %s (sift init --force replaces it)\n", path)
			return nil
		}

		c := config.Default()
		for _, p := range profile.Builtins() {
			if st, err := os.Stat(p.HomeDir()); err == nil && st.IsDir() {
				c.Profiles = append(c.Profiles, p.Name)
				_, _ = fmt.Fprintf(out, "%-12s found (%s)\n", p.Label+":", p.HomeDir())
			} else {
				_, _ = fmt.Fprintf(out, "%-12s not found\n", p.Label+":")
			}
		}
		given := *fs.Lookup("root").Value.(*roots)
		if len(given) == 0 {
			given = []string{proposeRoot()}
		}
		for _, r := range given {
			abs, err := filepath.Abs(profile.Expand(r))
			if err != nil {
				return tools.Exitf(2, "root %s: %v", r, err)
			}
			c.Roots = append(c.Roots, config.Root{Path: abs})
			_, _ = fmt.Fprintf(out, "root:        %s\n", abs)
		}
		if len(c.Profiles) == 0 {
			_, _ = fmt.Fprintln(out, "no harness found: only the roots will be audited")
		}
		_, _ = fmt.Fprintf(out, "budgets:     global %d, repo %d, skill %d bytes\n", c.Budgets.Global, c.Budgets.Repo, c.Budgets.Skill)
		if dev := devRepos(c.Roots); len(dev) > 0 {
			_, _ = fmt.Fprintln(out, "note:        these repos have an origin/dev. sift reads each repo at its default branch and opens\n"+
				"             its pull requests there; for one whose pull requests go to dev, add to the config:\n"+
				"               [[repo]]\n               path = \"<the repo>\"\n               base = \"dev\"")
			for _, d := range dev {
				_, _ = fmt.Fprintf(out, "             %s\n", d)
			}
		}

		if !boolFlag(fs, "yes") {
			if !isTerminal(stdin) {
				return tools.Exitf(2, "sift init needs --yes, or a terminal to ask on")
			}
			_, _ = fmt.Fprintf(out, "Write %s? [Y/n] ", path)
			line, _ := bufio.NewReader(stdin).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(line)); a != "" && a != "y" && a != "yes" {
				_, _ = fmt.Fprintln(out, "config: not written")
				return nil
			}
		}
		// The pattern lists stay unset so the shipped defaults, and any
		// improvement to them, apply.
		c.Stale = config.Stale{}
		if err := config.Save(path, c); err != nil {
			return tools.Exitf(2, "%v", err)
		}
		_, _ = fmt.Fprintf(out, "config: written to %s\n", path)
		return nil
	}
}

// devRepos lists the repos under roots with an origin/dev branch. It is a
// note for the user only: a dev branch doesn't say where a repo's pull
// requests go, so init never sets a base from it.
func devRepos(roots []config.Root) []string {
	ctx := context.Background()
	var out []string
	seen := map[string]bool{}
	for _, r := range roots {
		_ = filepath.WalkDir(r.Path, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil //nolint:nilerr // an unreadable directory is skipped
			}
			switch d.Name() {
			case ".git", "node_modules", "vendor", "testdata", ".worktrees":
				return filepath.SkipDir
			}
			if !gitDir(p) || seen[p] {
				return nil
			}
			seen[p] = true
			if discover.Git(ctx, p, "rev-parse", "--verify", "-q", "refs/remotes/origin/dev").Run() == nil {
				out = append(out, p)
			}
			return nil
		})
	}
	return out
}

// gitDir reports whether dir holds a .git directory (a clone, not a linked
// worktree).
func gitDir(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && st.IsDir()
}

// proposeRoot is the git toplevel of the current directory, else the
// directory itself.
func proposeRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	b, err := discover.Git(context.Background(), wd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return wd
	}
	if top := strings.TrimSpace(string(b)); top != "" {
		return top
	}
	return wd
}

// loadConfig loads the config, mapping a missing one to an error that says
// what to run.
func loadConfig() (config.Config, error) {
	c, err := config.Load(config.Path())
	if errors.Is(err, config.ErrMissing) {
		return c, tools.Exitf(2, "no sift config at %s", config.Path()).WithHint("sift init")
	}
	if err != nil {
		return c, tools.Exitf(2, "%v", err)
	}
	return c, nil
}
