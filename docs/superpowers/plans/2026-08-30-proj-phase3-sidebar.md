# proj Rewrite — Phase 3 (Sidebar) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Port the `bin/proj-right-column.sh` sidebar builder into Go (`internal/proj/sidebar.go`), expose it as a `proj sidebar` subcommand, and wire it into new-session creation (honoring the config default, the picker `s` toggle, and `proj new --no-sidebar`). All the hard-won defenses are preserved verbatim in behavior; the scratch no-terminal-probe optimization is **deferred** (the probe-focus dance is kept for both scratch and yazi — don't disrupt what works).

**Architecture:** `BuildSidebar` is a synchronous Go function that drives tmux to construct the `scratch → yazi → shell` column (configurable via `config.toml [sidebar_layout]`), tagging each pane `@sidebar`. It preserves: dead-path dir fallback, main-pane detection (leftmost/tallest), anchor detection (insert above existing agent panes vs. split off a lone main), `@agent_pin` hook suppression during the build, the probe-focus stagger (focus + ~0.5s per probing app), and the final main-pane 70% re-assert. The new-session flow spawns `proj sidebar` as a **detached** child so the build survives the picker exiting (mirrors the bash `&` fork).

**Tech Stack:** Go 1.26, tmux.

**Spec:** `tackle/docs/superpowers/specs/2026-08-29-proj-rewrite-design.md` — "The sidebar / right-column". Reference implementation to port: `~/dotfiles/bin/proj-right-column.sh` (read it; the Go port must preserve its documented failure-mode defenses). Builds on Phases 1–2.

## Global Constraints

