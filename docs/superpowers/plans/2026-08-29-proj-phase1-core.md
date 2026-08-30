# proj Rewrite — Phase 1 (Core) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A runnable `cmd/proj` Go binary that discovers projects from the roots file, shows the two-view Bubble Tea picker with agent-presence rows, and creates/switches per-project tmux sessions via the reattach model — plus the `proj list/current/new` agent subcommands. It does NOT yet ship muster attention, the preview pane, git, or the sidebar (Phases 2–3), and it does NOT cut over the live zsh `proj` (Phase 4).

**Architecture:** `cmd/proj` in the tackle monorepo, sharing the module. Pure logic (roots, config, identity, discovery) in `internal/proj/`; the Bubble Tea UI in `internal/projtui/` (mirrors scratch's `internal/tui/` split). tmux is driven by shelling `tmux` with an explicit `-L <socket>`. The binary never reattaches the parent shell itself: it runs `switch-client`/`detach -E` directly where it can, and prints an `exec tmux … attach` line for the bare-shell case (Phase 4's shim evals it; in Phase 1 the print is asserted by tests and usable by hand).

**Tech Stack:** Go 1.26, Bubble Tea / Bubbles / Lipgloss (already tackle deps via scratch), BurntSushi/toml, tmux.

**Spec:** `tackle/docs/superpowers/specs/2026-08-29-proj-rewrite-design.md` — read it; this plan implements its Architecture, picker (agent-presence subset), config, identity, per-project-server, and reattach sections. Muster/preview/git/sidebar/skill sections are later phases and explicitly out of scope here.

## Global Constraints

- **Module:** `github.com/schuettc/tackle`; new code under `cmd/proj/` and `internal/proj/`, `internal/projtui/`. Reuse `internal/` patterns from scratch; follow scratch's `internal/tui` as the Bubble Tea reference.
- **Hard contracts (verbatim from spec, never violate):**
  - Per-project tmux socket is named **`proj-<project>`** (`tmux -L proj-<project>`). muster derives identity from this.
  - The session label lives in the tmux option **`@claude_task`** (env override `$MUSTER_LABEL_OPTION`).
  - Sessions are **created from `$HOME`** (`cd $HOME` before `tmux new-session`), with `-c <dir>` still setting each pane's dir — prevents server-cwd poisoning.
  - Session identity is **`<project>/<work>`**; work names match `^[A-Za-z0-9_-]+$` after slugging whitespace runs to `-`.
- **Roots file:** `~/.config/proj/roots` (or `$XDG_CONFIG_HOME/proj/roots`), unchanged line format: a bare line is a **root** (its immediate children are projects); a `project:<path>` line is a **project itself**; `~` and `$VAR` expand; blank lines and `#`-comments are skipped; trailing slash stripped; only existing dirs count.
- **Config file:** `~/.config/proj/config.toml` (optional). Absent or partial → defaults: `default_agent="pi"`... **wait, the default must match today's absence-of-config behavior:** default `default_agent` is unset → treat as `"none"` unless config says otherwise? NO — spec says an absent file behaves like today (agent-on). Use `default_agent=""` meaning "no auto-launch" ONLY when explicitly none; the built-in default when the key is missing is `"pi"`. Encode exactly: missing file/key → `default_agent="pi"`, `sidebar=true`. (Sidebar is Phase 3; parse the keys now, act on `default_agent` now.)
- **Degradation:** every tmux/git/agent probe that fails returns a zero value, never an error to the user. No panics on a missing roots file (that path triggers first-run, Phase 1 shows an empty-state message; the interactive first-run wizard is Phase 4 — here, if no roots, print the same guidance the zsh version prints and exit 1).
- **Testing:** pure logic = unit tests, no tmux. tmux orchestration = integration tests on a throwaway `proj-phase1-test` socket, skipped when `tmux` is absent (`t.Skip`). Bubble Tea = `Update()`-driven, like scratch's `model_test.go`. Run `gofmt -l . && go vet ./... && go build ./... && go test -race ./...` before every commit.

---

## File Structure

```
cmd/proj/main.go                    CLI dispatch: no args → TUI; `list|current|new` subcommands; --json
internal/proj/
  roots.go        roots_test.go     parse roots file; NameForDir, DirForName, AllProjectDirs
  config.go       config_test.go    Config struct; Load(); per-project override resolution
  identity.go     identity_test.go   SlugWork, ValidWork, SessionName, AliasFor(socket,label)
  tmux.go                            exec helpers: Run(socket,args...), Query(socket,target,fmt), env parsing
  server.go       server_test.go    SocketFor(project), Servers(), CurrentServer(), FindServer(name), HasSession
  discovery.go    discovery_test.go  LiveSessions() → []Session{Name,Socket,Dir,Agent,State}
  agentstate.go   agentstate_test.go AgentIn(pane) → (kind,state) from pane_current_command + flags
  session.go      session_test.go    EnsureSession(create-from-$HOME, -c dir, @claude_task, launch agent)
  reattach.go     reattach_test.go   Goto(socket,name) → Action{Switch|DetachExec|PrintExec}; Render()
internal/projtui/
  model.go        model_test.go      Bubble Tea model: View1 entrance / View2 project, filter, keys
  view.go                            render rows (name + agent·state) + footer; preview stub (Phase 2)
```

---

### Task 1: Scaffold `cmd/proj` + tmux exec helper

**Files:** Create `cmd/proj/main.go`, `internal/proj/tmux.go`, `internal/proj/tmux_test.go`

**Interfaces:**
- Produces: `proj.Run(socket string, args ...string) (string, error)` and `proj.Query(socket, target, format string) string` (empty on any failure). `main.go` dispatches subcommands (stubs for now) and defaults to a "TUI not wired yet" message.

- [ ] **Step 1: Write the failing test for the tmux exec helper**

`internal/proj/tmux_test.go`:
```go
package proj

import "testing"

func TestQueryEmptyOnBadSocket(t *testing.T) {
	// A socket that cannot exist yields "" (degradation), never a panic.
	if got := Query("/nonexistent/proj-nope", "=nope", "#{session_name}"); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}
```

