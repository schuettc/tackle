package main

import (
	"os"

	"github.com/schuettc/tackle/internal/projcli"
)

func main() { os.Exit(projcli.Dispatch(os.Args[1:], os.Stdout, os.Stderr)) }
