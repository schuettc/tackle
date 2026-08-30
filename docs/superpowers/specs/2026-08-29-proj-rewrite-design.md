# proj Rewrite — Design Spec

**Status:** approved design, pre-implementation
**Date:** 2026-08-29
**Repo:** `tackle` (`cmd/proj`)
**Related:** the tackle monorepo foundation (`docs/superpowers/plans/2026-08-29-tackle-monorepo-foundation.md`); muster identity (`muster/internal/tmuxenv`); the family download standard (only relevant later, for proj's own release).

## Purpose

Rewrite proj — today ~600 lines of zsh plus a bash sidebar builder — as a Go binary in the tackle monorepo, **and improve it while we're there**. proj is the operator's way *into* a tmux work session. The rewrite keeps that fast fuzzy-picker entrance and makes it an **agent-aware live dashboard**: each session row and a preview pane show the agent running in it and any muster attention waiting for it. Commands the operator never uses are cut. The tmux and muster contracts that already work are preserved exactly.

## Operating context (why the design is shaped this way)

The operator works **almost exclusively inside an agent** (pi / claude / cursor). Consequences that drive every decision below:

- The **fzf-style picker entrance is the product** — loved, and to be *expanded*, not replaced by a headless CLI. It is rebuilt as the best native TUI we can make (Bubble Tea), not a shell-out to fzf.
- Human-only CLI verbs the operator never types (`pt`, `tat`, `bell-clear`) are **cut**. A non-TUI command earns its place only if an *agent* will use it via a skill (Section: Agent surface).
- **muster is the coordination/attention bus.** proj *enhances* muster (reads it to light up the picker) but **muster must never require proj, and proj must never require muster** — each is optional to the other.
- Worktrees stay **the agent's job**. proj is worktree-agnostic; it only stays robust to agents churning worktrees underneath it.

## Scope

**In:** `cmd/proj` Go binary; the Bubble Tea two-view picker; roots discovery + management; per-project tmux servers; agent auto-launch (default-on); the configurable sidebar builder; the agent-facing subcommands + skill; a thin `proj()` shell shim + the precmd auto-join hook.

**Out (cut):** `pt`, `tat`, `bell-clear`. **Folded in:** `proj-clean` becomes a picker keystroke ("reap idle"), not a command. **Not proj's job:** worktree creation/teardown (agents own it); writing to muster (read-only integration only).

## Hard contracts the rewrite MUST preserve

These are depended on by muster and by reproduced-bug defenses; they are spec, not implementation detail:

1. **Socket naming `proj-<project>`.** muster's `internal/tmuxenv.ProjectFromSocket` derives the project by `TrimPrefix(base, "proj-")` and returns `""` for any other socket. Renaming the socket scheme silently breaks muster identity for every proj session.
2. **The `@claude_task` label option** (overridable via `$MUSTER_LABEL_OPTION`) is muster's session label. proj sets/reads it; muster reads it.
3. **Sessions are created from `$HOME`.** A tmux server permanently inherits the cwd of the client that first started it; if that dir is later deleted (a removed worktree), tmux silently ignores `-c` and births every pane in a dead path. Creating from `$HOME` (which can't be deleted) prevents server-cwd poisoning. `-c <dir>` still sets each pane's dir.
4. **The sidebar dead-path fallback.** When a session's dir is a deleted worktree, walk to the nearest surviving ancestor (else session path, else `$HOME`) so yazi doesn't panic and half-build the column.

## Architecture

- **Home:** `tackle/cmd/proj`, sharing `internal/` where useful, released on its own prefixed tag `proj/vX.Y.Z` (per-tool release isolation from the foundation plan — shipping proj never disturbs scratch).
- **Shape:** the binary is primarily the TUI. The shell surface collapses to a near-trivial `proj()` shim plus the precmd auto-join hook. Roots parsing, discovery, session naming, the picker, per-project-server logic, and the sidebar build all move into Go.
- **Degradation is a first-class rule:** no muster → the attention column and its preview line don't render. No agent installed → new work opens a plain shell. Not a git repo → no git line. Nothing errors; columns come and go. (Same no-op philosophy as the `kempt status` nudge already in `proj()`.)

### muster is optional — a hard guarantee

proj has **zero hard dependency** on muster, enforced three ways:

1. **No code dependency.** proj does not import any muster package. The alias derivation is ~2 lines — `TrimPrefix(socket, "proj-")` for the project, read the `@claude_task` tmux option for the label — which proj reimplements itself. There is no build-time link to muster.
2. **Runtime feature-detect.** proj shells `muster status --json` only if `muster` is on `PATH` and supports it. muster absent, too old for the command, or the call failing → the ✉ column and its preview line don't render. No error, no prompt.
3. **Fully functional without it.** The picker, drill-down, live sessions, agent presence/state, git, sidebar, new work, and the reattach all work identically with no muster installed. muster only ever *adds* the attention column.

The symmetric direction already holds in muster (`ProjectFromSocket` returns `""` for non-proj sockets), so neither tool requires the other.

## The picker (TUI)

Full-screen Bubble Tea, fuzzy-filter-as-you-type, keyboard-driven, live-refreshing, with a **preview pane** on the right. Two views with drill-down (mockup rendered during design):

### View 1 — Entrance (global)
- **Live sessions first**, across every `proj-*` server — each a rich row: `● name  agent·state  ✉N`.
- **Projects below**, discovered from roots — those with no live session, ready to start fresh (dim rows).
- Type to fuzzy-filter across both. Highlight anything → the preview pane streams its detail.
- `enter` on a live session jumps to it; `enter` on a project **drills into View 2**.

### View 2 — One project
Reached by drill-in, by `proj <name>`, or by ⌘T auto-join.
- **`+ new work…` at the very top** (explicit requirement). Naming is an inline text input; submit → create `<project>/<work>`, launch the default agent, build the sidebar (unless toggled off), switch.
- **🏠 home base** — the primary clone on its current branch.
- **● live sessions of this project** — rich rows.
- `esc`/`←` returns to the entrance.

### Interactions — on keys, not list rows
The current design clutters the list with `[+ add root…]`/`[- remove…]` rows; the TUI moves these to a footer help bar:
- `enter` act · `esc`/`←` back · type to filter
- `a` add root · `^e` edit roots (opens `$EDITOR` on the roots file)
- `x`/`del` reap-or-kill the highlighted idle session (this is where `proj-clean` lives — a keystroke)
- `tab` cycle the agent the next new-work will launch (pi/claude/cursor/none), defaulting from config
- `s` toggle whether the next new-work builds the sidebar, defaulting from config

**New work launches the default agent by default** (the old `--claude` opt-in is gone; "no agent" is the deliberate `tab` choice). **Live refresh:** the picker ticks (~1–2s) so attention counts and agent state update while open.

## Row + preview state model

Each field comes from state proj can already reach:

| Field | Source | Cost |
|---|---|---|
| live ● | session exists on a `proj-<project>` server | cheap |
| agent presence (pi/claude/cursor/shell) | tmux `pane_current_command` of the main pane | cheap |
| agent state (working/waiting/idle) | tmux pane flags — `pane_activity` (working), bell/attention (waiting), silence (idle) | cheap |
| ✉ muster attention | the alias muster derives (`proj-<project>` socket + `@claude_task`), read via `muster status --json` | cheap-ish |
| git (branch ↑↓ dirty) | `git` in the project dir | mid |
| last line / topic | `capture-pane` last non-empty line, or `@claude_task` | mid |

**Honesty on fidelity:** agent *presence* is exact (pane command). Agent *state* is best-effort/coarse from tmux flags; ship the coarse signal, refine later if agents expose something better. Do not present it as precise.

**muster read is side-effect-free (the one cross-repo dependency).** proj must NOT shell `muster inbox <alias>` per row — that journals a "peek" per session per tick. Instead, muster gains a small read-only `muster status --json` (or `muster counts`) returning unread/pending per alias with no journaling. proj computes each session's alias exactly as muster does (project from the `proj-<project>` socket, label from `@claude_task`) and calls it once per tick. This is a small, additive muster change (muster already computes these counts for the inbox). If the command is absent (older muster, or muster not installed), the ✉ column simply doesn't render — the both-optional contract.

**Refresh & performance:** each tick computes only the **cheap** fields for *all* rows (presence, state, attention). The **expensive** fields (git, last-line) are computed **lazily for the highlighted row's preview only**, so a many-session machine stays snappy.

## The sidebar / right-column

Auto-built 30%-wide column: **scratch** (notes, top) → **yazi** (files, middle) → **shell** (bottom), each pane tagged `@sidebar` so `prefix f` toggles the column. It is valued and used frequently. Kept, with three improvements:

1. **Optional per session-launch.** New work builds it by default (config), but the `s` toggle starts a session without it; `prefix f` still summons/hides it anytime.
2. **Configurable contents/sizes** via `config.toml` `[sidebar.layout]` (default `scratch, yazi, shell`).
3. **The probe-focus hack shrinks.** scratch is now `cmd/scratch` in this monorepo, so proj can invoke scratch in a "don't probe the terminal" startup mode and drop scratch's half of the focus-probe dance. yazi still needs it.

**Load-bearing defenses ported faithfully** (each fixes a reproduced failure): anchor detection (insert the column above existing agent panes vs. split off a lone main pane), agent-pin hook suppression during the build (so `after-split-window` doesn't crush the fresh panes to 1 column), and the dead-path fallback. The builder becomes Go orchestrating tmux instead of `bin/proj-right-column.sh`.

## Config

Two machine-local, untracked files (never in the repo):

- **`~/.config/proj/roots`** — unchanged. The line-based list (`<dir>` roots whose children are projects; `project:<dir>` entries that are themselves projects; `~`/`$VAR` expansion; `#` comments). Drives the `add`/`remove`/`edit` flows.
- **`~/.config/proj/config.toml`** *(new)* — behavior settings, all with defaults so an absent file behaves like today (agent on, sidebar on, `scratch/yazi/shell`):
  ```toml
  default_agent = "pi"          # what new work launches; "none" = plain shell
  sidebar       = true          # auto-build the column on new work by default

  [sidebar.layout]
  panes = ["scratch", "yazi", "shell"]
  sizes = { scratch = 12, shell = 10 }   # rows; middle (yazi) fills

  [project."bettor-help"]       # optional per-project overrides
  default_agent = "claude"
  sidebar       = false
  ```
  TOML matches the family (kempt, muster).

## Agent surface + skill (scope C: orient + spawn)

Non-interactive subcommands an agent uses via a skill (the picker is human-only; agents can't drive a TUI):

- `proj list --json` — projects, live sessions, and their state (presence/attention/git). Orientation.
- `proj current --json` — this session's identity (project, work, alias, dir). Orientation.
- `proj new <project>/<work> [--agent pi|claude|cursor|none] [--no-sidebar]` — create the session **detached**: launch the agent, build the sidebar, register nothing with muster (the agent self-registers on launch). **It does NOT switch the operator's client** — minting a workspace is fine for an agent; yanking the operator's screen is not. Switching stays a human gesture in the TUI.

**Flow this enables:** inside an agent session the operator says "spin up a session for the frontend work"; the agent runs `proj new frontend/ui --agent pi`; the detached session appears in the operator's picker **glowing** (agent working, ✉ muster attention); the operator drills in when ready. proj mints the workspace; muster (which the agent already writes to) carries the task.

**Skill delivery:** a `proj` skill in the tackle repo (`.claude/skills/proj/SKILL.md`, alongside the `scratch` one) teaching orient + spawn-then-delegate-via-muster. The same content can be published as a pi skill if pi sessions should carry it.

## tmux mechanics — the reattach design

A Go child process cannot reattach the parent shell's tmux client from inside itself. proj splits by case; only one case needs the shim:

- **Inside tmux, same server** → the binary runs `tmux switch-client -t =name` directly (acts on the server + names the calling client). No shim involvement.
- **Inside tmux, other server** → the binary runs `tmux detach -E "tmux -L <srv> attach -t =name"` directly — detaches this client and execs the attach in its place (the only clean cross-server jump; servers can't share a client).
- **Outside tmux (bare shell / ⌘T auto-join)** → the binary can't `attach` on the shell's behalf (it would attach itself), so it **prints** `exec tmux -L <srv> attach -t =name` and the thin `proj()` shim `eval`s it, so the attach replaces the shell process.

The shim stays trivial: run the binary; if it emitted an `exec tmux …` line, eval it. Most paths (switch/detach) need nothing from the shim. The precmd auto-join hook rides the same path: a new interactive shell in a project dir, not already in tmux, invokes proj for that project (View 2), honoring the existing opt-outs (`$NO_AUTO_TMUX`, `~/.no-auto-tmux`, and `$AUTO_CLAUDE` semantics).

## Testing strategy

Four layers (mirrors scratch's split of pure-logic unit tests + `Update()`-driven TUI tests):

- **Pure logic** — roots parsing (roots vs `project:` entries, `~`/`$VAR` expansion, comment/blank handling), project discovery, `<project>/<work>` naming + the whitespace→hyphen slug + validity rule, alias derivation (socket→project, `@claude_task`→label), `config.toml` parsing + per-project override resolution. Unit tests, no tmux.
- **Bubble Tea model** — drive `Update()` with synthetic messages: fuzzy filter, entrance→project drill-down and back, `tab`/`s` toggles, selection dispatch, live-refresh tick handling.
- **Reattach emission** — table test asserting the exact command per case (`switch-client` / `detach -E …` / printed `exec … attach`) without a real client.
- **tmux orchestration + sidebar build** — integration tests against a real tmux on a throwaway `proj-test` socket: session creation from `$HOME`, the `<project>/<work>` name, per-project socket, sidebar pane layout + `@sidebar` tags, dead-path fallback. Run on the macOS CI runner (proj is a darwin tool).

## Cross-repo dependency (tracked)

The muster-attention column depends on a small, additive **`muster status --json`** (side-effect-free per-alias unread/pending). It is optional: proj degrades gracefully without it, so proj can ship first and light up the ✉ column once muster adds the command. This will be coordinated with the muster session over the bus; it does not block the proj rewrite.

## Explicit non-goals

- No worktree creation/teardown by proj (agents own that; proj only stays robust to worktree churn).
- No writing to muster (read-only integration only; the agent self-registers).
- No `pt` / `tat` / `bell-clear`.
- Agent `proj new` never switches the operator's client.
- proj never requires muster; muster never requires proj.
