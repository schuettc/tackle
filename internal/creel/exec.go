// exec is creel's language-agnostic secret-consumption path. `creel exec
// NAME[,NAME2,...] [--dest PATH] -- <command> [args...]` reads each NAME's
// value from a cwd-confined .env and hands it to the launched command through
// the CHILD's environment ONLY — never to stdout, stderr, argv, or any error
// string. It exists because request_secret writes a key to .env mid-session
// but the harness process env is frozen at launch and must never see the
// value; exec bridges that gap the way `aws-vault exec` / `sops exec-env` do.
package creel

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// execFn resolves the command on PATH and replaces the process with it. It is
// injected so tests can capture the argv and env without replacing the test
// binary or needing the command to exist. On success syscall.Exec never
// returns (stdio, signals, tty, and the child's exit code pass through
// transparently); it returns only when lookup or exec fails.
var execFn = osExec

// osExec is the production execFn: resolve command on PATH, then exec it.
func osExec(command string, argv, env []string) error {
	bin, err := exec.LookPath(command)
	if err != nil {
		return err
	}
	return syscall.Exec(bin, argv, env)
}

// execArgs is a parsed `exec` invocation.
type execArgs struct {
	names   []string
	dest    string
	command []string
}

// parseExecArgs parses `NAME[,NAME2,...] [--dest PATH] -- <command> [args...]`.
// Names are one comma-separated positional token; everything after the first
// bare `--` is the command and is passed through untouched (so the child may
// carry its own flags, including --help/--dest). Each name is validated with
// ValidName. A missing `--`, an empty command, or no names is a usage error.
func parseExecArgs(argv []string) (execArgs, error) {
	var e execArgs
	sawNames := false
	i := 0
	for i < len(argv) {
		arg := argv[i]
		if arg == "--" {
			e.command = argv[i+1:]
			if len(e.command) == 0 {
				return execArgs{}, errors.New("no command after --")
			}
			if !sawNames {
				return execArgs{}, errors.New("missing NAME before --")
			}
			return e, nil
		}
		switch {
		case arg == "--dest":
			i++
			if i >= len(argv) {
				return execArgs{}, errors.New("--dest requires a value")
			}
			e.dest = argv[i]
		case strings.HasPrefix(arg, "--dest="):
			e.dest = strings.TrimPrefix(arg, "--dest=")
		case strings.HasPrefix(arg, "-"):
			return execArgs{}, fmt.Errorf("unknown flag: %s", arg)
		default:
			if sawNames {
				return execArgs{}, fmt.Errorf("unexpected argument: %s", arg)
			}
			names, err := parseNames(arg)
			if err != nil {
				return execArgs{}, err
			}
			e.names = names
			sawNames = true
		}
		i++
	}
	return execArgs{}, errors.New("missing -- <command>")
}

// parseNames splits a comma-separated name token and validates each entry.
func parseNames(tok string) ([]string, error) {
	parts := strings.Split(tok, ",")
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		if !ValidName(p) {
			return nil, fmt.Errorf("invalid env var name: %q", p)
		}
		names = append(names, p)
	}
	return names, nil
}

// envPair is one resolved NAME=value, kept ordered for a deterministic child
// env. The value is carried only far enough to hand to execFn.
type envPair struct {
	name  string
	value string
}

// readValues pulls each name's value from the .env at dest. It is strict: a
// name absent from the file (or a missing file) is an error naming ONLY the
// key. Values may carry hand-added surrounding single or double quotes, which
// are stripped. The value never appears in any returned error.
func readValues(dest string, names []string) ([]envPair, error) {
	b, err := os.ReadFile(dest)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	lines := strings.Split(string(b), "\n") // "" split → [""], harmless

	pairs := make([]envPair, 0, len(names))
	for _, name := range names {
		val, ok := lookupLine(lines, name)
		if !ok {
			return nil, fmt.Errorf("%s not found in %s", name, dest)
		}
		pairs = append(pairs, envPair{name: name, value: unquote(val)})
	}
	return pairs, nil
}

// lookupLine returns the value for the first `name=` line, mirroring HasKey's
// prefix match.
func lookupLine(lines []string, name string) (string, bool) {
	prefix := name + "="
	for _, ln := range lines {
		if strings.HasPrefix(ln, prefix) {
			return strings.TrimPrefix(ln, prefix), true
		}
	}
	return "", false
}

// unquote strips one layer of matching surrounding single or double quotes.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// mergeEnv overlays pairs onto base (os.Environ()-shaped), replacing any
// existing entry for an overlaid name so the child sees the .env value.
func mergeEnv(base []string, pairs []envPair) []string {
	overlaid := make(map[string]bool, len(pairs))
	for _, p := range pairs {
		overlaid[p.name] = true
	}
	out := make([]string, 0, len(base)+len(pairs))
	for _, kv := range base {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if overlaid[key] {
			continue
		}
		out = append(out, kv)
	}
	for _, p := range pairs {
		out = append(out, p.name+"="+p.value)
	}
	return out
}

// runExec is the `creel exec` entry point. Exit codes: 2 for any usage error,
// a missing var, or a dest outside cwd; 127 for a command that cannot be found
// or launched. On success the process is replaced and this never returns.
func runExec(cwd string, argv []string, stdout, stderr io.Writer) int {
	e, err := parseExecArgs(argv)
	if err != nil {
		fmt.Fprintf(stderr, "creel exec: %v\n", err)
		return 2
	}
	dest, err := ResolveDest(cwd, e.dest)
	if err != nil {
		fmt.Fprintf(stderr, "creel exec: %v\n", err)
		return 2
	}
	pairs, err := readValues(dest, e.names)
	if err != nil {
		fmt.Fprintf(stderr, "creel exec: %v\n", err)
		return 2
	}

	env := mergeEnv(os.Environ(), pairs)
	if err := execFn(e.command[0], e.command, env); err != nil {
		// The error names only the command (LookPath) or is an errno on the
		// command path (Exec) — never a value.
		fmt.Fprintf(stderr, "creel exec: %v\n", err)
		return 127
	}
	return 0 // reached only when execFn is injected (tests)
}
