# casebook repository format, version 2

A casebook data repository is a private git repository written only by the
`casebook` binary. History is linear: no merge commits, no branches.

On each machine, every casebook process (serve, the launchd or CLI sync, the
CLI decide) writes its clone only under a lock on `.git/casebook.lock`
(flock): a write and its commit, or a sync's rebase, hold it exclusive, and a
reader that needs one whole tree (serve's rebuild) holds it shared. A commit
never stages another process's half-written files.

## Root

| Path | Content |
|---|---|
| `casebook.toml` | `format_version = 2`. A casebook binary refuses a repo with a newer version, and upgrades an older one in place with one commit (`upgrade casebook repo to format 2`). |
| `policy.toml` | Attention thresholds in days: `outgoing_pr_stale_days` (14), `incoming_no_reply_days` (7), `unpushed_days` (3), `repo_dormant_days` (365). Unknown keys are errors. |
| `README.md`, `CONTRIBUTIONS.md`, `MACHINES.md` | Rendered views. On a sync conflict the upstream copy wins and the next sync re-renders. |
| `.gitignore` | Holds `.*.tmp`: the temp files casebook writes beside a file before renaming it over (`.<name>-*.tmp`) are never committed. `casebook init` writes it; a binary opening a repo without the line appends it with one commit (`ignore casebook's temp files`), keeping any other lines. On a sync conflict the upstream copy wins and the next open adds the line again if it went. |

## Decisions: `items/`

One TOML file per item. Keys, and the file each key lives in:

| Kind | Key | File |
|---|---|---|
| repo | `repo:<owner>/<name>` | `items/repo/<owner>/<name>.toml` |
| pr | `pr:<owner>/<name>#<n>` | `items/pr/<owner>/<name>/<n>.toml` |
| issue | `issue:<owner>/<name>#<n>` | `items/issue/<owner>/<name>/<n>.toml` |
| branch | `branch:<owner>/<name>@<branch>` | `items/branch/<owner>/<name>/<url.PathEscape(branch)>.toml` |
| worktree | `worktree:<machine>:<abs path>` | `items/worktree/<machine>/<url.PathEscape(path)>.toml` |

Owner and name are lower-case. Branch names and paths keep their case.

```toml
disposition = "watch"                                  # keep archive close delete merge wait watch ignore
note        = "retire the fork when this lands"        # optional
until       = "merged(pr:elidickinson/pi-claude-bridge#97)"  # optional; required for wait and watch
decided_by  = "court"                                  # user login, claude:<session>, pi:<session>
decided_at  = 2026-09-24T10:12:00Z                     # UTC, second precision
proposed_by = "pi:<session>"                           # format 2, optional: who proposed it (agent session or rule:<id>)
rule        = "<rule id>"                              # format 2, optional: the standing rule that proposed it

[conflict]                                             # only after a decision race
disposition = "archive"
decided_by  = "court"
decided_at  = 2026-09-24T10:11:00Z
```

Allowed dispositions per kind:
- repo: keep archive delete wait watch ignore
- pr: keep close merge wait watch ignore
- issue: keep close wait watch ignore
- branch: keep delete wait watch ignore
- worktree: keep delete wait ignore

`until` conditions:
- `date(YYYY-MM-DD)`
- `merged(<pr key>)`
- `closed(<pr or issue key>)`
- `inactive(<n>h|<n>d|<n>w)`
- `released(<repo key>)`: a release published after `decided_at`

**Races.** When two machines decide the same item between syncs, the later `decided_at` wins. The loser is kept under `[conflict]`, and the item's status is `conflict` until someone decides again.

## Journal: `journal/<machine>/<YYYY>/<MM-DD>.jsonl`

One JSON object per line. Each machine writes only its own directory. Fields:
- `v` (1), `ts`, `src` (`git-hook`, `claude` or `pi`)
- `hook`, `args` and `stdin` (whitespace-split lines, at most 200; `truncated` true when stdin was cut at 200 lines) for git hooks
- `cwd`, `git_dir`
- `claude_id`, `agent_id` (raw harness session ids), `child` (true when the process belongs to the agent session in `agent_id`)
- `actions` (`tool`, `verb`, `dir`, `repo`, `number`, `refs`, `flags`)
- `exit_code`, `repo`, `machine`

Never stored: git activity in a temp folder. An event is not journalled when
its git dir is under a temp root, a git hook's working directory is, or every
one of an agent command's actions ran in one. The temp roots are
`os.TempDir()`, `$TMPDIR`, `/tmp`, `/private/tmp`, `/var/folders` and
`/private/var/folders`, compared by whole path segments with symlinks
resolved; a path inside one of the machine's configured scan roots is never
temp. A temp folder doing real work is journalled, whatever `repo` its line
carries:
- its git dir, or a linked worktree's common dir, resolves inside a configured
  scan root (a worktree of a tracked clone placed in `/tmp`); with no git dir
  recorded, a working tree that still exists is read for its `.git`;
- it pushes to a remote that isn't local: a pre-push hook's remote URL, an
  agent's `git push` to a URL or to a remote the clone's config names, or,
  once that clone is gone, a push action sync annotated with a repo (sync
  takes an action's repo only from the machine's snapshot). Local is a path or
  a `file://` URL;
- a gh action naming its repo with `-R` is about that repo wherever it ran.

`casebook prune --temp --apply` removes such events journalled before this
rule from the machine's own day files (and temp clones from its snapshot) in
one commit, keeping every other line byte for byte; history keeps the removed
lines. Its dry run counts the removed lines that carry a repo, by repo, so a
real line misclassified as temp shows before anyone applies.

Never stored: command lines, commit or tag messages, titles, bodies, field values, header values, query strings, or URL credentials.

## Snapshots: `machines/<machine>.json`

This machine's clones under its configured roots. Each clone has:
- path, identifying GitHub repo, remotes (non-GitHub URLs redacted)
- bare, dirty, stash count, local `core.hooksPath`, adopted (casebook's shims chained in)
- branches: upstream, ahead, unpushed count, oldest unpushed commit time, tip, tip_at (committer date), remote_tip (the tip SHA of the upstream remote branch at snapshot time; empty when no upstream is set or the remote branch is absent), landed_state ("yes" | "no" | "unknown" | "" = never checked), landed ("in <default>" or "merged #<n>", set only when landed_state=="yes"), landed_tip (tip SHA, set only when landed_state=="yes"), landed_how ("default-branch" or "merged-pr", set only when landed_state=="yes")
- linked worktrees: path, branch, head, detached, dirty

The file is deterministic and holds no timestamps of its own, so an unchanged machine produces no commit.

## Standing rules: `rules/`

One TOML file per rule at `rules/<id>.toml`. The `id` must match `^[a-z0-9-]+$`.

```toml
id = "landed-branches"
name = "Landed branches → delete"
status = "draft"                      # draft | active
created_by = "court"                  # user login, or "pi:<session>"
created_at = 2026-09-27T10:00:00Z
edited_at = 2026-09-27T10:00:00Z     # last edit of the conditions or the proposal

[[match]]                             # all conditions must hold
field = "kind"
op = "is"
value = "branch"
[[match]]
field = "landed"
op = "is"
value = "all-machines"
[[match]]
field = "worktree"
op = "is-not"
value = "dirty"

[propose]
disposition = "delete"
until = ""
note = "landed ({how}); restore tip {tip}"

[[exclude]]
key = "branch:schuettc/galley@feat/headcount-licensing"
reason = "keep for reference"
by = "court"
at = 2026-09-27T10:05:00Z
```

`[propose]` must be a decision valid for every kind of item the rule can match, as its `kind` conditions say (with none, every kind: only `keep`, `wait` and `ignore`): a `disposition` that kind allows (§Decisions), and an `until` that parses (`wait` and `watch` need one). A rule that breaks this is not valid: it can't be activated, and an active one is skipped with a notice.

`status` is `draft` (no effect) or `active` (proposes matches after every sync). `edited_at` is updated on every edit of `[[match]]` or `[propose]`; §4.2 uses it to decide whether to re-propose items that were previously rejected.

Commit subjects: `rule <id> → active by <who>`, `rule <id> edited by <who>`, `rule <id> deactivated by <who>`.

### Field vocabulary

The `field` in each `[[match]]` condition must be one of the following. Operators, by field type: text and enum fields take `is`, `is-not`, `in`, `not-in` (and `title` also `matches`, a regex); bool fields `is`, `is-not`; count fields `is`, `is-not`, `gt`, `gte`, `lt`, `lte`; duration fields `older-than`, `newer-than`. `GET /api/rules/vocabulary` lists each field's operators. Duration values use `<n>h`, `<n>d` or `<n>w`; bool values are `true` or `false`; count values are whole numbers, 0 or more. Anything else is a validation error.

| Field | Type | Allowed values / notes |
|---|---|---|
| `kind` | enum | `repo` \| `pr` \| `issue` \| `branch` \| `worktree` |
| `repo` | text | `owner/name` |
| `owner` | text | GitHub login |
| `relation` | enum | `outgoing` \| `incoming` \| `own` |
| `status` | enum | `new` \| `to-apply` \| `waiting` \| `due` \| `done` \| `drift` \| `conflict` |
| `direction` | enum | `outgoing` \| `incoming` \| `own` |
| `author` | text | GitHub login |
| `bot` | bool | `true` \| `false` |
| `title` | text | supports `matches` (regex) |
| `label` | text | label name |
| `age` | duration | time since created; use `older-than`/`newer-than` |
| `pushed` | duration | time since last push |
| `updated` | duration | time since last update |
| `landed` | enum | `all-machines` \| `some-machines` \| `none` \| `unknown` |
| `landed-how` | text | `default-branch` \| `merged-pr`; multi-valued (`is` = contains; `in`/`not-in` check any element) |
| `gone-upstream` | bool | branch merged and remote ref gone |
| `unpushed` | bool | has local-only commits |
| `dirty` | bool | has uncommitted changes |
| `worktree` | enum | `dirty` \| `clean` \| `none` |
| `archived` | bool | repo is archived on GitHub |
| `fork` | bool | repo is a fork |
| `open-prs` | count | number of open PRs; use `is`, `is-not`, `gt`, `gte`, `lt`, `lte` with a whole number, 0 or more |
| `open-issues` | count | number of open issues; same operators and values as `open-prs` |
| `has-decision` | bool | item has a recorded decision |
| `policy-hit` | text | policy rule name, e.g. `outgoing-stale` |

## Restore records: `restores/`

One TSV file per UTC calendar date at `restores/<YYYY-MM-DD>.tsv`. The file is appended (not replaced) as destructive steps run; one line is written immediately before each destructive step executes, so the record is always present if the step ran.

**Header line** (written once, on first creation of the file for that date):

```
key	action	before	restore-command
```

**Fields** (TAB-separated, one per line):

| Field | Content |
|---|---|
| `key` | Item key (e.g. `branch:schuettc/hail@feat/x`) |
| `action` | Apply action (e.g. `branch-delete-local`) |
| `before` | Live value overwritten by the step (branch tip SHA, etc.) |
| `restore-command` | Single shell command that reverses the step (e.g. `git -C '/path' branch 'feat/x' <tip>`) |

**Commit subject**: `restore record for <key> (<action>)` — one commit per appended line.

**Constraints**: tabs and newlines are refused in any field value; a step whose key or command contains a tab or newline fails before the precondition check.

## Not in the repository

The GitHub observation cache (`~/.local/state/casebook/github.json`), the drift record (`seen.json`), the event spool and the hook shims are machine-local.

So is `casebook serve`'s working state, `~/.local/state/casebook/casebook.db` (SQLite, `0600`): agent sessions, threads, messages and their deliveries, pending and settled proposals, evidence, progress and the live event log. It records collaboration, never intent: a proposal becomes intent only when Court accepts it, as a decision file with `proposed_by`.

## Version history

- **1** (casebook 0.1.x): decisions, journal, snapshots, views.
- **2** (casebook 0.2.0): decisions gain the optional `proposed_by` and `rule` fields. Standing rules under `rules/` and restore records under `restores/` are new directories (no format bump; older binaries ignore them). Nothing else changes; the upgrade rewrites only `casebook.toml`.
