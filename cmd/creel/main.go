// Command creel captures a secret into a local .env without it passing through
// an agent's chat/context. Run bare (or via a tmux keybind) it prompts for the
// name, destination, and value; run as `creel NAME --dest PATH --status-file F`
// a harness drives it, and creel reports only a status token to F — never the
// value.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/schuettc/tackle/internal/creel"
	"github.com/schuettc/tackle/internal/version"
	tools "github.com/schuettc/tools-common"
)

type args struct {
	name       string
	dest       string
	statusFile string
}

func parseArgs(argv []string) (args, error) {
	var a args
	i := 0
	for i < len(argv) {
		arg := argv[i]
		switch {
		case arg == "--dest":
			i++
			if i >= len(argv) {
				return a, fmt.Errorf("--dest requires a value")
			}
			a.dest = argv[i]
		case strings.HasPrefix(arg, "--dest="):
			a.dest = strings.TrimPrefix(arg, "--dest=")
		case arg == "--status-file":
			i++
			if i >= len(argv) {
				return a, fmt.Errorf("--status-file requires a value")
			}
			a.statusFile = argv[i]
		case strings.HasPrefix(arg, "--status-file="):
			a.statusFile = strings.TrimPrefix(arg, "--status-file=")
		case strings.HasPrefix(arg, "-"):
			return a, fmt.Errorf("unknown flag: %s", arg)
		default:
			if a.name != "" {
				return a, fmt.Errorf("unexpected argument: %s", arg)
			}
			a.name = arg
		}
		i++
	}
	return a, nil
}

func run(cwd string, argv []string, stdout, stderr io.Writer) int {
	app := tools.New(tools.Config{
		Name:   "creel",
		Domain: "tackle.tools",
		Version: tools.Version{
			Number: version.Number(),
			Commit: version.Commit(),
			Date:   version.Date(),
		},
	})

	if len(argv) > 0 {
		switch argv[0] {
		case "version", "--version", "-v", "help", "--help", "-h", "update":
			return app.Dispatch(argv, stdout, stderr)
		}
	}

	a, err := parseArgs(argv)
	if err != nil {
		fmt.Fprintf(stderr, "creel: %v\n", err)
		return 2
	}
	if a.name != "" && !creel.ValidName(a.name) {
		return finish(stderr, a.statusFile, creel.Result{Err: fmt.Errorf("invalid env var name: %q", a.name)})
	}

	res := creel.RunTUI(cwd, a.name, a.dest, a.name != "", a.dest != "")
	return finish(stderr, a.statusFile, res)
}

// finish writes the harness status token (if requested) and maps the result to
// a human line + exit code. The token and the message never include the value.
func finish(stderr io.Writer, statusFile string, res creel.Result) int {
	token := string(res.Action)
	if res.Err != nil {
		token = "error:" + res.Err.Error()
	}
	_ = creel.WriteStatus(statusFile, token)

	switch {
	case res.Err != nil:
		fmt.Fprintf(stderr, "creel: %v\n", res.Err)
		return 1
	case res.Action == creel.Cancelled:
		fmt.Fprintln(stderr, "creel: cancelled")
		return 1
	default:
		fmt.Fprintf(stderr, "✓ %s %s in %s\n", res.Action, res.Name, res.Dest)
		return 0
	}
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(run(cwd, os.Args[1:], os.Stdout, os.Stderr))
}
