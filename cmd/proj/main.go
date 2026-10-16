package main

import (
	"os"

	"github.com/subaud-tools/tackle/internal/projcli"
)

func main() { os.Exit(projcli.Dispatch(os.Args[1:], os.Stdout, os.Stderr)) }
