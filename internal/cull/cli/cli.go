// Package cli is the cull command line, built on the tools-common App.
package cli

import (
	"flag"
	"io"

	"github.com/schuettc/tackle/internal/version"
	tools "github.com/schuettc/tools-common"
)

// Main runs `cull args...` and returns the exit code.
func Main(args []string, stdin io.Reader, out, errw io.Writer) int {
	a := tools.New(tools.Config{
		Name:    "cull",
		Domain:  "tackle.tools",
		Version: tools.Version{Number: version.Number(), Commit: version.Commit(), Date: version.Date()},
		Groups: []tools.Group{
			{Key: "judge", Heading: "Judge"},
		},
	})
	for _, c := range commands() {
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

func str(fs *flag.FlagSet, name string) string { return fs.Lookup(name).Value.String() }

func boolFlag(fs *flag.FlagSet, name string) bool {
	return fs.Lookup(name).Value.(flag.Getter).Get().(bool)
}
