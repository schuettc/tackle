package main

import (
	"fmt"
	"io"
	"os"

	"github.com/schuettc/tackle/internal/notes"
	"github.com/schuettc/tackle/internal/tui"
)

func run(cwd string, args []string, stdout io.Writer) int {
	path, err := notes.Path(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

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
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		return 2
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
