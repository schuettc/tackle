package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/schuettc/tackle/internal/proj"
	"github.com/schuettc/tackle/internal/projtui"
	"github.com/schuettc/tackle/internal/version"
	"github.com/schuettc/tools-common"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	app := tools.New(tools.Config{
		Name:   "proj",
		Domain: "tackle.tools",
		Version: tools.Version{
			Number: version.Number(),
			Commit: version.Commit(),
			Date:   version.Date(),
		},
	})
	// Register the real subcommands. Dispatch is now their execution path (not
	// just the help listing): each carries NewFlags so -h/help render richly and
	// ExitError/UsageError from Run map to the right exit code.
	app.Register(tools.Command{
		Name:     "list",
		Summary:  "list projects and live sessions (--json)",
		Synopsis: "list --json",
		Help:     "Lists configured project names and every live session across proj's per-project tmux servers.",
		NewFlags: func() *flag.FlagSet { fs, _ := newListFlags(); return fs },
		Run:      cmdList,
	})
	app.Register(tools.Command{
		Name:     "current",
		Summary:  "identify the ambient proj session (--json)",
		Synopsis: "current --json",
		Help:     "Identifies the ambient proj session from $TMUX. All fields are empty when run outside a proj session.",
		NewFlags: func() *flag.FlagSet { fs, _ := newCurrentFlags(); return fs },
		Run:      cmdCurrent,
	})
	app.Register(tools.Command{
		Name:     "new",
		Summary:  "create/open a project session",
		Synopsis: "new <project>/<work> [--agent X] [--no-sidebar]",
		Help:     "Creates or resumes the tmux session for <project>/<work>. Detached by contract: mints the session but never switches the caller's client.",
		NewFlags: func() *flag.FlagSet { fs, _ := newNewFlags(); return fs },
		Run:      cmdNew,
	})
	app.Register(tools.Command{
		Name:     "sidebar",
		Summary:  "open the project sidebar",
		Synopsis: "sidebar <session> [--socket S] [--dir D]",
		Help:     "Opens the sidebar panes for an existing session. Socket resolves from --socket, then the ambient server, then a server search by session name.",
		NewFlags: func() *flag.FlagSet { fs, _ := newSidebarFlags(); return fs },
		Run:      cmdSidebar,
	})

	// PRESERVE the special routes BEFORE delegating to Dispatch.
	// No args → the Bubble Tea picker.
	if len(args) == 0 {
		return runPicker("", "")
	}
	switch args[0] {
	case "__autojoin-project":
		return cmdAutojoinProject()
	// list/current/new/sidebar now flow through Dispatch (which parses their
	// flags, calls Run, and maps UsageError/ExitError to the right exit code)
	// instead of the bare-project fallback below — a name match here MUST
	// come before that fallback or "proj list" would be read as project "list".
	// Built-ins from tools-common: version/help/update (+ aliases).
	case "list", "current", "new", "sidebar",
		"version", "--version", "-v", "help", "--help", "-h", "update", "man", "commands":
		return app.Dispatch(args, os.Stdout, os.Stderr)
	}
	// `proj [--claude|--pi|--cursor] <project>` — open that project's view
	// with the agent preselected (the no-subcommand flag-route).
	if args[0] == "--claude" || args[0] == "--pi" || args[0] == "--cursor" ||
		!strings.HasPrefix(args[0], "-") {
		project, agent, err := parseProjectArgs(args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "proj: %v\n", err)
			return 2
		}
		return runPicker(project, agent)
	}
	// Unknown flag/command → let Dispatch report it (exit 2).
	return app.Dispatch(args, os.Stdout, os.Stderr)
}

// parseProjectArgs parses `[--claude|--pi|--cursor] <project>`, returning the
// project name and (optional) preselected agent. At most one project positional
// is allowed; unknown flags or extra positionals are errors.
func parseProjectArgs(args []string) (project, agent string, err error) {
	for _, a := range args {
		switch a {
		case "--claude":
			agent = "claude"
		case "--pi":
			agent = "pi"
		case "--cursor":
			agent = "cursor"
		default:
			if strings.HasPrefix(a, "-") {
				return "", "", fmt.Errorf("unknown flag %q", a)
			}
			if project != "" {
				return "", "", fmt.Errorf("unexpected argument %q", a)
			}
			project = a
		}
	}
	return project, agent, nil
}

// cmdAutojoinProject is the hidden `proj __autojoin-project` helper used by the
// shell auto-join hook: it prints the name of the project containing $PWD and
// exits 0, or prints nothing and exits 1 when $PWD is outside every root.
func cmdAutojoinProject() int {
	roots, err := proj.LoadRoots()
	if err != nil {
		return 1
	}
	wd := os.Getenv("PWD")
	if wd == "" {
		wd, err = os.Getwd()
		if err != nil {
			return 1
		}
	}
	name, ok := roots.NameForDir(wd)
	if !ok {
		return 1
	}
	fmt.Println(name)
	return 0
}

// runPicker builds the Bubble Tea model, runs it, and executes the user's
// Result: "new" → EnsureSession then Goto; "jump" → Goto.
func runPicker(project, agent string) int {
	m, err := projtui.NewFor(project, agent)
	if err != nil {
		if errors.Is(err, proj.ErrNoRoots) {
			printNoRootsGuidance()
			return 1
		}
		fmt.Fprintf(os.Stderr, "proj: %v\n", err)
		return 1
	}

	final, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "proj: %v\n", err)
		return 1
	}
	res := final.(projtui.Model).Result

	switch res.Kind {
	case "new":
		dir, ok := projectDir(res.Project)
		if !ok {
			fmt.Fprintf(os.Stderr, "proj: unknown project %q\n", res.Project)
			return 1
		}
		if err := proj.EnsureSession(res.Socket, res.Name, dir, res.Agent); err != nil {
			fmt.Fprintf(os.Stderr, "proj: %v\n", err)
			return 1
		}
		if res.Sidebar {
			SpawnSidebarDetached(res.Socket, res.Name, dir)
		}
		if err := proj.Goto(res.Socket, res.Name); err != nil {
			fmt.Fprintf(os.Stderr, "proj: %v\n", err)
			return 1
		}
	case "jump":
		if err := proj.Goto(res.Socket, res.Name); err != nil {
			fmt.Fprintf(os.Stderr, "proj: %v\n", err)
			return 1
		}
	}
	return 0
}

// projectDir resolves a project name to its directory via the configured roots.
func projectDir(project string) (string, bool) {
	roots, err := proj.LoadRoots()
	if err != nil {
		return "", false
	}
	return roots.DirForName(project)
}

func printNoRootsGuidance() {
	fmt.Fprintln(os.Stderr, "proj: no project roots configured.")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Create ~/.config/proj/roots with one entry per line, e.g.:")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  ~/GitHub")
	fmt.Fprintln(os.Stderr, "  project:~/dotfiles")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "A bare path's children are projects; a 'project:' line is itself a project.")
}
