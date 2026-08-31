package main

import (
	"errors"
	"fmt"
	"io"
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
	// Register the real subcommands so `help`/usage lists them alongside the
	// built-in version/help/update. They are executed via direct interception
	// below to preserve their exact output and exit codes (the handlers print
	// their own diagnostics and return an int).
	app.Register(tools.Command{Name: "list", Summary: "list projects and live sessions (--json)", Run: wrapInt(cmdList)})
	app.Register(tools.Command{Name: "current", Summary: "identify the ambient proj session (--json)", Run: wrapInt(cmdCurrent)})
	app.Register(tools.Command{Name: "new", Summary: "create/open a project session", Run: wrapInt(cmdNew)})
	app.Register(tools.Command{Name: "sidebar", Summary: "open the project sidebar", Run: wrapInt(cmdSidebar)})

	// PRESERVE the special routes BEFORE delegating to Dispatch.
	// No args → the Bubble Tea picker.
	if len(args) == 0 {
		return runPicker("", "")
	}
	// Real subcommands: execute directly, returning their exact exit codes.
	switch args[0] {
	case "list":
		return cmdList(args[1:])
	case "current":
		return cmdCurrent(args[1:])
	case "new":
		return cmdNew(args[1:])
	case "sidebar":
		return cmdSidebar(args[1:])
	case "__autojoin-project":
		return cmdAutojoinProject()
	// Built-ins from tools-common: version/help/update (+ aliases).
	case "version", "--version", "-v", "help", "--help", "-h", "update":
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

// wrapInt adapts an existing `func([]string) int` handler to the tools.Command
// Run shape used by the help listing. The handlers print their own diagnostics,
// so a non-zero code maps to a silent error carrying the exit code.
func wrapInt(h func([]string) int) func([]string, io.Writer, io.Writer) error {
	return func(a []string, out, errw io.Writer) error {
		if code := h(a); code != 0 {
			return errExit{code}
		}
		return nil
	}
}

type errExit struct{ code int }

func (e errExit) Error() string { return fmt.Sprintf("exit %d", e.code) }

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

	final, err := tea.NewProgram(m).Run()
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
