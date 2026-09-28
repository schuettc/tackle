// Package verify works out how to run a project's tests for the files
// `cull apply` touched, and runs them with a per-command timeout.
package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Command is one test invocation Plan produced: Argv[0] plus its arguments,
// run with cwd Dir. Shell holds the raw test_command string, and is set
// only when the command came from test_command (run via `sh -c`); it is
// empty for the language-derived commands.
type Command struct {
	Dir   string
	Argv  []string
	Shell string
}

// Result is the outcome of running one Command.
type Result struct {
	Command    string
	ExitCode   int
	OK         bool
	TimedOut   bool
	OutputTail string
}

// pythonProjectMarkers are the files that mark a directory as a Python
// project root for test discovery.
var pythonProjectMarkers = []string{"pyproject.toml", "setup.cfg", "pytest.ini", "tox.ini", "setup.py"}

// Plan works out the commands needed to run tests covering files (a
// project-root-relative, '/'-separated relpath -> language map). If
// testCommand is set, it wins outright: a single `sh -c testCommand` in
// root. Otherwise Plan groups files by language and, within a language, by
// the project directory that owns their tests (nearest go.mod for Go,
// nearest directory with a Python project marker, nearest package.json with
// a scripts.test for TypeScript), and emits one deterministic command per
// group. A file whose language has no way to run its tests is an error.
func Plan(root, testCommand string, files map[string]string) ([]Command, error) {
	if testCommand != "" {
		return []Command{{Dir: root, Argv: []string{"sh", "-c", testCommand}, Shell: testCommand}}, nil
	}

	relpaths := make([]string, 0, len(files))
	for rel := range files {
		relpaths = append(relpaths, rel)
	}
	sort.Strings(relpaths)

	goPkgs := map[string]map[string]bool{}
	pyFiles := map[string]map[string]bool{}
	tsDirs := map[string]bool{}

	for _, rel := range relpaths {
		lang := files[rel]
		dirRel := path.Dir(rel)
		switch lang {
		case "go":
			modDir, ok := nearestMarkerDirStrict(root, dirRel, hasGoMod)
			if !ok {
				return nil, cannotRunErr(rel)
			}
			if goPkgs[modDir] == nil {
				goPkgs[modDir] = map[string]bool{}
			}
			goPkgs[modDir][goPackageSpec(modDir, dirRel)] = true
		case "python":
			projDir := nearestMarkerDir(root, dirRel, hasPythonMarker)
			if pyFiles[projDir] == nil {
				pyFiles[projDir] = map[string]bool{}
			}
			pyFiles[projDir][relTo(projDir, rel)] = true
		case "typescript":
			pkgDir, ok := nearestTSTestDir(root, dirRel)
			if !ok {
				return nil, cannotRunErr(rel)
			}
			tsDirs[pkgDir] = true
		default:
			return nil, cannotRunErr(rel)
		}
	}

	var cmds []Command

	for _, modDir := range mapKeys(goPkgs) {
		pkgs := sortedStrings(goPkgs[modDir])
		argv := append([]string{"go", "test"}, pkgs...)
		cmds = append(cmds, Command{Dir: osPath(root, modDir), Argv: argv})
	}

	for _, projDir := range mapKeys(pyFiles) {
		absDir := osPath(root, projDir)
		interp := pythonInterpreter(absDir)
		fileArgs := sortedStrings(pyFiles[projDir])
		argv := append([]string{interp, "-m", "pytest", "-q"}, fileArgs...)
		cmds = append(cmds, Command{Dir: absDir, Argv: argv})
	}

	for _, pkgDir := range sortedStrings(tsDirs) {
		cmds = append(cmds, Command{Dir: osPath(root, pkgDir), Argv: []string{"npm", "test"}})
	}

	return cmds, nil
}

func cannotRunErr(rel string) error {
	return fmt.Errorf("cannot determine how to run %s's tests; set test_command in .cull.toml or pass --no-verify", rel)
}

// osPath converts a "."-rooted, '/'-separated relpath (root itself is ".")
// to an absolute, OS-native path under root.
func osPath(root, rel string) string {
	if rel == "." {
		return root
	}
	return filepath.Join(root, filepath.FromSlash(rel))
}

// relTo returns target (a '/'-separated relpath rooted at root) relative to
// base (also '/'-separated, rooted at root, or "." for root itself).
func relTo(base, target string) string {
	if base == target {
		return "."
	}
	if base == "." {
		return target
	}
	return strings.TrimPrefix(target, base+"/")
}

// goPackageSpec returns the `go test` package spec for a directory dirRel
// under Go module modDir: "./" for the module root itself, "./sub" for a
// subdirectory.
func goPackageSpec(modDir, dirRel string) string {
	rel := relTo(modDir, dirRel)
	if rel == "." {
		return "./"
	}
	return "./" + rel
}

// nearestMarkerDir walks from startRel upward (bounded by root) looking for
// a directory hasMarker accepts; it falls back to "." (root) if none is
// found.
func nearestMarkerDir(root, startRel string, hasMarker func(absDir string) bool) string {
	d := startRel
	for {
		if hasMarker(osPath(root, d)) {
			return d
		}
		if d == "." {
			return "."
		}
		d = path.Dir(d)
	}
}

