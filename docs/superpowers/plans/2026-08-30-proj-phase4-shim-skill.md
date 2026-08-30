# proj Rewrite — Phase 4 (Shim · Auto-join · Skill) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Ship the tackle-side glue that makes the Go `proj` usable as the operator's way in — the thin `proj()` shell shim, the precmd auto-join hook, and the agent skill — plus the small proj-side change the shim needs to reattach through the parent shell. **This plan does NOT perform the dotfiles cutover** (retiring the zsh `proj`/`pt`/`tat`/`bell-clear` and sourcing the shim); that is a separate, operator-gated step documented here as a runbook.

**Architecture:** A Bubble Tea TUI owns the terminal, so the shim cannot capture the binary's stdout to recover the reattach command. Instead the binary emits the `exec tmux … attach` line **out-of-band** to a file named by `$PROJ_EXEC` (set by the shim) and the shim `eval`s that file after `proj` exits. The switch-client / detach cases still run in-process (no shim needed). The shim + hook ship as `shell/proj.zsh` in tackle (the cutover sources it); the skill ships at `.claude/skills/proj/SKILL.md`.

**Tech Stack:** Go 1.26, zsh, tmux.

**Spec:** `tackle/docs/superpowers/specs/2026-08-29-proj-rewrite-design.md` — "tmux mechanics — the reattach design", "Agent surface + skill". Reference shell to preserve behavior of: `~/dotfiles/config/zsh/06-tmux-autojoin.zsh` (auto-join opt-outs). Builds on Phases 1–3.

## Global Constraints

- **Out-of-band reattach emit:** when `$PROJ_EXEC` is set and non-empty, `proj.Goto`'s "print" case writes the `exec tmux …` line to that file (truncating) instead of stdout. When `$PROJ_EXEC` is unset, it falls back to `fmt.Println` (Phase-1 behavior — so `proj` alone still works, just can't reattach a bare shell). Nothing else changes; switch/detach still run in-process.
- **The shim stays trivial and defensive:** create a temp file, export `PROJ_EXEC`, run the real binary (`command proj`), and on exit `eval` the file iff it is non-empty, then remove it. Never leave a temp file behind. Never `eval` empty/garbage.
- **Auto-join hook preserves the existing opt-outs verbatim:** skip if not interactive, if already in tmux (`$TMUX`), if `$NO_AUTO_TMUX` set, if `~/.no-auto-tmux` exists, if tmux is absent, or if `$PWD` is not under a configured project root. Otherwise open the project's view (`proj <name>`), honoring `$AUTO_CLAUDE` (→ prefer the claude agent). It runs once at shell start (not a repeating precmd) — matching the reference.
- **No cutover here:** do not edit `~/dotfiles`. Do not remove the zsh `proj`/`pt`/`tat`/`bell-clear`. The shim/hook file is shipped but only activated when the operator sources it (runbook in Task 4).
- **Testing:** the proj-side emit change is unit-tested (Go). The shell is checked with `zsh -n` (syntax) and, where a behavior is isolable, a small `zsh`-driven assertion. The skill/README are prose (no tests). `gofmt -l . && go vet ./... && go build ./... && go test -race ./...` before each code commit.

---

## File Structure

```
internal/proj/reattach.go   (MODIFY)  Goto "print" case → write $PROJ_EXEC file if set, else stdout
internal/proj/reattach_test.go (MOD)  TestGotoEmitFile
shell/proj.zsh              (NEW)     the proj() shim + __proj_autojoin hook
shell/proj.zsh.test.zsh     (NEW)     zsh -n syntax + a shim-eval smoke (optional, best-effort)
.claude/skills/proj/SKILL.md (NEW)    the agent skill (orient + spawn-then-delegate)
README.md                   (MODIFY)  proj section: install, the shim, the CUTOVER runbook
```

---

### Task 1: proj-side out-of-band reattach emit

**Files:** Modify `internal/proj/reattach.go`, `internal/proj/reattach_test.go`

**Interfaces:**
- Consumes: `$PROJ_EXEC` env var.
- Produces: `Goto` unchanged for switch/detach; for the "print" action, when `os.Getenv("PROJ_EXEC") != ""`, write `a.Print + "\n"` to that path (`os.WriteFile`, 0644, truncate) and return nil; else `fmt.Println(a.Print)`. `PlanGoto` is unchanged (still pure).

