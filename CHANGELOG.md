# Changelog

All notable changes to `scratch` are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and versions follow
[Semantic Versioning](https://semver.org/).

## [0.3.0] — 2026-08-23

### Changed
- With no agent session visible, a pane inside tmux now keys its pad on the
  **tmux session name** (`#S`) rather than the working directory. Two
  shell-only tmux sessions open in one checkout used to collapse onto the
  directory pad and overwrite each other; they now get one pad each. The
  directory key remains for shells outside tmux.

## [0.2.0] — 2026-08-07

### Added
- The editor now **follows the agent session**. It re-resolves its pad once a
  second, so a TUI that started before the agent did — the usual case, since the
  workspace builder opens the scratch pane and the agent's pane together — moves
  onto the session's pad as soon as a `SessionStart` hook stamps the
  `@harness_session` tmux option. Unsaved edits are flushed to the previous pad
  first, so notes stay with the conversation they were typed in.
- The title bar shows the pad's own key instead of its parent directory, which
  is now `pads` for every pad and identified nothing.

### Changed
- **Breaking:** pads are now stored per user, outside the working tree, keyed by
  the coding-agent session instead of the directory. The store is
  `os.UserConfigDir()/scratch/pads/<key>.md` — `~/Library/Application Support`
  on macOS, `$XDG_CONFIG_HOME` or `~/.config` on Linux, `%AppData%` on Windows.
  The key is the agent session id (from `$CLAUDE_CODE_SESSION_ID`, or the
  `@harness_session` tmux option for a TUI running in a sibling pane), falling
  back to a flattened working directory when no agent session is visible.

  Two sessions open in one checkout used to share a single `$PWD/.scratch.md`
  and overwrite each other; they now get separate pads. A resumed session keeps
  its id and so reopens its own pad from any pane or worktree. Nothing is
  written into the working tree any more, so repositories no longer need to
  ignore `.scratch.md`.

  Existing `$PWD/.scratch.md` files are **not** migrated or read — move anything
  worth keeping by hand.

### Added
- `$SCRATCH_FILE` pins the exact pad file; `$SCRATCH_DIR` relocates the store.
- Added a Claude Code explainer skill (`.claude/skills/scratch/`), this changelog,
  and a GitHub Pages site.

## [0.1.2] — 2026-07-14

### Added
- Clear the scratchpad with `ctrl+x` — arms a `clear all? y/n` confirmation in the
  status line; `y` wipes the buffer and autosaves empty, anything else cancels
  (guards against a one-key wipe of your notes).

## [0.1.1] — 2026-07-14

### Changed
- Chrome redesign: a small filled title bar naming the pane
  (`scratch · <workspace> ●`) and a data status line showing the last **saved-at**
  time (`saved HH:MM`).

### Removed
- The empty-state `notes…` placeholder (it rendered highlighted/odd).
- The on-screen `ctrl+s · ctrl+r · ctrl+q` command hints — data over command hints.

## [0.1.0] — 2026-07-14

### Added
- First release: a per-worktree markdown scratchpad TUI that edits `$PWD/.scratch.md`.
- Debounced atomic autosave (temp-file + `rename`); saves are serialized so an
  overlapping stale write can't clobber newer content; the buffer is flushed on quit.
- Non-destructive external-change reload via fsnotify — `Classify` reloads when the
  buffer is clean, flags "changed on disk" when dirty (never clobbers), and ignores
  our own writes; the watcher watches the directory so it survives atomic renames.
- Keys: type to edit · `ctrl+s` save · `ctrl+r` reload · `ctrl+q`/`esc` quit.
- CLI subcommands: `scratch` (TUI), `scratch print`, `scratch append <text>`,
  `scratch path`.

[0.2.0]: https://github.com/schuettc/scratch/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/schuettc/scratch/releases/tag/v0.1.2
[0.1.1]: https://github.com/schuettc/scratch/releases/tag/v0.1.1
[0.1.0]: https://github.com/schuettc/scratch/releases/tag/v0.1.0
