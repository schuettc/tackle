package main

import (
	"fmt"
	"io"
	"os"

	"github.com/schuettc/tackle/internal/notes"
	"github.com/schuettc/tackle/internal/tui"
	"github.com/schuettc/tackle/internal/version"
	"github.com/schuettc/tools-common"
)

func run(cwd string, args []string, stdout io.Writer) int {
	path, err := notes.Path(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	app := tools.New(tools.Config{
		Name:   "scratch",
		Domain: "tackle.tools",
		Version: tools.Version{
			Number: version.Number(),
			Commit: version.Commit(),
			Date:   version.Date(),
		},
	})
	// Register scratch's subcommands with Synopsis/Help so they appear fully in
	// help/man/commands --json. Route them through Dispatch for consistent -h
	// handling (Dispatch intercepts -h and renders HelpFor with Synopsis).
	app.Register(tools.Command{
		Name:     "path",
		Summary:  "print the notes file path",
		Synopsis: "path",
		Help:     "Prints the file path of the notes pad associated with the current Claude Code session.",
		Run:      cmdPath(cwd, path),
	})
	app.Register(tools.Command{
		Name:     "print",
		Summary:  "print the notes file contents",
		Synopsis: "print",
		Help:     "Prints the contents of the notes pad associated with the current Claude Code session.",
		Run:      cmdPrint(cwd, path),
	})
	app.Register(tools.Command{
		Name:     "append",
		Summary:  "append text to the notes file",
		Synopsis: "append <text>",
		Help:     "Appends text to the notes pad associated with the current Claude Code session.",
		Run:      cmdAppend(cwd, path),
	})

	// PRESERVE the TUI entry: no args → the notes TUI.
	if len(args) == 0 {
		return tui.Run(path, func() string {
			p, err := notes.Path(cwd)
			if err != nil {
				return ""
			}
			return p
		})
	}

	// Route path/print/append through Dispatch so -h renders and exit codes map
	// correctly. Built-ins from tools-common: version/help/update (+ aliases).
	switch args[0] {
	case "path", "print", "append",
		"version", "--version", "-v", "help", "--help", "-h", "update":
		return app.Dispatch(args, stdout, os.Stderr)
	default:
		return app.Dispatch(args, stdout, os.Stderr)
	}
}

// cmdPath returns the Run function for the path subcommand.
func cmdPath(cwd, initialPath string) func([]string, io.Writer, io.Writer) error {
	return func(args []string, out, errw io.Writer) error {
		path, err := notes.Path(cwd)
		if err != nil {
			return tools.Exitf(1, "%v", err)
		}
		fmt.Fprintln(out, path)
		return nil
	}
}

// cmdPrint returns the Run function for the print subcommand.
func cmdPrint(cwd, initialPath string) func([]string, io.Writer, io.Writer) error {
	return func(args []string, out, errw io.Writer) error {
		path, err := notes.Path(cwd)
		if err != nil {
			return tools.Exitf(1, "%v", err)
		}
		content, err := notes.Read(path)
		if err != nil {
			return tools.Exitf(1, "%v", err)
		}
		fmt.Fprint(out, content)
		return nil
	}
}

// cmdAppend returns the Run function for the append subcommand.
func cmdAppend(cwd, initialPath string) func([]string, io.Writer, io.Writer) error {
	return func(args []string, out, errw io.Writer) error {
		if len(args) < 1 {
			return tools.UsageError{Msg: "usage: scratch append <text>"}
		}
		path, err := notes.Path(cwd)
		if err != nil {
			return tools.Exitf(1, "%v", err)
		}
		if err := notes.Append(path, args[0]); err != nil {
			return tools.Exitf(1, "%v", err)
		}
		return nil
	}
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(run(cwd, os.Args[1:], os.Stdout))
}
