// Command docket records the fate of every repo, pull request, issue, branch
// and worktree, and shows what needs attention. See internal/docket/FORMAT.md.
package main

import (
	"os"

	"github.com/schuettc/tackle/internal/docket/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
