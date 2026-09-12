// Command creel captures a secret into a local .env without it passing through
// an agent's chat/context. Run bare (or via a tmux keybind) it prompts for the
// name, destination, and value; run as `creel NAME --dest PATH --status-file F`
// a harness drives it, and creel reports only a status token to F — never the
// value. With --event-file it also writes a value-free {name,dest,action}
// record on a successful save, so a keybind/watcher can tell an agent what was
// captured (still never the value).
package creel

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/schuettc/tackle/internal/version"
	tools "github.com/schuettc/tools-common"
)

type args struct {
	name       string
	dest       string
	statusFile string
	eventFile  string
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
		case arg == "--event-file":
			i++
			if i >= len(argv) {
				return a, fmt.Errorf("--event-file requires a value")
			}
			a.eventFile = argv[i]
		case strings.HasPrefix(arg, "--event-file="):
			a.eventFile = strings.TrimPrefix(arg, "--event-file=")
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
		case "version", "--version", "-v", "help", "--help", "-h", "update", "man", "commands":
			return app.Dispatch(argv, stdout, stderr)
		}
	}

	a, err := parseArgs(argv)
	if err != nil {
		fmt.Fprintf(stderr, "creel: %v\n", err)
		return 2
	}
	if a.name != "" && !ValidName(a.name) {
		return finish(stderr, a.statusFile, a.eventFile, Result{Err: fmt.Errorf("invalid env var name: %q", a.name)})
	}

	res := RunTUI(cwd, a.name, a.dest, a.name != "", a.dest != "")
	return finish(stderr, a.statusFile, a.eventFile, res)
}

// finish writes the harness status token (if requested) and maps the result to
// a human line + exit code. The token and the message never include the value.
func finish(stderr io.Writer, statusFile, eventFile string, res Result) int {
	token := string(res.Action)
	if res.Err != nil {
		token = "error:" + res.Err.Error()
	}
	_ = WriteStatus(statusFile, token)
	// The event carries name+dest+action (never the value) for a watcher that
	// tells the agent what was captured; only a real save writes one.
	_ = WriteEvent(eventFile, res)

	switch {
	case res.Err != nil:
		fmt.Fprintf(stderr, "creel: %v\n", res.Err)
		return 1
	case res.Action == Cancelled:
		fmt.Fprintln(stderr, "creel: cancelled")
		return 1
	default:
		fmt.Fprintf(stderr, "✓ %s %s in %s\n", res.Action, res.Name, res.Dest)
		return 0
	}
}

// Dispatch parses args and runs the creel CLI, writing top-level output to
// out/errw and returning the process exit code.
func Dispatch(args []string, out, errw io.Writer) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	return run(cwd, args, out, errw)
}
