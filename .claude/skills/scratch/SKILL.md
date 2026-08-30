---
name: scratch
description: Explains the scratch codebase — a fast markdown scratchpad TUI scoped to a coding-agent session (Go / Bubble Tea). Use when working in, extending, debugging, or trying to understand THIS repository: its architecture (the notes file layer, pad path resolution, the Bubble Tea model, the fsnotify watcher), the non-obvious design decisions (session-keyed pad storage, atomic writes, the Classify reload decision, serialized autosave, the terminal-probe focus dance, directory-watching), where the design specs live, and how to build / test / run / release.
---

# Understanding scratch

`scratch` is a small, keyboard-first terminal UI that edits **one markdown pad per
coding-agent session**, stored per user outside any repository (resolution lives in
`internal/notes/locate.go`). The file on disk is the single source of
truth; the TUI is a window onto it. It autosaves, reloads external edits
non-destructively, and never leaves a half-written file. Built on the Charm stack
(Bubble Tea, Bubbles, Lipgloss) plus fsnotify.

Use this skill to orient fast, then read the code. When a design choice looks
surprising, it's almost always deliberate — the "why" is captured below and, in
depth, in `docs/superpowers/specs/` and `docs/superpowers/plans/`.

## Layout

```
cmd/scratch/main.go         CLI dispatch: run(cwd, args, stdout) int
internal/notes/             pure file layer (no TUI deps) — the easy-to-test core
  notes.go                    Path, Read, Write (atomic), Append, Classify
  notes_test.go
internal/tui/               the terminal UI
  model.go                    Bubble Tea Model: editing, autosave, chrome, keybinds
  model_test.go               drives Update() with synthetic messages
  watcher.go                  fsnotify watcher + Run(path) entry point
docs/superpowers/specs/     the design docs (the "why")
docs/superpowers/plans/     the task-by-task build plans
```

## The pieces

### `internal/notes` — the file layer (start here)
Pure functions, fully unit-tested, no TUI dependency. This is where correctness lives.

- `Path(cwd) → (padPath, error)` — resolution lives in `locate.go`, keyed by the
  agent session (`$CLAUDE_CODE_SESSION_ID`, else the `@harness_session` tmux
  option) and falling back to a flattened `cwd`. It returns an **error** rather
  than a path inside `cwd` when the store location is undiscoverable: writing a
  pad into the working tree is the outcome the store exists to prevent.
- `Read(path) → (string, error)`. A **missing file is not an error** — it returns
  `("", nil)`. Only real I/O errors (permissions, etc.) return non-nil.