- [ ] **Step 2: Run it to confirm it fails to compile (Query undefined)**

Run: `go test ./internal/proj/ -run TestQueryEmptyOnBadSocket`
Expected: build failure, `undefined: Query`.

- [ ] **Step 3: Implement `internal/proj/tmux.go`**

```go
package proj

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Run executes tmux against an explicit socket ("" = default server), trimming
// one trailing newline. Any error is returned to callers that care; most use
// Query, which swallows it.
func Run(socket string, args ...string) (string, error) {
	full := args
	if socket != "" {
		full = append([]string{"-L", socket}, args...)
	}
	out, err := exec.Command("tmux", full...).Output()
	return strings.TrimSuffix(string(out), "\n"), err
}

// Query runs `display-message -p -t <target> <format>` on socket and returns
// the value, or "" on any failure (dead socket, missing session, no tmux).
func Query(socket, target, format string) string {
	if socket == "" || target == "" {
		return ""
	}
	out, err := Run(socket, "display-message", "-p", "-t", target, format)
	if err != nil {
		return ""
	}
	return out
}

// SocketFromEnv returns the tmux socket path from $TMUX ("<sock>,<pid>,<idx>"),
// symlinks resolved, or "" outside tmux.
func SocketFromEnv() string {
	t := os.Getenv("TMUX")
	if t == "" {
		return ""
	}
	p := strings.SplitN(t, ",", 2)[0]
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
```

- [ ] **Step 4: Write `cmd/proj/main.go` with subcommand dispatch stubs**

```go
package main

import (
	"fmt"
	"os"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "proj: TUI not wired yet (Phase 1 Task 8)")
		return 0
	}
	switch args[0] {
	case "list", "current", "new":
		fmt.Fprintf(os.Stderr, "proj %s: not implemented yet\n", args[0])
		return 1
	default:
		fmt.Fprintf(os.Stderr, "proj: unknown command %q\n", args[0])
		return 2
	}
}
```

- [ ] **Step 5: Run tests + gates**

Run: `gofmt -l . && go vet ./... && go build ./cmd/proj && go test ./internal/proj/ -run TestQueryEmptyOnBadSocket`
Expected: clean; test PASS.

- [ ] **Step 6: Commit**
```bash
git add cmd/proj internal/proj/tmux.go internal/proj/tmux_test.go
git commit -m "feat(proj): scaffold cmd/proj + tmux exec helper"
```

---

### Task 2: Roots file parsing + project lookups

**Files:** Create `internal/proj/roots.go`, `internal/proj/roots_test.go`

**Interfaces:**
- Produces:
  - `type Roots struct { Roots []string; Projects []string }`
  - `func LoadRoots() (Roots, error)` — reads `$XDG_CONFIG_HOME/proj/roots` (fallback `~/.config/proj/roots`); error only on unreadable file; missing file → empty Roots + `ErrNoRoots`.
  - `func (Roots) NameForDir(dir string) (string, bool)` — project containing dir; declared projects checked first.
  - `func (Roots) DirForName(name string) (string, bool)`
  - `func (Roots) AllProjectDirs() []string` — children of each root + each declared project (absolute, no trailing slash).
  - `var ErrNoRoots = errors.New("no project roots configured")`

- [ ] **Step 1: Write failing tests** (use `t.TempDir()` + a fake HOME)

`internal/proj/roots_test.go`:
```go
package proj

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRoots(t *testing.T, body string) (home string) {
	t.Helper()
	home = t.TempDir()
	cfg := filepath.Join(home, ".config", "proj")
	if err := os.MkdirAll(cfg, 0o755); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(cfg, "roots"), []byte(body), 0o644); err != nil { t.Fatal(err) }
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

func TestParseRootsAndLookups(t *testing.T) {
	home := writeRoots(t, "# comment\n\n~/code\nproject:~/dotfiles\n")
	// create the dirs so they count
	mustMkdir(t, filepath.Join(home, "code", "alpha"))
	mustMkdir(t, filepath.Join(home, "code", "beta"))
	mustMkdir(t, filepath.Join(home, "dotfiles"))

	r, err := LoadRoots()
	if err != nil { t.Fatalf("LoadRoots: %v", err) }

	if got, ok := r.NameForDir(filepath.Join(home, "code", "alpha", "sub")); !ok || got != "alpha" {
		t.Fatalf("NameForDir(alpha/sub) = %q,%v", got, ok)
	}
	if got, ok := r.NameForDir(filepath.Join(home, "dotfiles")); !ok || got != "dotfiles" {
		t.Fatalf("NameForDir(dotfiles) = %q,%v", got, ok)
	}
	if got, ok := r.DirForName("beta"); !ok || got != filepath.Join(home, "code", "beta") {
		t.Fatalf("DirForName(beta) = %q,%v", got, ok)
	}
	dirs := r.AllProjectDirs()
	if !contains(dirs, filepath.Join(home, "code", "alpha")) ||
		!contains(dirs, filepath.Join(home, "dotfiles")) {
		t.Fatalf("AllProjectDirs missing entries: %v", dirs)
	}
}

func TestLoadRootsMissingFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "x"))
	if _, err := LoadRoots(); err != ErrNoRoots {
		t.Fatalf("want ErrNoRoots, got %v", err)
	}
}

func mustMkdir(t *testing.T, p string) { t.Helper(); if err := os.MkdirAll(p, 0o755); err != nil { t.Fatal(err) } }
func contains(s []string, v string) bool { for _, x := range s { if x == v { return true } }; return false }
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/proj/ -run 'TestParseRoots|TestLoadRootsMissing'`
Expected: build failure (undefined `LoadRoots` etc.).

- [ ] **Step 3: Implement `internal/proj/roots.go`**