- [ ] **Step 1: Test** (`reattach_test.go`)
```go
func TestGotoEmitFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "exec")
	t.Setenv("PROJ_EXEC", f)
	t.Setenv("TMUX", "") // force the outside-tmux "print" path
	if err := Goto("proj-x", "proj-x/w"); err != nil { t.Fatal(err) }
	b, err := os.ReadFile(f)
	if err != nil { t.Fatal(err) }
	got := strings.TrimSpace(string(b))
	if got != "exec tmux -L proj-x attach -t =proj-x/w" {
		t.Fatalf("emitted %q", got)
	}
}
```
(Note: `CurrentServer()` reads `$TMUX`; with `TMUX=""` → `PlanGoto("", …)` → "print". Confirm `CurrentServer()` returns "" when `$TMUX` is empty — it does, via `SocketFromEnv`.)

- [ ] **Step 2: Run — confirm failure** (current Goto only prints).

- [ ] **Step 3: Implement.** In `reattach.go` `Goto`, change the `case "print"` branch:
```go
case "print":
	if p := os.Getenv("PROJ_EXEC"); p != "" {
		return os.WriteFile(p, []byte(a.Print+"\n"), 0o644)
	}
	fmt.Println(a.Print)
	return nil
```
Add `"os"` import if missing.

- [ ] **Step 4: Run tests + gates.**

- [ ] **Step 5: Commit** — `feat(proj): emit reattach command to $PROJ_EXEC for the shell shim`.

---

### Task 2: the `proj()` shim + auto-join hook (`shell/proj.zsh`)

**Files:** Create `shell/proj.zsh`, `shell/proj.zsh.test.zsh`

**Interfaces:**
- Produces: a sourceable zsh file defining `proj()` (the shim) and `__proj_autojoin` (run once at source time). No dependency on the old zsh proj internals.

- [ ] **Step 1: Read the reference** — `~/dotfiles/config/zsh/06-tmux-autojoin.zsh` for the exact opt-out set and the `$AUTO_CLAUDE`/`$NO_AUTO_TMUX`/`~/.no-auto-tmux` semantics to preserve.

