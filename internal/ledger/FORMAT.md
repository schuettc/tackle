# ledger repository format, version 1

A ledger data repository is a private git repository written only by the
`ledger` binary. History is linear: no merge commits, no branches.

## Root

| Path | Content |
|---|---|
| `ledger.toml` | `format_version = 1`. A ledger refuses a repo with a newer version. |
| `policy.toml` | Attention thresholds in days: `outgoing_pr_stale_days` (14), `incoming_no_reply_days` (7), `unpushed_days` (3), `repo_dormant_days` (365). Unknown keys are errors. |
| `README.md`, `CONTRIBUTIONS.md`, `MACHINES.md` | Rendered views. On a sync conflict the upstream copy wins and the next sync re-renders. |

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

Never stored: command lines, commit or tag messages, titles, bodies, field values, header values, query strings, or URL credentials.

## Snapshots: `machines/<machine>.json`

This machine's clones under its configured roots. Each clone has:
- path, identifying GitHub repo, remotes (non-GitHub URLs redacted)
- bare, dirty, stash count, local `core.hooksPath`
- branches: upstream, ahead, unpushed count, oldest unpushed commit time, tip
- linked worktrees: path, branch, head, detached, dirty

The file is deterministic and holds no timestamps of its own, so an unchanged machine produces no commit.

## Not in the repository

The GitHub observation cache (`~/.local/state/ledger/github.json`), the drift record (`seen.json`), the event spool and the hook shims are machine-local.
