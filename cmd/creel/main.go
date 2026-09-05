package main

import (
	"os"

	"github.com/schuettc/tackle/internal/creel"
)

func main() { os.Exit(creel.Dispatch(os.Args[1:], os.Stdout, os.Stderr)) }