- [ ] **Step 2: Write `shell/proj.zsh`.**
```zsh
# proj — thin shim over the tackle `proj` binary.
#
# The binary renders its TUI on the terminal and, for the one case it cannot
# handle itself (attaching a bare shell to a session on another server), writes
# an `exec tmux … attach` line to $PROJ_EXEC. We eval that line here so the
# attach replaces THIS shell. The in-tmux switch/detach cases run in-process in
# the binary and leave $PROJ_EXEC empty.
proj() {
  command -v proj >/dev/null 2>&1 || { echo "proj: binary not on PATH" >&2; return 127; }
  local ef; ef="$(mktemp "${TMPDIR:-/tmp}/proj-exec.XXXXXX")" || return 1
  PROJ_EXEC="$ef" command proj "$@"
  local rc=$?
  if [[ -s "$ef" ]]; then
    local cmd; cmd="$(<"$ef")"; rm -f "$ef"
    [[ -n "$cmd" ]] && eval "$cmd"
    return
  fi
  rm -f "$ef"
  return $rc
}

# Auto-join: a fresh interactive shell inside a project dir (and not already in
# tmux) opens that project's proj view — so ⌘T in a project's Ghostty window
# lands on proj. Opt-outs preserved from the zsh reference.
__proj_autojoin() {
  [[ $- != *i* ]] && return 0                 # interactive only
  [[ -n "${TMUX:-}" ]] && return 0            # already in tmux
  [[ -n "${NO_AUTO_TMUX:-}" ]] && return 0    # per-shell opt-out
  [[ -f "$HOME/.no-auto-tmux" ]] && return 0  # global opt-out
  command -v tmux >/dev/null 2>&1 || return 0
  command -v proj >/dev/null 2>&1 || return 0
  # Is $PWD inside a known project? Ask the binary (exit 0 + non-empty name).
  local name; name="$(proj __autojoin-project 2>/dev/null)"
  [[ -z "$name" ]] && return 0
  if [[ -n "${AUTO_CLAUDE:-}" ]]; then
    proj --claude "$name"
  else
    proj "$name"
  fi
}
__proj_autojoin
```
Note: this references a tiny binary helper `proj __autojoin-project` that prints the project name containing `$PWD` (or nothing + exit 1). Add it in Task 2 Step 3 (it's trivial and keeps root-detection logic in Go, not duplicated in shell). `proj --claude <name>` must be accepted by the binary — Phase 1 removed the `--claude` picker flag, so add a thin acceptance: `proj [--claude|--pi|--cursor] <project>` maps to opening that project's view with the agent preselected (the picker already cycles agents; this just seeds the choice). If that is more than a trivial change, instead have the hook use `AUTO_CLAUDE` by exporting a `PROJ_DEFAULT_AGENT=claude` env the picker reads as its initial agent — pick whichever is smaller and record it in the report.

- [ ] **Step 3: Add the `__autojoin-project` helper + agent-preselect to the binary** (`cmd/proj`): a hidden subcommand `__autojoin-project` that loads roots, prints `NameForDir($PWD)` (exit 0) or exits 1; and make the no-args/`<project>` picker honor an initial-agent hint (either a `--claude/--pi/--cursor` flag or `$PROJ_DEFAULT_AGENT`). Unit-test the arg wiring.

- [ ] **Step 4: `shell/proj.zsh.test.zsh`** — at minimum `zsh -n shell/proj.zsh` (syntax) invoked from the test; if feasible, a smoke that stubs `proj` with a script writing a known line to `$PROJ_EXEC` and asserts the shim would `eval` it (guard with a no-op `eval` override). Best-effort; syntax check is the floor.

- [ ] **Step 5: Run** `zsh -n shell/proj.zsh` + Go gates for the binary change.

- [ ] **Step 6: Commit** — `feat(proj): proj() shim + auto-join hook (shell/proj.zsh)`.

---

### Task 3: the agent skill

**Files:** Create `.claude/skills/proj/SKILL.md`

**Interfaces:** none (prose). Teaches an agent scope-C usage.

- [ ] **Step 1: Write `.claude/skills/proj/SKILL.md`** with frontmatter `name: proj`, a description, and body covering:
  - **Orient:** `proj list --json` (projects + live sessions + agent/state/attention), `proj current --json` (this session's project/work/alias/dir).
  - **Spawn a sibling work session:** `proj new <project>/<work> --agent pi` — creates it **detached**, launches the agent, builds the sidebar; **never switches the operator's client** (so it's safe for an agent to call).
  - **The real flow — spawn then delegate:** after `proj new`, the spawned session self-registers on muster; hand it a task with `muster send <alias> "…"` (alias = `<project>/<work>`). The operator sees it glow in their picker and drills in when ready.
  - **Boundaries:** don't switch the operator's client; don't create worktrees for the operator (that's the operator's/your-own-isolation job); muster is optional.
  - Match the house style of the existing `.claude/skills/scratch/SKILL.md` (read it first).

- [ ] **Step 2: Sanity** — no code; verify the commands named exist (`proj list/current/new`, flags) against `cmd/proj`.

- [ ] **Step 3: Commit** — `docs(proj): agent skill (orient + spawn-then-delegate)`.

---

### Task 4: README + the cutover runbook

**Files:** Modify `README.md`

- [ ] **Step 1: Add a `proj` section to README.md**: what proj is (the agent-aware tmux session picker), install (`go install github.com/schuettc/tackle/cmd/proj@main`; domain install once tackle.tools is live), and a **CUTOVER runbook** (operator-run, NOT done by this plan):
  1. Install the binary (above).
  2. Source `shell/proj.zsh` from your shell rc (e.g. `source ~/GitHub/schuettc/tackle/shell/proj.zsh`) — this defines `proj()` + the auto-join hook.
  3. Remove/disable the old zsh `proj`/`pt`/`tat`/`bell-clear` and the old `06-tmux-autojoin.zsh` so they don't shadow the shim.
  4. Repoint the `prefix f` tmux keybind from `bin/proj-right-column.sh` to `proj sidebar`.
  5. Config: `~/.config/proj/roots` (unchanged) + optional `~/.config/proj/config.toml`.
  Note that until step 2–3, the zsh `proj()` function shadows the binary, so **installing the binary changes nothing** — the cutover is deliberate and reversible.

- [ ] **Step 2: Commit** — `docs(proj): README proj section + cutover runbook`.

---

## Explicit non-goals (this plan)
- The dotfiles cutover itself (operator-gated; runbook only).
- The scratch no-probe optimization (deferred from Phase 3).
- The tackle.tools site / subaud entry (separate, tools-ops-owned effort).

## Self-Review
- **Spec coverage:** reattach out-of-band emit (T1) completes the "prints a command the shim evaluates" mechanism; the shim + hook (T2) are the "near-trivial proj() shim + precmd auto-join hook"; the skill (T3) is the Agent-surface scope-C skill; the runbook (T4) documents the deferred cutover.
- **Placeholder scan:** the T2 note offers two concrete alternatives for the agent-preselect (a `--claude` flag vs `$PROJ_DEFAULT_AGENT`) and tells the implementer to pick the smaller and record it — a decision, not a placeholder. `__autojoin-project` is fully specified.
- **Type consistency:** `PROJ_EXEC` is written by `Goto` (T1) and read by the shim (T2); `proj __autojoin-project` (T2 binary) is called by the hook (T2 shell); the skill (T3) names only real `proj list/current/new` commands.
