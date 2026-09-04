# tackle

Small workshop tools — TUIs you run in your own terminal. A `.tools` family
member. One Go monorepo; each tool is an independent binary under `cmd/`.

## Tools

- **scratch** — per-directory scratch notes with a live TUI.
- **proj** — agent-aware tmux session picker; the way *in* to a work session.
- **creel** — masked capture of an API key/secret into a local `.env`, so it never passes through an agent's chat. Run it bare (or via `prefix S`), or let the pi `request_secret` tool drive it.

## Install

Prebuilt binaries are published per tool on each GitHub release
(`<tool>_<os>_<arch>.tar.gz`). Once tackle.tools is live, the supported install is:

    curl -fsSL https://tackle.tools/install.sh | sh -s scratch
    curl -fsSL https://tackle.tools/install.sh | sh -s proj
    curl -fsSL https://tackle.tools/install.sh | sh -s creel

Dev/fallback (requires a Go toolchain):

    go install github.com/schuettc/tackle/cmd/scratch@main
    go install github.com/schuettc/tackle/cmd/proj@main
    go install github.com/schuettc/tackle/cmd/creel@main

## proj

`proj` is an agent-aware tmux session picker — the way *in* to a work session.

### What it does

Open the picker with a bare `proj` and you get a Bubble Tea TUI that lists every
live tmux session across all per-project servers. Each row shows:

- the session name (`<project>/<work>`)
- which **agent** is attached (pi, claude, cursor, or blank)
- a **muster attention** indicator (glows when an agent wants a response)

A **preview pane** on the right shows the session's live terminal output.  A
configurable **sidebar** (pane layout, per-project toggle) sits beside the main editor
pane inside the chosen session.

Sessions live on **per-project tmux servers** (one server per project root), so
a crash or runaway in one project never takes down another.

`proj` also understands non-interactive invocations that agents use safely:

| Command | Effect |
|---------|--------|
| `proj list --json` | Print all known projects and live sessions as JSON. |
| `proj current --json` | Print the current session's identity as JSON. |
| `proj new <project>/<work> --agent <name>` | Spawn a detached session (safe from inside an agent). |
| `proj sidebar` | Re-draw the sidebar for the current session (keybind target). |

See `.claude/skills/proj/SKILL.md` for the full agent-scope specification.

### Configuration

**Roots file** — unchanged from the original:

    ~/.config/proj/roots

One project root per line (blank lines and `#` comments ignored). Example:

    # work
    ~/GitHub/myapp
    ~/GitHub/infra

    # personal
    ~/GitHub/dotfiles

**Optional TOML config:**

    ~/.config/proj/config.toml

```toml
default_agent = "pi"          # what new work launches; "none" for a plain shell
sidebar       = true          # auto-build the sidebar column on new work

[sidebar_layout]
panes = ["scratch", "yazi", "shell"]   # top → bottom
sizes = { scratch = 12, shell = 10 }   # rows; the middle pane fills

[project."bettor-help"]        # optional per-project overrides
default_agent = "claude"
sidebar       = false
```

All keys are optional; unset keys fall back to built-in defaults.

### Shell shim + auto-join hook

`proj` needs a thin zsh shim so the shell can `eval` the one command the binary
cannot execute inside its own process (attaching a bare shell to a session on a
remote server). Source it from your shell rc:

```zsh
source ~/GitHub/schuettc/tackle/shell/proj.zsh
```

`shell/proj.zsh` defines:

- **`proj()`** — wraps the binary; evals the `exec tmux … attach` line it writes
  to `$PROJ_EXEC` for the out-of-band attach case.  All other cases (in-tmux
  switch, detach, agent commands) run in-process and leave `$PROJ_EXEC` empty.
- **`__proj_autojoin`** — fired once at source time.  If the shell is interactive,
  not already inside tmux, and `$PWD` is inside a known project root, it opens
  that project's picker automatically — so ⌘T in a Ghostty window (or any
  per-project terminal) lands straight on `proj`.  Opt-outs:
  - `NO_AUTO_TMUX=1` — per-shell
  - `~/.no-auto-tmux` — global
  - `AUTO_CLAUDE=1` — auto-join with claude preselected

### Cutover runbook

> **Operator-run only.** This plan does NOT perform these steps automatically.
> The cutover is deliberate and reversible — read all steps before starting.

The current dotfiles ship their own `proj` zsh function, `pt`, `tat`,
`bell-clear`, and `06-tmux-autojoin.zsh`.  Until those are removed/disabled the
zsh function shadows the binary, so **installing the binary changes nothing** —
which is why this runbook is a distinct, manual step.

**Step 1 — Install the binary**

```zsh
go install github.com/schuettc/tackle/cmd/proj@main
```

Verify: `which proj` should print something under `~/go/bin` (or your `$GOPATH/bin`).

**Step 2 — Source the shim from your shell rc**

Add to `~/.zshrc` (or equivalent):

```zsh
source ~/GitHub/schuettc/tackle/shell/proj.zsh
```

Reload: `exec zsh`. The shim wraps the binary and fires the auto-join hook.

**Step 3 — Remove / disable the old zsh shims**

Locate and remove (or comment out) from your dotfiles:

- the old `proj()` zsh function
- `pt` (project-tmux shorthand)
- `tat` (tmux-attach shorthand)
- `bell-clear` (post-attach bell cleaner)
- `06-tmux-autojoin.zsh` (or whichever file defines the old auto-join precmd)

Once these are gone, the `proj` name resolves to the shim → binary pipeline.
Reload again: `exec zsh`.

**Step 4 — Repoint the `prefix f` tmux keybind**

If your `tmux.conf` maps `prefix f` to `bin/proj-right-column.sh` (or similar),
change it to:

```
bind f run-shell "proj sidebar"
```

Then reload tmux config: `tmux source-file ~/.config/tmux/tmux.conf` (or your
path).

**Step 5 — Verify**

```zsh
proj list --json        # should show your projects and any live sessions
proj current --json     # run from inside a tmux session
```

Open a new terminal window inside a project directory — it should land on the
`proj` picker automatically.

**Rolling back** — revert step 3 (re-enable the old functions) and step 2
(remove the `source` line). The binary stays installed and harmless.

---

## Releasing

Each tool releases independently via a prefixed tag:

    git tag scratch/v0.16.0 && git push origin scratch/v0.16.0
    git tag proj/v0.1.0    && git push origin proj/v0.1.0

That builds and publishes **only** the tagged tool (darwin/linux × arm64/amd64),
leaving every other tool untouched.

## Develop

    go build ./cmd/scratch
    go build ./cmd/proj
    go test ./...
