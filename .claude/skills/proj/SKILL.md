---
name: proj
description: Teaches an agent scope-C usage of proj — how to orient (list projects and live sessions, inspect the current session), spawn a sibling work session detached without touching the operator's client, and hand off a task via muster. Use when you need to create a new work session for a project or understand what sessions are already running.
---

# Using proj as an agent

`proj` is a tmux-based workspace manager. As an agent you operate at **scope C**:
orient yourself, spawn new sessions detached, and delegate. You must **never**
switch the operator's client to another window — that is the operator's job.

## Orient

### What projects and sessions exist?

```bash
proj list --json
```

Returns:

```json
{
  "projects": ["myapp", "dotfiles", "infra"],
  "sessions": [
    {
      "name":    "myapp/feat-auth",
      "project": "myapp",
      "socket":  "myapp",
      "agent":   "pi",
      "state":   "running",
      "dir":     "/Users/you/GitHub/myapp"
    }
  ]
}
```

- `projects` — every project known to `~/.config/proj/roots`.
- `sessions` — every **live** tmux session across all per-project servers.
  - `agent` — which agent is attached (`pi`, `claude`, `cursor`, or blank).
  - `state` — last-known tmux state (`running`, `idle`, etc.).
  - `dir` — working directory of the session's active pane.

### What session am I in right now?

```bash
proj current --json
```

Returns:

```json
{
  "project": "myapp",
  "work":    "feat-auth",
  "alias":   "myapp/feat-auth",
  "dir":     "/Users/you/GitHub/myapp"
}
```

All fields are empty strings when invoked outside a proj session. Use `alias`
(format `<project>/<work>`) as the muster address for this session.

## Spawn a sibling work session

```bash
proj new <project>/<work> --agent pi
```

This is the **only** `proj` command you should issue to create new work. It:

1. Creates (or reuses) a tmux session named `<project>/<work>` on the project's
   server — **detached** (no window switch).
2. Launches the agent process (`pi`, `claude`, `cursor`, or `none`) inside it.
3. Builds the sidebar layout if configured.
4. Prints the session name to stdout and exits.

The operator's client is **never switched**. It is safe to call from inside any
running agent session.

Options:

| Flag | Effect |
|------|--------|
| `--agent <name>` | Override the configured agent (`pi`, `claude`, `cursor`, `none`). Required when you want a specific agent. |
| `--no-sidebar` | Suppress sidebar spawn even if configured. |

Example — spin up a pi agent for a new feature branch:

```bash
proj new myapp/feat-payments --agent pi
# → myapp/feat-payments
```

## The real flow: spawn then delegate

After `proj new` the spawned session self-registers on muster. Hand it a task
immediately with:

```bash
muster send myapp/feat-payments "Implement Stripe webhook handler — see docs/stripe.md"
```

The alias is always `<project>/<work>` (the same string you passed to `proj new`).
The operator sees the new session glow in their picker and drills in when ready.
You don't need to do anything else — the agent in the new session reads the
message and gets to work.

Typical pattern:

```bash
# 1. Spawn the worker session
proj new myapp/feat-payments --agent pi

# 2. Delegate the task
muster send myapp/feat-payments "Implement Stripe webhook handler per docs/stripe.md"

# 3. Continue your own work — the operator will review when the session glows
```

## Boundaries

- **Never call `proj` without `--json` or `new`** from automation — the bare
  `proj` command opens an interactive TUI picker that blocks.
- **Never switch the operator's client.** `proj new` is deliberately detached.
  There is no agent-safe way to call `proj goto` or the bare `proj` picker.
- **Worktrees are your problem, not proj's.** If your task requires an isolated
  git worktree, create it yourself before or after spawning the session. `proj`
  is worktree-agnostic.
- **muster is optional.** `proj new` works fine without muster installed. If
  muster is absent, omit the `muster send` step and document the task another
  way (e.g. write it to the session's scratch pad).

## Build / test / run

```bash
go build ./...                              # compile
go test ./...                               # unit tests
proj list --json                            # inspect live state
proj current --json                         # inspect this session
proj new <project>/<work> --agent pi        # spawn (detached)
```
