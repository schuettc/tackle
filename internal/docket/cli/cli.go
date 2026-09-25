// Package cli is the docket command line, built on the tools-common App.
package cli

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/schuettc/tackle/internal/docket/app"
	"github.com/schuettc/tackle/internal/docket/config"
	"github.com/schuettc/tackle/internal/docket/hooks"
	"github.com/schuettc/tackle/internal/docket/observe"
	"github.com/schuettc/tackle/internal/version"
	tools "github.com/schuettc/tools-common"
)

// Test seams.
var (
	newRunner  = func() observe.Runner { return observe.ExecRunner{} }
	executable = defaultExecutable
)

// defaultExecutable is this binary's resolved path, which the hook shims call.
func defaultExecutable() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// Main runs `docket args...` and returns the exit code. `docket hook` is
// handled before dispatch: it must never fail, print or wait.
func Main(args []string, stdin io.Reader, out, errw io.Writer) int {
	if len(args) > 0 && args[0] == "hook" {
		return hooks.Main(args[1:], stdin)
	}
	a := tools.New(tools.Config{
		Name:    "docket",
		Domain:  "tackle.tools",
		Version: tools.Version{Number: version.Number(), Commit: version.Commit(), Date: version.Date()},
		Groups: []tools.Group{
			{Key: "observe", Heading: "Observe"},
			{Key: "decide", Heading: "Decide"},
			{Key: "setup", Heading: "Setup"},
			{Key: "plumbing", Heading: "Plumbing (called by hooks and harnesses)"},
		},
	})
	for _, c := range commands(stdin) {
		a.Register(c)
	}
	return a.Dispatch(args, out, errw)
}

func open() (*app.App, error) {
	a, err := app.Open(newRunner())
	if errors.Is(err, config.ErrNotInitialized) {
		return nil, tools.Exitf(1, "docket is not initialized on this machine").WithHint("docket init")
	}
	return a, err
}

func hookOpts() hooks.Options {
	return hooks.Options{Dir: config.HooksDir(), StatePath: filepath.Join(tools.StateDir(config.Tool), "hooks.json"), Binary: executable()}
}

// parse accepts flags anywhere among the positionals. A value flag followed
// by a bare "-" (stdin/stdout) is joined first: tools.SplitArgs would take
// the "-" for a flag.
func parse(fs *flag.FlagSet, args []string, out io.Writer) ([]string, error) {
	var norm []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && !strings.Contains(a, "=") && i+1 < len(args) && args[i+1] == "-" {
			if f := fs.Lookup(strings.TrimLeft(a, "-")); f != nil {
				if b, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !b.IsBoolFlag() {
					norm = append(norm, a+"=-")
					i++
					continue
				}
			}
		}
		norm = append(norm, a)
	}
	fa, pos := tools.SplitArgs(fs, norm)
	if err := tools.ParseFlags(fs, fa, out); err != nil {
		return nil, err
	}
	return pos, nil
}

type multi []string

func (m *multi) String() string     { return "" }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }
