# Changelog

All notable changes to `scratch`, `proj`, `casebook`, `cull` and `sift` are documented here (proj, casebook, cull and sift entries are headed with the tool's name; older proj and casebook releases have none). The format follows
[Keep a Changelog](https://keepachangelog.com/), and versions follow
[Semantic Versioning](https://semver.org/).

## [casebook 0.4.5] - 2026-10-07

### Fixed
- A recommendation made for the item you have open now shows on it at
  once: its recommendation card, with the reason, and the "recommended"
  mark on the proposed choice. Before, the list row said "pi recommends
  …" while the open item showed nothing until you reopened it. The same
  goes for a recommendation accepted, changed or rejected elsewhere, and
  for one a rule makes. If you are mid-answer on that item (a closing
  comment typed, or the Not now picker open), it isn't redrawn under you:
  a quiet line reads "pi recommends ‹label› · show", and "show" redraws it.
- Every `proposals` live event now names the items its proposals are for
  (`keys`), and a rule's new proposals (propose once, or an active rule
  after a sync) publish one too, from `rule:<id>`.

### Added
- "ask ‹session› to look into it" under the cards of the item you have
  open, when a session is attached to the page. It decides nothing: it
  sends that session a normal message, with the item attached, asking it
  to check the item's CI, recent activity and anything blocking it, add
  what it finds as evidence and recommend what to do. Like any message, it
  waits for the end of the agent's turn.
- While that message is queued or being worked on, the item says
  "‹session› is looking into it" under its question, and its list row says
  so too. It goes when the agent answers, declines or fails the message;
  the evidence and recommendation it added show on the open item as they
  come. serve marks the item (`looking` on the item's view) from the
  message itself: Court's unsettled message that starts "Look into
  ‹key›:" with that item attached.
- The recommending guide asks the agent, when asked to look into an item,
  to read its CI and recent activity with gh and record what it finds with
  casebook_evidence before recommending.

### Changed
- The recommendation card's head and the board's cards say "recommends",
  as the list's rows do: "pi recommends · Leave it open".
- New evidence for the item you have open shows on it at once (with the
  same "· show" line instead while you are mid-answer: "new evidence ·
  show").

## [casebook 0.4.4] - 2026-10-07

### Changed
- "Leave it open" (and "Keep it" for a branch, worktree or repository)
  keeps the item in your list. It moves to a "left open" group at the
  bottom of each Attention view it would be in (new; waiting on you when
  it is flagged; proposed; all), after the items that need a decision. A
  quiet divider reads "left open · N" with the group's own count, and each
  muted row reads "left open · <date>". The view's count leaves left-open
  items out. Opening one shows its decision and the cards as usual.
  Move-on after a decision skips them. A kept pull request or issue that
  has since closed or merged on GitHub drops out.
- Someone else's activity on a kept pull request or issue that is newer
  than the decision brings it back to the top, needing a decision, with
  "new activity since you left it open". It counts again, and
  `casebook_next` hands it out. `casebook_next` never hands out a quiet
  left-open item.
- "Not now" also comes back on someone else's newer activity, not only
  when its condition is met. The item says which: "its condition was met"
  or "new activity since Not now".
- The cards say so. Leave it open: "It stays open and stays in your list,
  at the bottom. It moves back up when someone replies or it changes."
  Keep it: "It stays as it is and stays in your list, at the bottom."
  Not now: "Hidden until a date or an event you pick, or until someone
  replies or it changes. Then it asks again."
- Someone else's activity is what the GitHub cache records: a pull
  request's or issue's last comment, when someone other than you wrote it
  (or, with no comments, its creation by someone else). The cache keeps no
  commits or reviews, so a push or a review alone doesn't bring an item
  back yet.
- An item's computed status can be `left-open`. `GET /api/items` adds
  `left_open` and `left_open_total` (the view's left-open group), and
  items carry `new_activity`. The board leaves left-open items off. The
  README board lists them in a "Left open" lane, and its summary counts
  them. Stop tracking it is unchanged. casebook-data's format is
  unchanged: this is how status is computed from the decisions you
  already have.

## [casebook 0.4.3] - 2026-10-07

- The same code as 0.4.2: it was tagged before the change listed under 0.4.4 had merged.

## [casebook 0.4.2] - 2026-10-07

### Fixed
- The page no longer replays the whole event log when it loads. It
  starts its live stream at the cursor its first summary carries
  (`GET /api/summary`'s new `cursor`), and everything it shows on load
  comes from its own first reads. On a machine whose log held 206,439
  events, an item took 24.6 s to show after a click; it now shows at
  once.
- A heartbeat no longer publishes a `sessions` event unless it changed
  what the page shows of the session: a new session, one crossing into
  or out of left, a new name or label, becoming or no longer being a
  worker, an end, a queued count. Sessions crossing into left are still
  announced by serve's watch, and the watch now also announces a
  delivery going stuck (or no longer stuck), which the page used to
  learn from the next heartbeat. About 40 live sessions used to publish
  one each every 30 s, and every one made each open page fetch the
  session list again (14,870 fetches for one click).
- The dock and To apply each fetch the session list with at most one
  request in flight and one queued, however many events arrive.
- `GET /api/sessions` answers only what the page uses: live sessions
  that are not subagent workers, plus any named by `?session=` (comma-
  separated: the dock's attached session, shown after it has left, and
  To apply's jobs' sessions). `?all=1` answers every known session.

### Changed
- serve prunes the event log when it starts and every hour, keeping the
  newest 10,000 events or the last 7 days of them, whichever is more.
  It used to drop week-old events only after a day of uptime. A page
  whose cursor is older than the oldest event kept hears a `gap` event
  first (the stream and `/api/state` both send it), and reloads all it
  shows.

## [casebook 0.4.1] - 2026-10-07

### Added
- The open item's key links to its page on GitHub (a pull request, an
  issue or a repository), in a new tab. serve builds the link from the
  key (the item's `url`). A branch or a worktree has no page, so its key
  stays plain text.
- Choosing "Close it" (an issue) or "Close it without merging" (a pull
  request) opens a closing-comment field under the cards. **close with
  this comment** (or ↵) decides with the text as the decision's note.
  **close without comment** decides with no note. Esc cancels. Nothing
  is decided until you press one of them. Number keys still pick the card.
  Selecting several items and agree with all work as before.
- Accepting a close recommendation on the open item (`a`, or the card's
  accept) opens the same closing-comment field, empty, and accepts from
  its buttons. `POST /api/proposals/accept` takes an optional `note`:
  Court's closing comment for an accepted close.

### Changed
- A close posts its decision's note as the closing comment, and nothing
  else. A close with no note now closes without a comment: the command
  has no `--comment` and the step doesn't post. It used to post
  "Closing.". casebook-data's format is unchanged.
- A recommendation's note (the agent's or a rule's reason, written to
  you) is never the closing comment. Accepting a close recommendation
  records no note unless you give the comment yourself: agree with all,
  accept N and an API accept close without a comment. The reason stays
  on the recommendation. Accepting any other recommendation keeps its
  note, as before (it is never posted).

## [casebook 0.4.0] - 2026-10-07

### Changed
- Deciding asks a question ("What should happen to this pull request?")
  and offers its answers as cards that say what each one does and when.
  One click decides; number keys pick a card; `u` undoes. After a
  decision the next undecided item opens. Selecting several items asks
  the same question with the same cards.
- wait and watch are one choice, "Not now". It offers conditions to pick
  (in 1 week, on a date, when a PR merges, when it goes quiet for 90
  days, ...). A PR, issue or repo is found by searching casebook's items
  or pasting a GitHub URL. Existing `watch` decisions read as Not now.
- The server holds all of this wording (`GET /api/decisions/vocabulary`):
  the page and the agent's guide read the same words.

### Added
- `casebook_next`: an agent works through the items that need a
  recommendation, one at a time, with a guide on how to recommend. A
  recommendation from it needs a one-line reason.
- Recommended items sort first. Attention shows "N recommended · M not
  yet" and can ask the attached session to recommend the rest.
  "agree with all N" accepts a group of matching recommendations; close,
  merge, archive and delete still only go to To apply.
- `POST /api/decisions/clear` removes a decision (undo).
- A Not now condition naming a PR, issue or repo that GitHub can't find
  brings the item back to attention, saying so, after the next sync.

## [casebook 0.3.1] - 2026-10-06

### Fixed
- A casebook serve restart no longer wakes every agent session on the
  machine. A session hears about a restart only when that restart
  interrupted messages in flight to it, and the notice says which ones
  ("casebook serve restarted while 2 messages to you were in flight (7,
  8)…"). Every other session hears nothing. The channel asks serve (the
  new, session-scoped `GET /api/agent/interrupted`), and when serve can't
  answer (an older serve, an error) it stays silent. The notice no longer
  mentions the page reopening in a new tab: that is about the browser, not
  the agent's work.

### Upgrading
- The fix is in the channel each agent session runs (`casebook channel`)
  and in serve. Sessions still running the 0.3.0 channel keep the old
  behaviour until they restart: every serve restart still wakes them. So a
  serve restart while 0.3.0 channels are running (including the one that
  picks up this release) wakes those sessions one last time; restart serve
  when few old sessions are left. Until serve restarts, a new channel finds
  an old serve that can't say what a restart interrupted, and stays silent.

## [casebook 0.3.0] - 2026-10-06

### Changed
- The page belongs to the agent session that opened it: `casebook_open` and
  `casebook serve` run by a session open it attached to that session, and
  it never sends anywhere else. With no session it asks which one, unless
  exactly one is here. pi-subagents workers are not offered; pi sessions
  show by name (`pi-casebook` 0.3.0 reports names and parent sessions).
- serve refuses (409) a page's message or batch into a thread that now
  belongs to another session, and the page drops a thread moved away.
- serve prunes sessions gone for 7 days that nothing refers to.

### Upgrading
- Restart casebook serve after installing (`casebook serve --stop`; the next
  `casebook_open` or `casebook serve` starts the new one). Neither kempt nor
  anything in casebook restarts a serve that is already running, and an old
  serve can't attach the page: until it restarts, `casebook_open` opens the
  page unattached and says a restart is needed. The working database moves
  to schema v3, which older casebook binaries refuse.

## [sift 0.1.0] - 2026-10-06

### Added
- `sift init`: detects the agent harnesses you use (Claude Code, Codex, pi)
  and writes sift's config: profiles, roots, budgets and windows.
- `sift check`: audits instruction files (global files, repo files and
  skills, read at each repo's fetched base) and records the round's findings
  as rows: size, load limit, duplicate, dead path, stale status, retired
  store, misplaced content and secret, and each audited file (where it is,
  the commit it was read at, its content's hash; never the content). Forks
  (a repo with an `upstream` remote) and vendor-managed skill directories
  (Claude Code's `skills/synced`, `plugins/cache` and `plugins/marketplaces`)
  are skipped and listed, unless the config's `[include]` turns them on.
  `--json` prints them.
- `sift doctor`: config, profiles found, roots readable, and whether `gh` is
  there for PR and issue state; the page server and each enabled harness's
  channel registration.
- `sift next` and `sift propose`: the recommending loop. Every file with
  findings gets one recommendation from the agent: the whole revised file,
  the base it started from, each finding fixed (how) or kept (why), links to
  the files it moves text to or from, and a summary. `propose` refuses a
  stale base, a finding left out or unexplained, a certain finding kept, a
  one-way link, and content unchanged while something is fixed; a new
  recommendation drops the decisions on its file and the files linked to it.
  `next` gives the guidance for the rewrite with each file.
- Round states: recommending until every file has a recommendation, then
  ready, sent and applied. The page and `sift_review` wait for ready.
- `sift rows add`: merges the agent's rows (proposals and intake rows), as
  JSON lines, into a backlog or intake round, decided per item. A changed
  proposal drops its decision, and a changed merge target drops every merge
  into it.
- `sift serve`: the review page. An audit round has two sections. To
  change lists one entry per file the recommendation changes (sizes before
  and after, findings, the version picked), linked files together; a file
  asks which version it should have, side by side: 1 current, 2
  recommended, or your own (e writes it; it is then a third choice, 3),
  then the findings with what the chosen version does, a note, and a
  wrapping diff with the certain fixes marked. Linked files are picked
  together. Nothing to change lists the files the agent keeps as they are,
  with its reason for each finding: agree (one file, or a for all) mutes
  their findings until the text changes; disagree takes a note, and Send
  asks the agent to recommend the file again. A backlog round lists its
  items, grouped as issues by repo, decisions and closes, each with accept,
  edit or reject; an item shows your edit once you make one, and
  accepting it keeps the edit; u reverts to the proposal. Each decision and clear
  carries the fingerprints of what the page showed (an edit included), and
  one the agent or another page has changed since is refused, and the page
  reloads what changed. A decision shows once the server has it, and while
  it saves, its linked files and Send wait; while Send is in flight,
  nothing else runs. Send delivers the decisions the page showed, those
  still in force, to the agent session that opened that review, and the
  page marks only those sent.
- `sift wait`: returns when you press Send and prints what to do, for
  sessions without the channel.
- `sift channel`: the MCP server for an agent session, with `sift_check`,
  `sift_next`, `sift_propose`, `sift_review`, `sift_apply`, `sift_status`
  and `sift_clean`, and Send as a channel event.
- `sift apply`: each file accepted or edited and sent, written whole, as one
  branch per repo cut from the fetched base in a worktree; a file whose base
  changed since the audit is held, with the files linked to it; every path
  stays inside the repo (no `..`, no `.git`, no symlinks), and every write
  goes through an `os.Root` on the worktree; a failure after the commit
  keeps the branch; a repo with uncommitted or unpushed work, or an
  untracked instruction file, in its primary clone is held; with `gh` and a
  GitHub remote, a pull request (`--body-file`). A file outside any repo is
  left to you with its approved content saved; a backlog round's approved
  rows are left to the agent.
- A repo's own base: `[[repo]] path = …, base = "dev"` in the config. The
  audit reads that repo at `origin/dev`, and apply cuts its branch from it
  and opens its pull request against `dev`. `sift init` lists the repos
  that have an `origin/dev` for you to confirm, and never sets a base itself.
- A skill or global file that is a symlink into a git repo is audited at
  the base of the repo that owns it and written there, on that repo's
  branch from the ref the audit read, with the same checks and holds as any
  repo file. A symlink to a file outside every repo is still left to
  you, its approved content saved.
- `sift clean` (and `sift_clean`): removes what apply recorded creating, and
  nothing found by name or author. Each round branch goes, local and remote,
  once its pull request is merged or closed (checked with `gh`; without
  `gh`, once its commit is in the base), and so does any worktree apply left
  behind (only its own registration: no blanket prune). A branch is
  deleted only while it is at the commit apply made: checked again just
  before a local delete, and leased on that commit for the remote one; a
  branch now elsewhere, or recorded without its commit, is kept.
  It prints the plan first; `--dry-run` stops there. Branch deletes skip
  the repo's pre-push hook, since they push no code.
- The sift skill: how an agent runs a round end to end through the channel
  tools. Embedded in the binary; `sift skills install` installs it for each
  harness found.
- `cmd/sift/kempt.toml`: the binary from tackle.tools and the channel for
  Claude Code and pi.
- `sift reconcile`: each file on an apply branch equals its approved
  content; any other file the branch changes is extra.

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

## [cull 0.1.2] - 2026-10-06

### Fixed
- An agent session started in a folder that holds several repositories (a
  workspace) can now use cull on the repositories inside it.
  `cull_review` and `cull_status` take a repository's path, like
  `cull_check` already did; the session owns that repository's review; and
  your Send for any repository inside the session's folder reaches it.
  Before, the session counted only for the folder it started in, so the
  review opened empty and Send never reached it. `cull wait` follows the
  same rule.
- The message an agent gets on Send names the repository's path, so an agent
  working from a workspace knows where to run `cull_check` and `cull_apply`.
- `cull init` at a workspace folder (no tests of its own, repositories inside)
  saves the key and lists the repositories to run it in, instead of allowing
  the workspace folder to send test source.

## [cull 0.1.1] - 2026-10-06

### Fixed
- `cull check` on a whole project failed ("argument list too long") when the
  project had a virtual environment that git ignores, such as `.venv/`: it
  looked at every test file on disk, including the third-party packages'
  own tests. In a git repository it now uses git's list of files (tracked
  files, plus new files that aren't ignored).
- `cull doctor` said cull was not registered in pi when it was: it now reads
  pi's `channelServers`. It also read `~/.claude.json` correctly only when
  that file held nothing but `mcpServers`.
- `cull doctor` no longer fails a project whose TypeScript tests have no
  `node_modules/typescript` at the project root; `cull check` skips those
  tests and says so, and doctor now notes it. Its test command line shows
  the command and how many test files it runs, not every file name.

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
