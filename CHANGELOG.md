# Changelog

All notable changes to `scratch`, `proj`, `casebook`, `cull` and `sift` are documented here (proj, casebook, cull and sift entries are headed with the tool's name; older proj and casebook releases have none). The format follows
[Keep a Changelog](https://keepachangelog.com/), and versions follow
[Semantic Versioning](https://semver.org/).

## [sift 0.1.0] - Unreleased

### Added
- `sift init`: detects the agent harnesses you use (Claude Code, Codex, pi)
  and writes sift's config: profiles, roots, budgets and windows.
- `sift check`: audits instruction files (global files, repo files and
  skills, read at each repo's fetched base) and records the round's findings
  as rows: size, load limit, duplicate, dead path, stale status, retired
  store, misplaced content, negative rule and secret. `--json` prints them.
- `sift doctor`: config, profiles found, roots readable, and whether `gh` is
  there for PR and issue state; the page server and each enabled harness's
  channel registration.
- `sift rows add`: merges the agent's proposals (verdict, title,
  destination, text, reason) and intake rows, as JSON lines, into the round.
  A changed proposal drops its decision, and a changed merge target drops
  every merge into it.
- `sift serve`: the review page. Rows in two views, needs you and applied,
  grouped by file and check (a backlog round: issues by repo, decisions,
  closes); accept, edit or reject a row (1–3) or a whole group; a note per
  row; an edit can clear the title or text; undo and redo of a certain fix,
  each sent like any decision; passages in a wrapping prose view. Each
  decision carries the fingerprint of the proposal the page showed, and one
  on a row the agent has changed since is refused. Send delivers the
  decisions to the agent session that opened that review.
- `sift wait`: returns when you press Send and prints what to do, for
  sessions without the channel.
- `sift channel`: the MCP server for an agent session, with `sift_check`,
  `sift_review`, `sift_apply` and `sift_status`, and Send as a channel event.
- `sift apply`: the certain fixes and the rows approved and sent (delete,
  rewrite, move, merge) as one branch per repo, cut from the fetched base in
  a worktree; every path stays inside the repo (no `..`, no `.git`, no
  symlinks), and every write goes through an `os.Root` on the worktree; a
  failure after the commit keeps the branch; a repo with uncommitted or unpushed work, or an untracked
  instruction file, in its primary clone is held; with `gh` and a GitHub
  remote, a pull request (`--body-file`).
- `sift reconcile`: each apply branch against the rows apply approved:
  missing, narrowed (approved text not added by the branch verbatim, as one
  run, at its place in the file) and extra lines, each diff line owned by at
  most one row.

## [casebook 0.2.1] - 2026-10-01

### Fixed
- Git activity in temp folders is never journalled: `casebook hook`,
  `casebook record` and the spool drain at `casebook sync` drop events whose
  working directory, git dir or (for an agent's command) every action's
  directory is under a temp root (`os.TempDir()`, `$TMPDIR`, `/tmp`,
  `/var/folders`, and their `/private` real paths). Paths are compared by
  whole segments with symlinks resolved; a configured scan root is never
  temp. Copier and pytest checkouts, `tmp.*` clones, casebook's own probe
  homes and scratch clones made most of the journal. `casebook hooks adopt`
  refuses a temp clone. A temp folder doing real work is still journalled:
  a linked worktree whose git dir (or common dir) is in a configured root,
  and a push to a remote that isn't a local path or `file://` URL (a scratch
  clone's pre-push to GitHub, an agent's `git -C /tmp/x push`).

### Added
- `casebook prune --temp`: removes temp-folder events journalled before the
  fix. A dry run by default (per-file and total counts, the top temp
  prefixes, how many removed lines carry a repo and which, the derived
  records it would clean); `--apply` rewrites only
  this machine's journal files, keeping every other line byte for byte, and
  drops temp clones from its snapshot, under casebook-data's lock, in one
  commit that is then pushed. History is not rewritten. An apply that dies
  before its commit is committed under its own message by the next sync; a
  refused push keeps the commit, prints the report and says the next sync
  pushes it.

## [cull 0.1.0] - 2026-10-01

### Added
- `cull check`: judge tests with TypeSafe's Jev, for the whole suite or a diff.
  `--group` judges near-duplicate groups.
- `cull apply`: remove the tests judged cut, tidy imports, verify, and roll
  back on failure.
- `cull serve` and the review page, where you decide what Jev wasn't sure of.
- `cull channel`: the agent side, over MCP, for Claude Code and pi.
- `cull wait` for sessions without the channel.
- `cull init`, `cull doctor`, and `cull judge` (plumbing).

## [casebook 0.2.0] - 2026-10-01

### Added
- `casebook serve`: a local page for working through casebook's attention
  list, started on demand on loopback. Four sections:
  - **Attention**: views (waiting on you, new, due, proposed), a board view,
    the item with its evidence and history, decide one or many, and accept or
    reject the agent's proposals.
  - **The agent panel**: the attached session, threads with delivery state,
    a message box with the attached context, a batch tray, the agent's live
    progress line, and a waiting strip. Messages wait for the end of the
    agent's turn.
  - **Rules**: standing rules with a conditions editor, live match previews,
    exclusions and a draft/active lifecycle. Rules propose; they never
    decide. An agent can draft a rule; only the user activates it.
  - **To apply**: plans that show the exact command for every step, approval
    with an explicit session for outward steps, jobs with needs-you cards for
    public comments, restore records, verification, pause, resume and undo.
    A stale plan offers to sync first; an item is in at most one unfinished
    job.
- `casebook channel`: the agent side, over MCP, for Claude Code and pi
  (`pi-casebook` 0.2.0).
- The keyboard layer (`?` lists the keys), and clear states for a lost
  connection, a restarted server, and decisions waiting to be pushed.

### Changed
- A decision replies once it is committed; the push to casebook-data runs in
  the background. A failed push shows as `push failed · N queued` with its
  reason; plain offline stays `offline · N queued`.
- Writes to casebook-data take one lock shared by serve, the background sync
  and the CLI, so a commit never picks up another process's half-written
  files. casebook-data ignores casebook's temporary files.
- Agent-facing text names the configured user.
- The macOS binary is signed and notarized.

## [proj 0.6.2] - 2026-10-01

### Changed
- The Ghostty layout saves itself. A `client-attached` /
  `client-session-changed` hook (installed with the others when the picker
  starts) runs `proj __save-layout`, which waits two seconds for attaches to
  settle, then folds the current tabs into the saved layout. `^s` still works.
- Saving merges instead of replacing: a session with no tab right now keeps
  its saved window, so attaching one session before a restore cannot shrink
  the layout to one window.
- Reading Ghostty's tabs no longer launches Ghostty when it is not running.

## [proj 0.6.1] - 2026-09-29

### Changed
- The picker's layout is fixed: the footer is pinned to the bottom two lines
  (a message line, then the keys, with `tab` and `?` right-aligned), the list
  and preview keep a constant height, and `?` shows the key list inside the
  preview frame instead of growing the footer.
- The list scrolls only when the cursor reaches its top or bottom edge, and
  the highlight stays on its session when a refresh adds rows above it.
- One row layout on every tab: a status cell (`▸` folder, `●` session, `[x]`
  saved) then the name, so names line up. The title shows all three tabs with
  the current one highlighted; saved window groups are header lines.
- The first frame draws at once (folders need no tmux scan); sessions and
  saved show "loading…" until the background scan lands.

## [proj 0.6.0] - 2026-09-29

### Added
- Restore after a reboot. proj records every work session it sees (name,
  directory, agent, and the pi / Claude Code conversation it holds) in
  `~/.local/state/proj/sessions.json`, kept current by tmux hooks it installs
  on its servers. The record only grows; `^x` (reap) is the one removal path.
- A **saved** scope in the picker (`tab` cycles folders, sessions, saved). It
  lists sessions that need restoring, grouped by Ghostty window: `space`
  toggles, `^r` restores the checked ones and rebuilds their Ghostty windows
  and tabs, `enter` restores one and jumps to it, `^s` saves the Ghostty
  layout, `^x` forgets.
- Restored agents reopen their conversation (`pi --session <id>` /
  `claude --resume <id>`); a missing transcript starts fresh under the same
  name.

### Changed
- The picker's discovery runs in the background, so keys no longer wait on
  tmux while it refreshes.

## [0.5.5] — 2026-09-29

### Changed
- macOS binaries are signed with the Developer ID and notarized, so a copy
  downloaded in a browser runs instead of being quarantined by Gatekeeper.
  Built through the family release actions (`schuettc/tools-actions`); the
  assets and `/dl` paths are unchanged.

## [0.5.4] — 2026-09-28

### Changed
- tools-common v0.8.2: `scratch help <cmd>` and `scratch <cmd> -h` print the
  command's one-line summary under the usage line, and `scratch man` escapes
  double quotes and a leading `.` correctly.

## [0.5.3] — 2026-09-28

### Changed
- The pad key's session id now comes from tools-common's shared harness rule.
  A pi-claude-bridge child (which pi-claude-bridge now marks with
  `AGENT_SESSION_CHILD=1` next to the `AGENT_SESSION_ID` it stamps) still
  writes to the outer pi conversation's pad. A Claude session started from
  inside pi (both ids, no marker) now gets **its own** pad instead of pi's.
  The `@harness_session` tmux-option fallback is unchanged.
- Pads are written through tools-common's `WriteFileAtomic`: same fsync and
  kept-temp-on-rename-failure behaviour, temp files now named `.<pad>-*.tmp`.

## [0.5.2] — 2026-09-04

### Changed
- `path`, `print` and `append` have full help (`-h`, `help <command>`, `man`,
  `commands --json`), and exit codes follow the family standard: 0 ok,
  1 runtime error, 2 usage error (e.g. `append` with no text).

## [0.5.1] — 2026-08-30

### Added
- `scratch version` in the family format and `scratch update`, which
  self-updates from tackle.tools/dl (via tools-common).

## [0.5.0] — 2026-08-30

### Changed
- scratch moved into the tackle monorepo (`cmd/scratch`) and is released with
  prefixed tags (`scratch/vX.Y.Z`); binaries are published to
  tackle.tools/dl.

## [0.4.0] — 2026-08-28

### Changed
- The pad key now prefers **`$AGENT_SESSION_ID`**, the harness-neutral session
  id, over the Claude-specific `$CLAUDE_CODE_SESSION_ID`. A harness spawned
  inside another agent's session — pi's claude-bridge runs real Claude Code
  subprocesses for model calls, subagents and reviews — carries its own Claude
  id plus the outer agent's `AGENT_SESSION_ID`, and the pad belongs to the
  outer conversation. This also lets anything shelling out to `scratch` inside
  a pi session resolve its pad from the environment alone, with no tmux
  hand-off involved.

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
