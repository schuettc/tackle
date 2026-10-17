// Command imprint guides a coding agent through npm package work: it logs
// in, publishes, sets trusted publishers, deprecates and confirms CI
// releases, opening each approval for the user and checking every step on
// the registry.
package main

import (
	"os"

	"github.com/subaud-tools/tackle/internal/imprint/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