- `Write(path, content)` — **atomic**: write a temp file *in the same directory* →
  `fsync` → `rename` over the target. A concurrent reader (Claude, yazi's preview)
  never sees a partial file. On a write failure the temp is removed; on a *rename*
  failure the temp is **kept** and its path is named in the error, so no content is lost.
- `Append(path, line)` — atomic append with a separating newline; used by the
  `scratch append "…"` subcommand (for hooks/scripts).
- `Classify(diskContent, lastWritten, dirty) → Action` — **the single reload
  decision**, the whole conflict-safety story in one pure function:
  - `diskContent == lastWritten` → **Ignore** (it's our own write echoing back via fsnotify).
  - else `dirty` → **Flag** (unsaved local edits — surface "changed on disk", never clobber).
  - else → **Reload** (buffer clean, real external change — load it).

### `internal/tui/model.go` — the Bubble Tea model
The editor is a `bubbles/textarea`; the file is the source of truth. The subtle
parts (each has a test in `model_test.go`):

- **Debounced autosave** via a *generation counter*: each text-changing keystroke
  bumps `gen` and schedules a `tea.Tick`; a `saveMsg` only saves if its `gen` still
  matches (no keystroke since). ~500ms.
- **Serialized saves** (`triggerSave` + `saving` guard): Bubble Tea runs commands
  in separate goroutines, so two overlapping `notes.Write`s could race and a *stale*
  one could land last, clobbering newer content. The guard allows only one write in
  flight; when it completes, if the buffer changed meanwhile it *drains* — issues one
  more save — so disk always converges to the latest content without concurrent writes.
- **Quit flushes through the guard** (`quitting` flag): `ctrl+q`/`esc` don't fire a
  raw concurrent save; they wait for any in-flight save + drain, then quit — so the
  newest edits are never lost on exit. (A persistent write error still quits, to not
  trap the user.)
- **Reload / echo suppression:** on a `DiskChangeMsg` the model calls `Classify`. A
  successful save clears the `changed on disk` flag and updates `lastWritten`, which
  also suppresses the spurious flag our own write's fsnotify echo could otherwise raise.
- **Clear:** `ctrl+x` arms a `clear all? y/n` confirmation (status line); the next key
  decides — `y` wipes + autosaves empty, anything else cancels.
- **Chrome:** a small filled title bar (`scratch · <workspace> ●/○`) + a data status
  line (`saved HH:MM`, or `changed on disk`, or a save error in red). Catppuccin Mocha.
  There are intentionally **no on-screen command hints** — data over hints.

### `internal/tui/watcher.go` — external-change watching
- `watchCmd` blocks on fsnotify until a relevant event (`Write|Create|Rename`) for
  the target file, then emits a `DiskChangeMsg` with the file's current contents; the
  model re-issues it to keep listening.
- **It watches the file's DIRECTORY, filtering by basename** — load-bearing: an atomic
  save replaces the inode, so watching the file directly would silently drop the watch.
- `Run(path) int` builds the model, adds the dir to the watcher, sets `WatchCmd`, and
  runs the program. It **degrades gracefully** — if the watcher can't be created, the
  editor still runs (just without live auto-reload); `WatchCmd` is nil-safe throughout.

### `cmd/scratch/main.go` — CLI
`run(cwd, args, stdout) int` (injectable for tests). Subcommands: `path` (print the
resolved file path), `print` (file → stdout), `append "…"` (atomic append). No args →
`tui.Run`. Exit codes: 0 success, 1 runtime error, 2 usage/unknown.

## Keys
type to edit · `ctrl+s` save · `ctrl+r` reload from disk · `ctrl+x` clear (`y/n`) ·
`ctrl+q`/`esc` quit. Autosave runs ~500ms after you stop typing, on quit, and on `ctrl+s`.

## Build / test / run / release

```bash
go build ./...            # compile
go test ./...             # unit tests (notes = pure logic; tui = drives Update())
go vet ./...              # keep clean before committing
go run ./cmd/scratch      # run the TUI on this session's pad (see `scratch path`)
go install github.com/schuettc/tackle/cmd/scratch@main   # install the published binary
```

The TUI can't be asserted headlessly by unit tests alone; the `tui` tests drive
`Update()` with synthetic `tea.Msg`s (this is how autosave/reload/clear are verified).
For real end-to-end confidence, drive the binary through a PTY that answers the
terminal's startup probes (background-color / cursor-position), or just run it.

**Releasing:** commit with a clear message → cut a release by pushing a **prefixed**
tag `scratch/vX.Y.Z` (e.g. `git tag scratch/v0.16.0 && git push origin
scratch/v0.16.0`). That tag triggers `.github/workflows/release.yml`, which builds
only scratch (darwin/linux × arm64/amd64) and publishes
`scratch_<os>_<arch>.tar.gz` + `.sha256` assets. Update `CHANGELOG.md`. The dotfiles
reference installs scratch via the tackle module path
(`go install github.com/schuettc/tackle/cmd/scratch@main`). Keep every numeric
default a knob and every release note explaining *what changed and why*.

## Where to change things
- New file behavior / atomicity / the reload rule → `internal/notes` (add a unit test).
- Keybinds, autosave/save-state machine, chrome → `internal/tui/model.go`
  (add a `model_test.go` case that drives `Update()`).
- Watching / startup / graceful degradation → `internal/tui/watcher.go`.
- A new subcommand → `cmd/scratch/main.go` `run()` + `cmd/scratch/main_test.go`.
- The design rationale for any of the above → `docs/superpowers/specs/`.