```go
package proj

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var ErrNoRoots = errors.New("no project roots configured")

type Roots struct {
	Roots    []string // dirs whose children are projects
	Projects []string // dirs that are themselves projects
}

func rootsPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "proj", "roots")
	}
	return filepath.Join(os.Getenv("HOME"), ".config", "proj", "roots")
}

// expand applies leading ~ then $VAR expansion, strips a trailing slash.
func expand(s string) string {
	if strings.HasPrefix(s, "~") {
		s = filepath.Join(os.Getenv("HOME"), strings.TrimPrefix(s, "~"))
	}
	s = os.ExpandEnv(s)
	if s != "/" {
		s = strings.TrimRight(s, "/")
	}
	return s
}

func LoadRoots() (Roots, error) {
	f, err := os.Open(rootsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return Roots{}, ErrNoRoots
		}
		return Roots{}, err
	}
	defer f.Close()
	var r Roots
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		isProject := false
		if strings.HasPrefix(line, "project:") {
			isProject = true
			line = strings.TrimSpace(strings.TrimPrefix(line, "project:"))
		}
		p := expand(line)
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			continue
		}
		if isProject {
			r.Projects = append(r.Projects, p)
		} else {
			r.Roots = append(r.Roots, p)
		}
	}
	if len(r.Roots)+len(r.Projects) == 0 {
		return r, ErrNoRoots
	}
	return r, sc.Err()
}

func (r Roots) NameForDir(dir string) (string, bool) {
	for _, p := range r.Projects { // declared projects are the more specific claim
		if dir == p || strings.HasPrefix(dir, p+string(os.PathSeparator)) {
			return filepath.Base(p), true
		}
	}
	for _, root := range r.Roots {
		prefix := root + string(os.PathSeparator)
		if strings.HasPrefix(dir, prefix) {
			rel := strings.TrimPrefix(dir, prefix)
			return strings.SplitN(rel, string(os.PathSeparator), 2)[0], true
		}
	}
	return "", false
}

func (r Roots) DirForName(name string) (string, bool) {
	for _, p := range r.Projects {
		if filepath.Base(p) == name {
			return p, true
		}
	}
	for _, root := range r.Roots {
		cand := filepath.Join(root, name)
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return cand, true
		}
	}
	return "", false
}

func (r Roots) AllProjectDirs() []string {
	var out []string
	for _, root := range r.Roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				out = append(out, filepath.Join(root, e.Name()))
			}
		}
	}
	out = append(out, r.Projects...)
	return out
}
```

- [ ] **Step 4: Run tests + gates**

Run: `go test ./internal/proj/ -run 'TestParseRoots|TestLoadRootsMissing' && gofmt -l internal/proj && go vet ./internal/proj/`
Expected: PASS, clean.

- [ ] **Step 5: Commit**
```bash
git add internal/proj/roots.go internal/proj/roots_test.go
git commit -m "feat(proj): roots file parsing + project lookups"
```

---

### Task 3: Session identity — slug, validity, name, alias

**Files:** Create `internal/proj/identity.go`, `internal/proj/identity_test.go`

