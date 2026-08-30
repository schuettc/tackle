package main

import (
	"fmt"
	"os"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "proj: TUI not wired yet (Phase 1 Task 8)")
		return 0
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