- **Preserve these defenses verbatim (each fixes a reproduced failure; do not "simplify" them away):**
  1. **Dead-path fallback:** if `dir` doesn't exist (deleted worktree), walk to the nearest surviving ancestor; else the session's own `#{session_path}`; else `$HOME`. yazi panics on a missing cwd and half-builds the column.
  2. **Main pane = leftmost, ties broken by tallest** (`#{pane_left} #{pane_height} #{pane_id}` | sort). Never `head -1` (breaks after pane renumber).
  3. **Anchor detection:** the topmost non-main, non-`@sidebar` pane. If present, insert the column ABOVE it in-column (`split-window -v -b -t anchor`); else split 30% off the main pane (`split-window -h -l 30% -t main`).
  4. **`@agent_pin` suppression:** save `@agent_pin`, set it `0` for the build (so the `after-split-window` hook doesn't crush the fresh panes), restore after; re-assert main `-x 70%` once if pin was on and ≥3 panes; `select-pane` back to main.
  5. **Each pane `cd`s to `dir` itself** (`cd <qdir> && exec <app>`) in addition to `-c dir` — belt to the `-c`-ignored-on-poisoned-server braces.
  6. **`@sidebar 1`** set on every built pane so `prefix f` can toggle by tag.
- **Probe-focus stagger:** scratch and yazi each probe the terminal at startup; create each focused (they are, being the newest split) and `time.Sleep(~500ms)` before the next split so responses land in the right pane. (Deferred: a scratch no-probe mode to drop scratch's half. For now keep the stagger for both.)
- **Layout from config:** `proj.LoadConfig().SidebarLayout` — `Panes` (default `["scratch","yazi","shell"]`) top→bottom, `Sizes` (rows, e.g. `scratch=12, shell=10`; the middle pane fills). An app named `scratch` invokes the tackle `scratch` binary; `shell` invokes `$SHELL`; anything else is invoked as-is (e.g. `yazi`).
- **Detached build:** the new-session path spawns `proj sidebar` as a detached child (`Setpgid`, `Start`, no `Wait`) so it outlives the picker; `proj sidebar` itself runs `BuildSidebar` synchronously.
- **Degradation:** every tmux call tolerant; a missing app (`exec.LookPath`) → that pane runs a shell instead of failing the chain. Never panic.
- **Testing:** integration on a throwaway `proj-p3-test` socket (skip without tmux): assert the column builds with the right pane count, `@sidebar` tags, and dead-path fallback. Config-layout resolution is pure-unit-testable. `gofmt -l . && go vet ./... && go build ./... && go test -race ./...` before each commit.

---

## File Structure

```
internal/proj/
  sidebar.go       (NEW)  BuildSidebar(socket, session, dir string, layout Layout); resolveDir; helpers
  sidebar_test.go  (NEW)  integration (throwaway socket) + pure resolveDir/app-cmd tests
cmd/proj/
  agentcli.go      (MOD)  add `sidebar` subcommand → BuildSidebar on the current/target server
  main.go          (MOD)  dispatch `sidebar`; after EnsureSession in runPicker, spawn detached `proj sidebar` when the chosen Result.Sidebar is true
internal/proj/
  session.go       (MOD?) OPTIONAL: a SpawnSidebar helper (exec detached) if cleaner than inlining in main.go
```

---

### Task 1: `BuildSidebar` core (port the bash builder)

**Files:** Create `internal/proj/sidebar.go`, `internal/proj/sidebar_test.go`

**Interfaces:**
- Produces:
  - `func resolveDir(socket, session, dir string) string` (pure-ish: does `os.Stat` walks; the session_path fallback uses `Query`) — the dead-path fallback chain.
  - `func appCommand(app, dir string) string` — the `cd <qdir> && exec <app>` string; `scratch`→the scratch binary (LookPath "scratch", else shell), `shell`→`$SHELL`/zsh, else the app name; missing binary → shell.
  - `func BuildSidebar(socket, session, dir string, layout Layout)` — the full build with all six defenses + probe stagger. Best-effort; returns nothing (logs nothing).

- [ ] **Step 1: Read the reference** — read `~/dotfiles/bin/proj-right-column.sh` in full. The Go port must reproduce its `build()`/`run()` behavior and the header comments' failure-mode reasoning.

- [ ] **Step 2: Unit-test the pure helpers** (`sidebar_test.go`)

```go
func TestResolveDirFallback(t *testing.T) {
	real := t.TempDir()
	if got := resolveDir("", "", real); got != real { t.Fatalf("existing dir: %q", got) }
	// a non-existent child walks up to the surviving ancestor
	dead := filepath.Join(real, "gone", "deeper")
	if got := resolveDir("", "", dead); got != real { t.Fatalf("walk-up: %q want %q", got, real) }
	// empty/garbage → $HOME (no socket to query)
	if got := resolveDir("", "", "/definitely/not/here/xyz"); got == "" { t.Fatal("must never return empty") }
}

func TestAppCommand(t *testing.T) {
	c := appCommand("shell", "/tmp/x")
	if !strings.Contains(c, "cd ") || !strings.Contains(c, "exec ") { t.Fatalf("shell cmd: %q", c) }
	if !strings.Contains(appCommand("yazi", "/tmp/x"), "yazi") { t.Fatal("yazi passthrough") }
}
```

- [ ] **Step 3: Integration test — the build** (`sidebar_test.go`, skip without tmux)

```go
func TestBuildSidebarTagsPanes(t *testing.T) {
	requireTmux(t) // from Phase 1
	sock := "proj-p3-test"
	defer Run(sock, "kill-server")
	dir := t.TempDir()
	if err := EnsureSession(sock, "proj-p3-test/w", dir, "none"); err != nil { t.Fatal(err) }
	BuildSidebar(sock, "proj-p3-test/w", dir, Layout{Panes: []string{"scratch", "shell", "shell"}})
	// 3 sidebar panes built (use shells to avoid needing scratch/yazi installed in CI)
	out, _ := Run(sock, "list-panes", "-t", "proj-p3-test/w", "-F", "#{@sidebar}")
	n := 0
	for _, l := range splitLines(out) { if l == "1" { n++ } }
	if n < 3 { t.Fatalf("want ≥3 @sidebar panes, got %d\n%s", n, out) }
}
```
(Use `shell` for the 2nd/3rd panes so CI doesn't need `yazi`; `scratch` resolves via LookPath and falls back to a shell if the binary isn't installed.)

- [ ] **Step 4: Implement `sidebar.go`.** Port faithfully. Skeleton:

```go
package proj

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const sidebarSettle = 500 * time.Millisecond

func resolveDir(socket, session, dir string) string {
	if dir != "" {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
	}
	// walk up to nearest surviving ancestor
	d := dir
	for d != "" && d != "/" && d != "." {
		d = filepath.Dir(d)
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
	}
	if sp := Query(socket, session, "#{session_path}"); sp != "" {
		if fi, err := os.Stat(sp); err == nil && fi.IsDir() {
			return sp
		}
	}
	return os.Getenv("HOME")
}

func appCommand(app, dir string) string {
	var run string
	switch app {
	case "scratch":
		run = "scratch"
		if _, err := exec.LookPath("scratch"); err != nil {
			run = shellBin()
		}
	case "shell":
		run = shellBin()
	default:
		run = app
		if _, err := exec.LookPath(app); err != nil {
			run = shellBin() // degrade rather than break the chain
		}
	}
	return fmt.Sprintf("cd %s && exec %s", shellQuote(dir), run)
}

func shellBin() string { if s := os.Getenv("SHELL"); s != "" { return s }; return "zsh" }

// mainPane returns the leftmost (tie: tallest) pane id of session.
func mainPane(socket, session string) string {
	out, err := Run(socket, "list-panes", "-t", session, "-F", "#{pane_left} #{pane_height} #{pane_id}")
	if err != nil { return "" }
	best, bl, bh := "", 1<<30, -1
	for _, ln := range splitLines(out) {
		f := strings.Fields(ln)
		if len(f) < 3 { continue }
		l, h := atoi(f[0]), atoi(f[1])
		if l < bl || (l == bl && h > bh) { bl, bh, best = l, h, f[2] }
	}
	return best
}

// anchorPane returns the topmost non-main, non-@sidebar pane id, or "".
func anchorPane(socket, session, main string) string {
	out, err := Run(socket, "list-panes", "-t", session, "-F", "#{pane_id} #{pane_top} #{pane_left} #{?@sidebar,1,0}")
	if err != nil { return "" }
	best, bt, bl := "", 1<<30, 1<<30
	for _, ln := range splitLines(out) {
		f := strings.Fields(ln)
		if len(f) < 4 || f[0] == main || f[3] == "1" { continue }
		tp, lf := atoi(f[1]), atoi(f[2])
		if tp < bt || (tp == bt && lf < bl) { bt, bl, best = tp, lf, f[0] }
	}
	return best
}

func BuildSidebar(socket, session, dir string, layout Layout) {
	panes := layout.Panes
	if len(panes) == 0 { panes = []string{"scratch", "yazi", "shell"} }
	dir = resolveDir(socket, session, dir)
	main := mainPane(socket, session)
	if main == "" { return }

	prevPin, _ := Run(socket, "show-option", "-gqv", "@agent_pin")
	_, _ = Run(socket, "set-option", "-g", "@agent_pin", "0")
	defer func() {
		pin := prevPin
		if pin == "" { pin = "1" }
		_, _ = Run(socket, "set-option", "-g", "@agent_pin", pin)
		if pin == "1" {
			if wc := Query(socket, session, "#{window_panes}"); atoi(wc) >= 3 {
				_, _ = Run(socket, "resize-pane", "-t", main, "-x", "70%")
			}
		}
		_, _ = Run(socket, "select-pane", "-t", main)
	}()

	anchor := anchorPane(socket, session, main)
	var prev string
	for i, app := range panes {
		var id string
		switch {
		case i == 0 && anchor != "":
			id = splitTag(socket, "-v", "-b", anchor, dir, app)
		case i == 0:
			id = splitTag(socket, "-h", "-l", main, dir, app, "30%")
		default:
			id = splitTag(socket, "-v", "", prev, dir, app)
		}
		if id == "" { return }
		prev = id
		if app == "scratch" || app == "yazi" { time.Sleep(sidebarSettle) }
	}
	applySizes(socket, panes, layout.Sizes) // resize by row where given
}
```
Implement `splitTag` (a helper that runs `split-window <dir/flags> -t <target> -c dir -P -F '#{pane_id}' '<appCommand>'`, then `set-option -p -t id @sidebar 1`, returning the new pane id or ""), `applySizes` (for each named pane with a size, `resize-pane -t <id> -y <rows>` — track the ids as you build), and reuse `shellQuote` from session.go. Match the bash's `-l 30%` for the horizontal split and the `-b` (before) for the in-column insert. Keep every tmux call best-effort. The two split forms (`-h -l 30%` off main; `-v -b` above anchor; `-v` chaining below prev) mirror the bash exactly.

- [ ] **Step 5: Run tests + gates.**

- [ ] **Step 6: Commit** — `feat(proj): Go sidebar builder (port proj-right-column.sh)`.

---

### Task 2: `proj sidebar` subcommand

**Files:** Modify `cmd/proj/agentcli.go`, `cmd/proj/main.go`

**Interfaces:**
- Produces: `proj sidebar <session> [--socket S] [--dir D]` — resolves the socket (explicit `--socket`, else `CurrentServer()`, else the server hosting `<session>` via `FindServer`), resolves the dir (explicit `--dir`, else the session's `#{pane_current_path}`), loads `LoadConfig().SidebarLayout`, and calls `BuildSidebar`. Prints nothing on success. This is the entry point both the prefix-f keybind and the detached new-session spawn use.

- [ ] **Step 1: Parse-level test** — `parseSidebarArgs` (session + flags) unit test (missing session → error; flags parsed).

- [ ] **Step 2: Implement `cmdSidebar`** in agentcli.go + wire `case "sidebar":` in main.go's run() switch. Socket resolution order: `--socket` → `CurrentServer()` (if non-empty) → `FindServer(session)`; if none, exit 1 with a message. Layout from config.

- [ ] **Step 3: Run tests + gates**, smoke: `go build ./cmd/proj` (no live build needed for the unit test).

- [ ] **Step 4: Commit** — `feat(proj): proj sidebar subcommand`.

---

### Task 3: Wire the sidebar into new-session creation

**Files:** Modify `cmd/proj/main.go` (runPicker), possibly `internal/proj/session.go`

**Interfaces:**
- Consumes: the picker `Result{Sidebar bool}` (already set by the `s` toggle in Phase 1), `proj new --no-sidebar` (Phase 1 accepted it; now honor it), `Config.SidebarFor(project)`.
- Produces: after `EnsureSession` in the `new` path (both the picker's `runPicker` and `cmdNew`), when the effective sidebar decision is true, spawn `proj sidebar <name> --socket <sock> --dir <dir>` as a **detached** child (`exec.Command`, `SysProcAttr{Setpgid:true}`, `Start()`, no `Wait`) so it builds while the client attaches / after the CLI exits. `EnsureSession` itself does NOT build the sidebar (keeps it single-purpose); the caller decides.

- [ ] **Step 1: Add a `SpawnSidebarDetached(socket, session, dir string)` helper** (session.go or main.go) that builds the `proj` binary path (`os.Executable()`), runs `proj sidebar` detached. Guard: if `os.Executable()` fails, no-op (don't block the session).

- [ ] **Step 2: Wire the picker** — in `runPicker`, for `Result.Kind=="new"`, decide sidebar = `Result.Sidebar` (the `s` toggle already defaulted from config); if true, call `SpawnSidebarDetached` after `EnsureSession`, before/around `Goto`.

- [ ] **Step 3: Wire `cmdNew`** — honor `--no-sidebar`: effective = `!noSidebar && Config.SidebarFor(project)`; if true, `SpawnSidebarDetached` after `EnsureSession` (cmdNew stays detached and never calls Goto — unchanged).

- [ ] **Step 4: Test** — a unit test that `SpawnSidebarDetached` constructs the right argv (factor the argv building into a pure helper `sidebarArgv(exe, socket, session, dir) []string` and assert it) without actually spawning. Non-flaky.

- [ ] **Step 5: Run tests + gates** (whole module).

- [ ] **Step 6: Commit** — `feat(proj): build sidebar on new work (config/toggle/--no-sidebar honored)`.

---

## Deferred to Phase 4 / later

- **Phase 4:** the `proj()` shim, the precmd auto-join hook, the agent skill, the zsh cutover (which also repoints the `prefix f` tmux keybind from `bin/proj-right-column.sh`/`tmux-sidebar-toggle.sh` to `proj sidebar`, and retires the bash builder).
- **Deferred optimization:** a scratch `--no-probe` mode (Bubble Tea startup-query suppression) to drop scratch's half of the probe-focus stagger. Requires investigating scratch's actual terminal queries; not attempted here to avoid destabilizing a working build.

## Self-Review

- **Spec coverage:** the configurable `scratch/yazi/shell` column (T1 layout), all documented defenses (T1 constraints), the `prefix f` entry point (T2 `proj sidebar`), and per-launch optionality via config/`s`-toggle/`--no-sidebar` (T3). The no-probe optimization is explicitly deferred with a reason.
- **Placeholder scan:** none. `splitTag`/`applySizes` are named with precise behavior specs + the bash reference to port; the implementer reads `proj-right-column.sh` (Step 1) for the exact flag forms. The integration test uses shells (not yazi) so it runs in CI.
- **Type consistency:** `Layout{Panes,Sizes}` (Phase-1 config) consumed by `BuildSidebar` (T1) and `cmdSidebar` (T2); `SpawnSidebarDetached`/`sidebarArgv` (T3) invoke the T2 subcommand; `resolveDir`/`appCommand`/`mainPane`/`anchorPane` are internal to sidebar.go.