**Interfaces:**
- Produces:
  - `func SlugWork(s string) string` — collapse whitespace runs to `-`.
  - `func ValidWork(s string) bool` — `^[A-Za-z0-9_-]+$`.
  - `func SessionName(project, work string) string` — `project + "/" + work`.
  - `func ProjectFromSocket(socket string) string` — base, must start `proj-`, else "" (matches muster's derivation verbatim).
  - `func AliasFor(socket, label string) string` — `ProjectFromSocket(socket) + "/" + label`, or "" if project empty.

- [ ] **Step 1: Failing tests**

```go
package proj

import "testing"

func TestSlugAndValid(t *testing.T) {
	if got := SlugWork("nfl  cutover run"); got != "nfl-cutover-run" {
		t.Fatalf("slug = %q", got)
	}
	if !ValidWork("nfl-4_x") || ValidWork("bad/name") || ValidWork("dot.ted") || ValidWork("") {
		t.Fatal("ValidWork wrong")
	}
}

func TestProjectFromSocketAndAlias(t *testing.T) {
	if ProjectFromSocket("/tmp/tmux-501/proj-tools-workspace") != "tools-workspace" {
		t.Fatal("ProjectFromSocket")
	}
	if ProjectFromSocket("/tmp/tmux-501/default") != "" {
		t.Fatal("non-proj socket must be empty")
	}
	if AliasFor("/x/proj-tw", "tackle") != "tw/tackle" {
		t.Fatal("AliasFor")
	}
	if AliasFor("/x/default", "tackle") != "" {
		t.Fatal("alias empty when no project")
	}
}
```

- [ ] **Step 2: Run to confirm failure** — `go test ./internal/proj/ -run 'TestSlug|TestProjectFromSocket'` → undefined.

- [ ] **Step 3: Implement `internal/proj/identity.go`**

```go
package proj

import (
	"path/filepath"
	"regexp"
	"strings"
)

var workRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func SlugWork(s string) string { return strings.Join(strings.Fields(s), "-") }
func ValidWork(s string) bool  { return workRe.MatchString(s) }
func SessionName(project, work string) string { return project + "/" + work }

func ProjectFromSocket(socket string) string {
	if socket == "" {
		return ""
	}
	base := filepath.Base(socket)
	if !strings.HasPrefix(base, "proj-") {
		return ""
	}
	return strings.TrimPrefix(base, "proj-")
}

func AliasFor(socket, label string) string {
	p := ProjectFromSocket(socket)
	if p == "" {
		return ""
	}
	return p + "/" + label
}
```

- [ ] **Step 4: Run tests + gates.** Expected PASS/clean.

- [ ] **Step 5: Commit**
```bash
git add internal/proj/identity.go internal/proj/identity_test.go
git commit -m "feat(proj): session identity — slug, validity, name, alias"
```

---

### Task 4: Config (`config.toml`) with per-project overrides

**Files:** Create `internal/proj/config.go`, `internal/proj/config_test.go`

**Interfaces:**
- Produces:
  - `type Config struct { DefaultAgent string; Sidebar bool; SidebarLayout Layout; Projects map[string]ProjectOverride }`
  - `type ProjectOverride struct { DefaultAgent *string; Sidebar *bool }`
  - `func LoadConfig() Config` — missing/partial file → defaults (`DefaultAgent:"pi"`, `Sidebar:true`, layout `["scratch","yazi","shell"]`). Never errors; a malformed file logs nothing and returns defaults.
  - `func (Config) AgentFor(project string) string` — per-project override else global.
  - `func (Config) SidebarFor(project string) bool`

- [ ] **Step 1: Failing test**

```go
package proj

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "proj")
	os.MkdirAll(cfg, 0o755)
	os.WriteFile(filepath.Join(cfg, "config.toml"), []byte(body), 0o644)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}

func TestConfigDefaultsAndOverride(t *testing.T) {
	writeConfig(t, `
default_agent = "pi"
sidebar = true
[project."bettor-help"]
default_agent = "claude"
sidebar = false
`)
	c := LoadConfig()
	if c.AgentFor("anything") != "pi" { t.Fatal("global agent") }
	if c.AgentFor("bettor-help") != "claude" { t.Fatal("override agent") }
	if c.SidebarFor("anything") != true || c.SidebarFor("bettor-help") != false {
		t.Fatal("sidebar override")
	}
}

func TestConfigMissingFileDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "none"))
	c := LoadConfig()
	if c.AgentFor("x") != "pi" || c.SidebarFor("x") != true {
		t.Fatal("missing file must yield built-in defaults")
	}
}
```

- [ ] **Step 2: Run to confirm failure.**

- [ ] **Step 3: Implement `internal/proj/config.go`** (uses `github.com/BurntSushi/toml`; add to go.mod via `go get` if not present — scratch may not depend on it yet)

```go
package proj

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Layout struct {
	Panes []string          `toml:"panes"`
	Sizes map[string]int    `toml:"sizes"`
}

type ProjectOverride struct {
	DefaultAgent *string `toml:"default_agent"`
	Sidebar      *bool   `toml:"sidebar"`
}

type Config struct {
	DefaultAgent  string                     `toml:"default_agent"`
	Sidebar       bool                       `toml:"sidebar"`
	SidebarLayout Layout                     `toml:"sidebar.layout"`
	Projects      map[string]ProjectOverride `toml:"project"`
}

func configPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "proj", "config.toml")
	}
	return filepath.Join(os.Getenv("HOME"), ".config", "proj", "config.toml")
}

func defaults() Config {
	return Config{
		DefaultAgent:  "pi",
		Sidebar:       true,
		SidebarLayout: Layout{Panes: []string{"scratch", "yazi", "shell"}},
		Projects:      map[string]ProjectOverride{},
	}
}

func LoadConfig() Config {
	c := defaults()
	b, err := os.ReadFile(configPath())
	if err != nil {
		return c // missing/unreadable → defaults
	}
	// Decode into a fresh struct pre-seeded with defaults so absent keys keep them.
	seeded := defaults()
	if _, err := toml.Decode(string(b), &seeded); err != nil {
		return c // malformed → defaults, silently
	}
	if seeded.Projects == nil {
		seeded.Projects = map[string]ProjectOverride{}
	}
	if len(seeded.SidebarLayout.Panes) == 0 {
		seeded.SidebarLayout.Panes = defaults().SidebarLayout.Panes
	}
	return seeded
}

func (c Config) AgentFor(project string) string {
	if o, ok := c.Projects[project]; ok && o.DefaultAgent != nil {
		return *o.DefaultAgent
	}
	return c.DefaultAgent
}

func (c Config) SidebarFor(project string) bool {
	if o, ok := c.Projects[project]; ok && o.Sidebar != nil {
		return *o.Sidebar
	}
	return c.Sidebar
}
```

Note for the implementer: `sidebar.layout` as a dotted key in the struct tag may not decode the `[sidebar.layout]` table directly; if the test surfaces this, model it as a nested struct field `Sidebar SidebarSection` with `type SidebarSection struct{ Enabled bool; Layout Layout }` mapped to `[sidebar]` + `[sidebar.layout]`, OR keep `sidebar` bool and read `[sidebar.layout]` under a separately-named top-level `[sidebar_layout]`. Pick whichever decodes cleanly under BurntSushi and update the test's TOML to match; the behavior (defaults + override resolution) is what must hold.

- [ ] **Step 4: `go get github.com/BurntSushi/toml` if needed, run tests + gates.**

Run: `go get github.com/BurntSushi/toml 2>/dev/null; go test ./internal/proj/ -run TestConfig && gofmt -l internal/proj && go vet ./internal/proj/`
Expected: PASS/clean.

- [ ] **Step 5: Commit**
```bash
git add internal/proj/config.go internal/proj/config_test.go go.mod go.sum
git commit -m "feat(proj): config.toml with per-project overrides"
```

---

### Task 5: Per-project servers + live-session discovery + agent state

**Files:** Create `internal/proj/server.go`, `internal/proj/agentstate.go`, `internal/proj/discovery.go` and their `_test.go`. The server/discovery tests are tmux integration tests (skip if no tmux).

**Interfaces:**
- Produces:
  - `func SocketFor(project string) string` → `"proj-"+project`
  - `func Servers() []string` — socket names with ≥1 live session (scans `${TMUX_TMPDIR:-/tmp}/tmux-<uid>/proj-*`, probes `list-sessions`).
  - `func CurrentServer() string` — from `$TMUX` socket base, "" outside tmux.
  - `func FindServer(name string) (string, bool)` — which server hosts session `name`.
  - `type Session struct { Name, Socket, Dir, Agent, State string }`
  - `func LiveSessions() []Session` — across all `Servers()`, each with `Agent`/`State` from `agentstate.AgentIn`.
  - `func AgentIn(socket, session string) (kind, state string)` — kind ∈ {pi,claude,cursor,shell,""}; state ∈ {working,waiting,idle,""} from `pane_current_command` + `pane_activity`/bell/silence flags of the session's **left/main** pane.

- [ ] **Step 1: Unit test the pure classifier** (`agentstate_test.go`, no tmux — test the mapping function that takes a command string + flags)

Factor the classification into a pure helper and test it:
```go
package proj

import "testing"

func TestClassifyAgent(t *testing.T) {
	cases := []struct{ cmd string; want string }{
		{"pi", "pi"}, {"claude", "claude"}, {"cursor-agent", "cursor"},
		{"node", "claude"}, // claude often shows as node; accept via table (see impl note)
		{"zsh", "shell"}, {"yazi", "shell"}, {"", ""},
	}
	for _, c := range cases {
		if got := classifyAgent(c.cmd); got != c.want {
			t.Errorf("classifyAgent(%q)=%q want %q", c.cmd, got, c.want)
		}
	}
}
```
Implementation note: the exact `pane_current_command` for claude/pi/cursor is environment-specific; the implementer must verify the real strings on this machine (`tmux display-message -p '#{pane_current_command}'` inside a running agent) and encode the observed values. Update the table to match what's real — do not ship guessed command names.

- [ ] **Step 2: Write the tmux integration test** (`server_test.go`, skips without tmux)

```go
package proj

import (
	"os/exec"
	"testing"
)

func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
}

func TestServersAndFind(t *testing.T) {
	requireTmux(t)
	sock := "proj-phase1-test"
	// create a detached session on the test socket, from $HOME
	if _, err := Run(sock, "new-session", "-d", "-s", "proj-phase1-test/w", "-c", t.TempDir()); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	defer Run(sock, "kill-server")

	found := false
	for _, s := range Servers() {
		if s == sock { found = true }
	}
	if !found { t.Fatalf("Servers() missing %s: %v", sock, Servers()) }

	if srv, ok := FindServer("proj-phase1-test/w"); !ok || srv != sock {
		t.Fatalf("FindServer = %q,%v", srv, ok)
	}
}
```

- [ ] **Step 3: Run to confirm failure.**

- [ ] **Step 4: Implement `server.go`, `agentstate.go`, `discovery.go`.**

`server.go`:
```go
package proj

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func SocketFor(project string) string { return "proj-" + project }

func socketDir() string {
	if d := os.Getenv("TMUX_TMPDIR"); d != "" {
		return filepath.Join(d, fmt.Sprintf("tmux-%d", os.Getuid()))
	}
	return filepath.Join("/tmp", fmt.Sprintf("tmux-%d", os.Getuid()))
}

func Servers() []string {
	entries, err := os.ReadDir(socketDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "proj-") {
			continue
		}
		if _, err := Run(name, "list-sessions"); err == nil {
			out = append(out, name)
		}
	}
	return out
}

func CurrentServer() string {
	return filepath.Base(SocketFromEnv())
}

func FindServer(name string) (string, bool) {
	for _, s := range Servers() {
		if _, err := Run(s, "has-session", "-t", "="+name); err == nil {
			return s, true
		}
	}
	return "", false
}
```

`agentstate.go`:
```go
package proj

import "strings"

// classifyAgent maps a pane_current_command to an agent kind. The concrete
// strings for pi/claude/cursor are verified on this machine (see the task's
// note); "shell"/idle programs collapse to "shell", unknown → "".
func classifyAgent(cmd string) string {
	switch cmd {
	case "pi":
		return "pi"
	case "claude", "node": // claude presents as node in this env; verify + adjust
		return "claude"
	case "cursor-agent", "cursor", "agent":
		return "cursor"
	case "zsh", "bash", "fish", "sh", "yazi", "scratch":
		return "shell"
	default:
		return ""
	}
}

// AgentIn returns the agent kind + coarse state of the session's main pane.
func AgentIn(socket, session string) (kind, state string) {
	// main/left pane: leftmost, tallest among ties (robust to agent panes).
	left := Query(socket, session, "#{pane_id}") // placeholder; see note
	_ = left
	cmd := paneMainCommand(socket, session)
	kind = classifyAgent(cmd)
	if kind == "" || kind == "shell" {
		return kind, "idle"
	}
	act := Query(socket, session, "#{pane_activity}")
	bell := Query(socket, session, "#{window_bell_flag}")
	switch {
	case bell == "1":
		state = "waiting"
	case act != "" && act != "0":
		state = "working"
	default:
		state = "idle"
	}
	return kind, state
}

// paneMainCommand returns pane_current_command of the leftmost-tallest pane.
func paneMainCommand(socket, session string) string {
	out, err := Run(socket, "list-panes", "-t", session, "-F",
		"#{pane_left} #{pane_height} #{pane_current_command}")
	if err != nil {
		return ""
	}
	best, bestLeft, bestH := "", 1<<30, -1
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(ln)
		if len(f) < 3 {
			continue
		}
		l, h := atoi(f[0]), atoi(f[1])
		if l < bestLeft || (l == bestLeft && h > bestH) {
			bestLeft, bestH, best = l, h, f[2]
		}
	}
	return best
}
```
Add a tiny `atoi` helper (strconv.Atoi ignoring error → 0) in `tmux.go` or here. Implementer resolves the `left` placeholder — it's not needed once `paneMainCommand` exists; remove it.

`discovery.go`:
```go
package proj

type Session struct{ Name, Socket, Dir, Agent, State string }

func LiveSessions() []Session {
	var out []Session
	for _, sock := range Servers() {
		names, err := Run(sock, "list-sessions", "-F", "#{session_name}")
		if err != nil {
			continue
		}
		for _, name := range splitLines(names) {
			if name == "" {
				continue
			}
			dir := Query(sock, name, "#{pane_current_path}")
			agent, state := AgentIn(sock, name)
			out = append(out, Session{Name: name, Socket: sock, Dir: dir, Agent: agent, State: state})
		}
	}
	return out
}
```
(`splitLines` = strings.Split on "\n"; add to tmux.go.)

- [ ] **Step 5: Run tests + gates** — `go test -race ./internal/proj/` (integration skips if no tmux locally; CI runs it).

- [ ] **Step 6: Commit**
```bash
git add internal/proj/server.go internal/proj/agentstate.go internal/proj/discovery.go internal/proj/*_test.go internal/proj/tmux.go
git commit -m "feat(proj): per-project servers, live-session discovery, agent state"
```

---

### Task 6: Session creation (from `$HOME`) + agent launch

**Files:** Create `internal/proj/session.go`, `internal/proj/session_test.go` (tmux integration)

**Interfaces:**
- Consumes: `SocketFor`, `SessionName`, `Config.AgentFor`, `LabelOption`.
- Produces:
  - `func LabelOption() string` — `$MUSTER_LABEL_OPTION` or `"@claude_task"`.
  - `func EnsureSession(socket, name, dir, agent string) error` — if `has-session =name` no-op; else create **from `$HOME`** with `-c dir`, set `@claude_task` to the work segment, and launch `agent` via `send-keys` into the pane (targets `=name:` — the trailing colon is load-bearing on tmux ≥3.7). `agent==""`/`"none"` → plain shell.

- [ ] **Step 1: Integration test** (`session_test.go`)

```go
package proj

import (
	"os"
	"testing"
)

func TestEnsureSessionFromHomeSetsLabel(t *testing.T) {
	requireTmux(t)
	sock := "proj-phase1-sess"
	defer Run(sock, "kill-server")
	dir := t.TempDir()
	if err := EnsureSession(sock, "proj-phase1-sess/w", dir, "none"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	// pane dir is the requested dir...
	if got := Query(sock, "proj-phase1-sess/w", "#{pane_current_path}"); got != dir {
		t.Fatalf("pane dir = %q want %q", got, dir)
	}
	// ...but the SERVER's start dir is $HOME (cwd-poison defense): the session's
	// own start path is $HOME because new-session ran from there.
	if got := Query(sock, "proj-phase1-sess/w", "#{session_path}"); got != os.Getenv("HOME") {
		t.Fatalf("session_path = %q want $HOME", got)
	}
	if got := Query(sock, "proj-phase1-sess/w", "#{@claude_task}"); got != "w" {
		t.Fatalf("@claude_task = %q want w", got)
	}
	// idempotent
	if err := EnsureSession(sock, "proj-phase1-sess/w", dir, "none"); err != nil {
		t.Fatalf("second EnsureSession: %v", err)
	}
}
```

- [ ] **Step 2: Run to confirm failure.**

- [ ] **Step 3: Implement `internal/proj/session.go`**

```go
package proj

import (
	"os"
	"os/exec"
	"strings"
)

func LabelOption() string {
	if v := os.Getenv("MUSTER_LABEL_OPTION"); v != "" {
		return v
	}
	return "@claude_task"
}

func EnsureSession(socket, name, dir, agent string) error {
	if _, err := Run(socket, "has-session", "-t", "="+name); err == nil {
		return nil // exists
	}
	// Create from $HOME so the server's permanent cwd never pins to a dir that
	// may later be deleted (worktree). -c still sets the pane's dir.
	c := exec.Command("tmux", "-L", socket, "new-session", "-d", "-s", name, "-c", dir)
	c.Dir = os.Getenv("HOME")
	if err := c.Run(); err != nil {
		return err
	}
	// label = the work segment (after the last '/'), for muster.
	label := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		label = name[i+1:]
	}
	_, _ = Run(socket, "set-option", "-t", name, LabelOption(), label)

	if agent != "" && agent != "none" {
		if cmd := agentLaunchCmd(agent); cmd != "" {
			// target "=name:" — trailing colon resolves the active pane on tmux 3.7+.
			_, _ = Run(socket, "send-keys", "-t", "="+name+":", cmd, "Enter")
		}
	}
	return nil
}

// agentLaunchCmd returns the shell command to type into the pane, or "" if the
// agent binary is absent (degradation → plain shell).
func agentLaunchCmd(agent string) string {
	bin := map[string]string{"pi": "pi", "claude": "claude", "cursor": "cursor-agent"}[agent]
	if bin == "" {
		return ""
	}
	if _, err := exec.LookPath(bin); err != nil {
		return ""
	}
	return bin
}
```
Implementer note: `pi`/`claude` launch invocations may need session-name arguments (the zsh version bakes `pi --name <session>` / a claude launch cmd). Check the current `__pi_launch_cmd`/`__claude_launch_cmd` in `~/dotfiles/config/zsh/04-aliases.zsh` and reproduce their argument shape here rather than the bare binary, if they carry load-bearing flags. Keep the LookPath degradation.

- [ ] **Step 4: Run tests + gates.**

- [ ] **Step 5: Commit**
```bash
git add internal/proj/session.go internal/proj/session_test.go
git commit -m "feat(proj): session creation from \$HOME + agent launch"
```

---

### Task 7: Reattach — the client-move design

**Files:** Create `internal/proj/reattach.go`, `internal/proj/reattach_test.go`

**Interfaces:**
- Produces:
  - `type Action struct { Kind string; Cmd []string; Print string }` — Kind ∈ {"switch","detach","print"}.
  - `func PlanGoto(currentServer, targetSocket, name string) Action` — pure decision (no tmux calls), table-testable:
    - `currentServer == ""` (outside tmux) → `{Kind:"print", Print: "exec tmux -L "+targetSocket+" attach -t ="+name}"`
    - `currentServer == targetSocket` → `{Kind:"switch", Cmd: ["switch-client","-t","="+name]}`
    - else → `{Kind:"detach", Cmd: ["detach","-E","tmux -L "+targetSocket+" attach -t ="+name]}`
  - `func Goto(targetSocket, name string) error` — resolves current server, runs the switch/detach directly, or for "print" writes the line to stdout (the shim evals it in Phase 4).

- [ ] **Step 1: Table test the pure planner**

```go
package proj

import "reflect"
import "testing"

func TestPlanGoto(t *testing.T) {
	if a := PlanGoto("", "proj-x", "proj-x/w"); a.Kind != "print" ||
		a.Print != "exec tmux -L proj-x attach -t =proj-x/w" {
		t.Fatalf("outside-tmux: %+v", a)
	}
	if a := PlanGoto("proj-x", "proj-x", "proj-x/w"); a.Kind != "switch" ||
		!reflect.DeepEqual(a.Cmd, []string{"switch-client", "-t", "=proj-x/w"}) {
		t.Fatalf("same-server: %+v", a)
	}
	if a := PlanGoto("proj-a", "proj-x", "proj-x/w"); a.Kind != "detach" ||
		!reflect.DeepEqual(a.Cmd, []string{"detach", "-E", "tmux -L proj-x attach -t =proj-x/w"}) {
		t.Fatalf("cross-server: %+v", a)
	}
}
```

- [ ] **Step 2: Run to confirm failure.**

- [ ] **Step 3: Implement `internal/proj/reattach.go`**

```go
package proj

import "fmt"

type Action struct {
	Kind  string   // "switch" | "detach" | "print"
	Cmd   []string // tmux args for switch/detach (run on the CURRENT server)
	Print string   // shell line for the shim to eval (print case)
}

func PlanGoto(currentServer, targetSocket, name string) Action {
	switch {
	case currentServer == "":
		return Action{Kind: "print",
			Print: fmt.Sprintf("exec tmux -L %s attach -t =%s", targetSocket, name)}
	case currentServer == targetSocket:
		return Action{Kind: "switch", Cmd: []string{"switch-client", "-t", "=" + name}}
	default:
		return Action{Kind: "detach", Cmd: []string{"detach", "-E",
			fmt.Sprintf("tmux -L %s attach -t =%s", targetSocket, name)}}
	}
}

// Goto executes the planned action. switch/detach run on the CURRENT server
// (via $TMUX, no -L). print writes the exec line to stdout for the shim.
func Goto(targetSocket, name string) error {
	a := PlanGoto(CurrentServer(), targetSocket, name)
	switch a.Kind {
	case "print":
		fmt.Println(a.Print)
		return nil
	default:
		_, err := Run("", a.Cmd...) // current server via ambient $TMUX
		return err
	}
}
```

- [ ] **Step 4: Run tests + gates.**

- [ ] **Step 5: Commit**
```bash
git add internal/proj/reattach.go internal/proj/reattach_test.go
git commit -m "feat(proj): reattach planner (switch/detach/print) + Goto"
```

---

### Task 8: The Bubble Tea picker (two views, agent-presence rows)

**Files:** Create `internal/projtui/model.go`, `internal/projtui/view.go`, `internal/projtui/model_test.go`. Wire `cmd/proj/main.go` no-args path to run it.

**Interfaces:**
- Consumes: `proj.LoadRoots`, `proj.LiveSessions`, `proj.AllProjectDirs`, `proj.EnsureSession`, `proj.Goto`, `proj.LoadConfig`.
- Produces: a Bubble Tea `Model` with `Result` describing what the user chose (jump to session / new work / cancel), so the top-level can execute `EnsureSession`+`Goto` after the program exits. Follow scratch's `internal/tui/model.go` structure (Init/Update/View, `tea.Msg` handling) as the concrete pattern.

Model of the two views (this is behavior spec + the test; the implementer writes idiomatic Bubble Tea following scratch's model, with real code — no fzf):

- **State:** `view` ∈ {entrance, project}; `filter string`; `cursor int`; `rows []Row`; `project string` (in project view); `agentChoice string` (from config, cycled by `tab`); `sidebarChoice bool`; and a `Result`.
- **entrance rows:** live sessions (from `LiveSessions()`) first, then project names (from `AllProjectDirs` minus those with a live session), filtered by fuzzy substring on `filter`.
- **project rows:** a synthetic `+ new work…` FIRST, then `🏠 home base`, then that project's live sessions.
- **keys:** `enter` (act on cursor), `esc`/left (project→entrance, or quit at entrance), printable → append to `filter`, backspace, up/down, `tab` cycle agentChoice over `[configDefault, "claude","pi","cursor","none"]` dedup, `s` toggle sidebarChoice, `ctrl+c`/`q`(when filter empty) quit. (`a`/`^e` roots editing and `x` reap are Phase-later; leave their key handlers as no-op stubs with a footer hint so the layout is stable.)
- **enter semantics:**
  - entrance, on a session row → `Result{Kind:"jump", Socket, Name}`, quit.
  - entrance, on a project row → switch to project view for that project (no quit).
  - project, on `+ new work…` → enter an inline text-input sub-state; on submit slug+validate → `Result{Kind:"new", Project, Work, Agent:agentChoice, Sidebar:sidebarChoice}`, quit.
  - project, on `🏠 home base` → `Result{Kind:"new", Project, Work:"", ...}` meaning the home session (name == project), quit.
  - project, on a session row → `Result{Kind:"jump", ...}`, quit.

- [ ] **Step 1: Write `Update()`-driven tests** (`model_test.go`, following scratch's `model_test.go` style — no real tmux; inject fixture rows)

```go
package projtui

import "testing"

func TestEntranceFilterAndDrillIn(t *testing.T) {
	m := newTestModel([]Row{
		{Kind: RowSession, Label: "bettor-help/data-lake", Socket: "proj-bettor-help", Name: "bettor-help/data-lake"},
		{Kind: RowProject, Label: "tools-workspace"},
	})
	m = typeString(m, "tools")            // filter
	if visibleCount(m) != 1 { t.Fatalf("filter did not narrow: %d", visibleCount(m)) }
	m = press(m, "enter")                  // drill into project
	if m.view != viewProject || m.project != "tools-workspace" {
		t.Fatalf("did not drill in: view=%v proj=%q", m.view, m.project)
	}
}

func TestProjectNewWorkAtTopProducesResult(t *testing.T) {
	m := newProjectModel("tools-workspace", nil)
	// cursor starts on the first row, which must be "+ new work…"
	if firstRowKind(m) != RowNewWork { t.Fatal("new work not at top") }
	m = press(m, "enter")                  // open inline input
	m = typeString(m, "nfl cutover")
	m = press(m, "enter")                  // submit
	r := m.Result
	if r.Kind != "new" || r.Project != "tools-workspace" || r.Work != "nfl-cutover" {
		t.Fatalf("result = %+v", r)
	}
}
```
(The `newTestModel/typeString/press/visibleCount/firstRowKind/newProjectModel` helpers are small test shims the implementer writes alongside, mirroring how scratch's tui tests drive `Update`.)

- [ ] **Step 2: Run to confirm failure.**

- [ ] **Step 3: Implement `model.go` + `view.go`** following scratch's `internal/tui` as the pattern. Rows render as `● name  agent·state` (entrance/project sessions), plain names (projects), `+ new work…` / `🏠 home base` specials. The right-hand preview pane renders a **stub** in Phase 1 (just the highlighted row's name + dir); the rich preview is Phase 2. Footer shows the active keys incl. `tab agent:<choice>` and `s sidebar:<on/off>`.

- [ ] **Step 4: Wire `cmd/proj/main.go`** no-args path:
```go
// in run(), replace the len==0 stub:
if len(args) == 0 {
	return runPicker()   // builds the model, runs tea, then executes Result
}
```
`runPicker()` loads roots (on `ErrNoRoots`, print the zsh-parity guidance and return 1), builds the entrance model, runs `tea.NewProgram`, and on a `Result` executes: `new` → `proj.EnsureSession` then `proj.Goto`; `jump` → `proj.Goto`. Keep it thin.

- [ ] **Step 5: Run tests + gates** — `go test -race ./...` (whole module: scratch + proj).

- [ ] **Step 6: Commit**
```bash
git add internal/projtui cmd/proj/main.go
git commit -m "feat(proj): Bubble Tea two-view picker (agent-presence rows)"
```

---

### Task 9: Agent subcommands `list` / `current` / `new` (--json)

**Files:** Modify `cmd/proj/main.go`; create `cmd/proj/agentcli.go`, `cmd/proj/agentcli_test.go`

**Interfaces:**
- Consumes: `proj.LiveSessions`, `proj.LoadRoots`, `proj.AllProjectDirs`, `proj.SocketFromEnv`, `proj.ProjectFromSocket`, `proj.EnsureSession`, `proj.Config`.
- Produces the scope-C agent surface (spec §Agent surface):
  - `proj list --json` → `{"projects":[...],"sessions":[{name,project,socket,agent,state,dir}...]}`.
  - `proj current --json` → `{"project":...,"work":...,"alias":...,"dir":...}` derived from `$TMUX` socket + the ambient session's `@claude_task`; empty fields when outside a proj session.
  - `proj new <project>/<work> [--agent X] [--no-sidebar]` → resolves the project dir via `DirForName`, `EnsureSession` **detached**, and **does NOT call Goto** (never switches the operator's client — spec boundary). Prints the created session name. `--agent` defaults to `Config.AgentFor(project)`. (Sidebar honored in Phase 3; accept and ignore `--no-sidebar` now, but validate the flag so the interface is stable.)

- [ ] **Step 1: Test the pure arg parse + `current` derivation** (`agentcli_test.go`)

```go
package main

import "testing"

func TestParseNewArg(t *testing.T) {
	p, w, err := parseNewTarget("tools-workspace/nfl cutover")
	if err != nil { t.Fatal(err) }
	if p != "tools-workspace" || w != "nfl-cutover" { t.Fatalf("%q %q", p, w) }
	if _, _, err := parseNewTarget("noslash"); err == nil { t.Fatal("want error on missing /") }
	if _, _, err := parseNewTarget("p/bad.name"); err == nil { t.Fatal("want error on invalid work") }
}
```

- [ ] **Step 2: Run to confirm failure.**

- [ ] **Step 3: Implement `agentcli.go`** — `parseNewTarget` (split on first `/`, slug+validate work via `proj.SlugWork`/`proj.ValidWork`), the three subcommand handlers writing JSON via `encoding/json`, and wire them into `run()`'s switch. `new` calls `EnsureSession` only (no `Goto`). Add a comment on the `new` handler: "detached by contract — an agent mints a workspace but never switches the operator's client."

- [ ] **Step 4: Run tests + gates**, plus a manual smoke:
```bash
go build ./cmd/proj && ./proj list --json | head
```
Expected: valid JSON (possibly empty arrays), exit 0.

- [ ] **Step 5: Commit**
```bash
git add cmd/proj
git commit -m "feat(proj): agent subcommands list/current/new (--json, detached)"
```

---

## Deferred to later phases (tracked, not in this plan)

- **Phase 2 — rich state:** the `muster status --json` read (feature-detected) + ✉ column; the real preview pane (agent status / muster / git / last-line, lazy for the highlighted row); the live-refresh tick. Config already parses; discovery already yields agent state.
- **Phase 3 — sidebar:** Go sidebar builder (`scratch, yazi, shell`, configurable, `@sidebar` tags), the anchor/agent-pin/dead-path defenses, scratch's new no-terminal-probe mode (a `cmd/scratch` change), wiring the `s` toggle and `prefix f`.
- **Phase 4 — skill + shell cutover:** `.claude/skills/proj/SKILL.md`; the thin `proj()` shim (evals the `print` reattach line) + the precmd auto-join hook; roots `add`/`remove`/`edit` + first-run wizard; the `x` reap action; retiring the zsh `proj`/`pt`/`tat`/`bell-clear` and updating dotfiles. **This is the cutover — until it lands, the Go proj runs alongside the zsh proj without replacing it.**

## Self-Review

- **Spec coverage (Phase 1 slice):** roots parsing → T2; identity/alias → T3; config + overrides → T4; per-project servers + discovery + agent state → T5; create-from-`$HOME` + label + agent launch → T6; reattach switch/detach/print → T7; two-view picker with new-work-at-top + agent-presence rows + `tab`/`s` toggles → T8; agent surface `list/current/new` detached → T9. muster/preview/git/sidebar/skill/cutover are explicitly deferred.
- **Placeholder scan:** the two acknowledged "verify on this machine" notes (agent `pane_current_command` strings in T5; agent launch arg shape in T6) are **verification steps with a named command to run**, not placeholders — the implementer must observe and encode real values, and the tests pin the behavior. The BurntSushi decode note in T4 gives a concrete fallback and a "make the test match what decodes" instruction. No `TODO`/`TBD` remain.
- **Type consistency:** `Session{Name,Socket,Dir,Agent,State}` is produced in T5 and consumed by T8/T9; `Action{Kind,Cmd,Print}` from T7 is used by the picker/CLI; `Config.AgentFor/SidebarFor` from T4 feed T6/T8/T9; `ProjectFromSocket`/`AliasFor` from T3 feed T9's `current`. The `proj-<project>` socket, `@claude_task` label, and `$HOME`-create contract are asserted in T5/T6 integration tests.
