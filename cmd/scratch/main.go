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
	// Register scratch's commands so `help`/usage lists them alongside the
	// built-in version/help/update. They are executed via direct interception
	// below to preserve their exact output and exit codes.
	app.Register(tools.Command{Name: "path", Summary: "print the notes file path", Run: func(a []string, out, errw io.Writer) error {
		if code := run(cwd, append([]string{"path"}, a...), out); code != 0 {
			return errExit{code}
		}
		return nil
	}})
	app.Register(tools.Command{Name: "print", Summary: "print the notes file contents", Run: func(a []string, out, errw io.Writer) error {
		if code := run(cwd, append([]string{"print"}, a...), out); code != 0 {
			return errExit{code}
		}
		return nil
	}})
	app.Register(tools.Command{Name: "append", Summary: "append text to the notes file", Run: func(a []string, out, errw io.Writer) error {
		if code := run(cwd, append([]string{"append"}, a...), out); code != 0 {
			return errExit{code}
		}
		return nil
	}})

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

	switch args[0] {
	case "path":
		fmt.Fprintln(stdout, path)
		return 0
	case "print":
		content, err := notes.Read(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Fprint(stdout, content)
		return 0
	case "append":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: scratch append <text>")
			return 2
		}
		if err := notes.Append(path, args[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	// Built-ins from tools-common: version/help/update (+ aliases).
	case "version", "--version", "-v", "help", "--help", "-h", "update":
		return app.Dispatch(args, stdout, os.Stderr)
	default:
		return app.Dispatch(args, stdout, os.Stderr)
	}
}

type errExit struct{ code int }

func (e errExit) Error() string { return fmt.Sprintf("exit %d", e.code) }

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(run(cwd, os.Args[1:], os.Stdout))
}
