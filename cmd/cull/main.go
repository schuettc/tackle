// Command cull judges whether tests are worth keeping, using TypeSafe's Jev
// classifier, and measures its rubrics against a labeled corpus.
package main

import (
	"os"

	"github.com/schuettc/tackle/internal/cull/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
