// Command sift keeps agent instruction files lean: it audits global, repo and
// skill files for size, duplicates, stale status, dead paths and the like,
// and records each round's findings as rows for an agent and a person to
// decide.
package main

import (
	"os"

	"github.com/schuettc/tackle/internal/sift/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
