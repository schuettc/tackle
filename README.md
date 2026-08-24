# scratch

A fast, keyboard-first TUI for a markdown scratchpad scoped to your coding-agent
session. Autosaves, reloads external edits non-destructively, and stays out of
your way.

## Where the pad lives

Pads are stored per user, outside any repository, and keyed by the **agent
session** when there is one:

```
<store>/pads/<key>.md

key    = the agent session id (Claude Code / Codex / Cursor), else
         the tmux session name, else a flattened working directory
store  = macOS    ~/Library/Application Support/scratch
         Linux    $XDG_CONFIG_HOME/scratch, else ~/.config/scratch
         Windows  %AppData%\scratch
```

Keying on the session rather than the directory means a pad follows the
conversation: resume a session in a different pane, tab, or worktree and you
get your notes back, while two sessions open in one checkout keep separate
pads instead of overwriting each other. Nothing is ever written into your
working tree.

`scratch` finds the session id from `$CLAUDE_CODE_SESSION_ID`, or — for a TUI
running in a sibling tmux pane, which does not inherit the agent's environment
— from the `@harness_session` tmux option, which a harness `SessionStart` hook
is expected to stamp. With no agent at all, a pane inside tmux keys on its
tmux session name (`#S`), so two shell-only sessions in one checkout still keep
separate pads; only outside tmux does the directory decide.

Two overrides, both absolute: `$SCRATCH_FILE` pins the exact file and skips all
derivation; `$SCRATCH_DIR` relocates the store.

## Install

```bash
go install github.com/schuettc/scratch@latest
```

## Usage

```bash
scratch            # open the TUI editor on this session's pad
scratch print      # print the pad to stdout
scratch append "…" # atomically append a line (for hooks/scripts)
scratch path       # print the resolved pad path
```

Keys: type to edit · `ctrl+s` save · `ctrl+r` reload from disk · `ctrl+x` clear (asks `y/n`) · `ctrl+q`/`esc` quit.
Autosave runs ~500ms after you stop typing, on quit, and on `ctrl+s`.

## Manual smoke checklist

1. With no pad yet: run `scratch`, type, wait ~1s, quit;
   `cat "$(scratch path)"` shows your text, and the working directory is
   untouched.
2. With the TUI open and idle (clean), run `scratch append "x"` in another
   shell → the line appears live in the editor.
3. Type locally (don't save), then `scratch append "y"` elsewhere → header
   shows `● changed on disk`; your edits are intact; `ctrl+r` loads the disk
   version.
4. Quit with unsaved edits (`ctrl+q`) → they're flushed to disk.