// nearestMarkerDirStrict walks from startRel upward (bounded by root)
// looking for a directory hasMarker accepts; unlike nearestMarkerDir it
// reports ok=false (no silent fallback to root) if none is found.
func nearestMarkerDirStrict(root, startRel string, hasMarker func(absDir string) bool) (dir string, ok bool) {
	d := startRel
	for {
		if hasMarker(osPath(root, d)) {
			return d, true
		}
		if d == "." {
			return "", false
		}
		d = path.Dir(d)
	}
}

func hasGoMod(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "go.mod"))
	return err == nil
}

func hasPythonMarker(dir string) bool {
	for _, m := range pythonProjectMarkers {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}

// nearestTSTestDir walks from startRel upward (bounded by root) looking for
// the nearest package.json with a non-empty scripts.test. ok is false if no
// such package.json is found by the time root is reached.
func nearestTSTestDir(root, startRel string) (dir string, ok bool) {
	d := startRel
	for {
		if hasTestScript(osPath(root, d)) {
			return d, true
		}
		if d == "." {
			return "", false
		}
		d = path.Dir(d)
	}
}

func hasTestScript(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		return false
	}
	return pkg.Scripts["test"] != ""
}

// pythonInterpreter picks the interpreter for a Python test run rooted at
// absDir: $VIRTUAL_ENV/bin/python, else absDir/.venv/bin/python, else
// absDir/venv/bin/python, else "python3".
func pythonInterpreter(absDir string) string {
	if v := os.Getenv("VIRTUAL_ENV"); v != "" {
		return filepath.Join(v, "bin", "python")
	}
	if p := filepath.Join(absDir, ".venv", "bin", "python"); fileExists(p) {
		return p
	}
	if p := filepath.Join(absDir, "venv", "bin", "python"); fileExists(p) {
		return p
	}
	return "python3"
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func mapKeys(m map[string]map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedStrings(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// tailLines is the outermost n lines of s.
const tailLines = 60

// syncBuffer is a bytes.Buffer safe for concurrent Write (from the
// command's stdout/stderr copy goroutines) and String (from runOne reading
// the tail after a bounded post-kill wait, which may race those
// goroutines if a detached grandchild keeps a pipe open).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// killGrace bounds how long runOne waits for cmd.Wait() to return after
// killing the process group on timeout. A detached grandchild that keeps
// holding the stdout/stderr pipe (e.g. one started with setsid) can
// otherwise block Wait() indefinitely even though the timed-out command's
// own process group is dead. Tests shorten this.
var killGrace = 5 * time.Second

// Run executes cmds in order, stopping at the first failing command
// (including a timeout). Each command gets its own timeout, and runs with
// the current environment minus TYPESAFE_API_KEY (test commands may run
// third-party build tooling that must not see it). The command's process
// runs in its own process group so `sh -c` / `go test` children are killed
// too on timeout.
func Run(ctx context.Context, cmds []Command, timeout time.Duration) []Result {
	env := filteredEnv()
	var results []Result
	for _, c := range cmds {
		r := runOne(ctx, c, timeout, env)
		results = append(results, r)
		if !r.OK {
			break
		}
	}
	return results
}

func runOne(ctx context.Context, c Command, timeout time.Duration, env []string) Result {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	label := c.Shell
	if label == "" {
		label = strings.Join(c.Argv, " ")
	}

	cmd := exec.Command(c.Argv[0], c.Argv[1:]...)
	cmd.Dir = c.Dir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	buf := &syncBuffer{}
	cmd.Stdout = buf
	cmd.Stderr = buf

	if err := cmd.Start(); err != nil {
		return Result{Command: label, ExitCode: -1, OK: false, OutputTail: err.Error()}
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-cmdCtx.Done():
		if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			_ = cmd.Process.Kill()
		}
		// cmd.Wait() can still block past the kill if a detached
		// grandchild (e.g. started via setsid) holds the stdout/stderr
		// pipe open in its own session. Wait at most killGrace for it,
		// then report the timeout anyway; the goroutine reading <-done
		// is leaked in that rare case but exits once the pipe eventually
		// closes.
		select {
		case <-done:
		case <-time.After(killGrace):
		}
		return Result{
			Command:    label,
			ExitCode:   -1,
			OK:         false,
			TimedOut:   true,
			OutputTail: tail(buf.String(), tailLines),
		}
	case err := <-done:
		exitCode := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				exitCode = ee.ExitCode()
			} else {
				exitCode = -1
			}
		}
		return Result{
			Command:    label,
			ExitCode:   exitCode,
			OK:         err == nil,
			OutputTail: tail(buf.String(), tailLines),
		}
	}
}

// tail returns the last n lines of s.
func tail(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n") + "\n"
}

// filteredEnv is the current environment minus TYPESAFE_API_KEY.
func filteredEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TYPESAFE_API_KEY=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}
