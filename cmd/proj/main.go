package main

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/schuettc/tackle/internal/proj"
	"github.com/schuettc/tackle/internal/projtui"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		return runPicker()
	}
	switch args[0] {
	case "list", "current", "new":
		fmt.Fprintf(os.Stderr, "proj %s: not implemented yet\n", args[0])
		return 1
	default:
		fmt.Fprintf(os.Stderr, "proj: unknown command %q\n", args[0])
		return 2
	}
}

// runPicker builds the Bubble Tea model, runs it, and executes the user's
// Result: "new" → EnsureSession then Goto; "jump" → Goto.
func runPicker() int {
	m, err := projtui.New()
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
