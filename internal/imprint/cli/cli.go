// Package cli is the imprint command line, built on the tools-common App.
package cli

import (
	"flag"
	"io"

	"github.com/subaud-tools/tackle/internal/version"
	tools "github.com/subaud-tools/tools-common"
)

// Main runs `imprint args...` and returns the exit code.
func Main(args []string, stdin io.Reader, out, errw io.Writer) int {
	a := tools.New(tools.Config{
		Name:    "imprint",
		Domain:  "tackle.tools",
		Version: tools.Version{Number: version.Number(), Commit: version.Commit(), Date: version.Date()},
		Groups: []tools.Group{
			{Key: "setup", Heading: "Setup"},
			{Key: "agent", Heading: "Agent"},
		},
	})
	for _, c := range commands(stdin) {
		a.Register(c)
	}
	return a.Dispatch(args, out, errw)
}

func flags(name, usage, long string, def func(fs *flag.FlagSet)) func() *flag.FlagSet {
	return func() *flag.FlagSet {
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		tools.SetUsage(fs, usage, long)
		if def != nil {
			def(fs)
		}
		return fs
	}
}

// parse accepts flags anywhere among the positionals.
func parse(fs *flag.FlagSet, args []string, out io.Writer) ([]string, error) {
	flagArgs, pos := tools.SplitArgs(fs, args)
	if err := tools.ParseFlags(fs, flagArgs, out); err != nil {
		return nil, err
	}
	return pos, nil
}
