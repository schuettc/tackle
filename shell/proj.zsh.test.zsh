#!/usr/bin/env zsh
# Tests for shell/proj.zsh.
#
# Floor: `zsh -n` must parse proj.zsh without error. Bonus: a shim-eval smoke
# that stubs the `proj` binary to write a known line to $PROJ_EXEC and asserts
# the shim would eval it (with a no-op eval override so the test stays inert).

emulate -L zsh
set -u

here="${0:A:h}"
target="$here/proj.zsh"
fail=0

# --- Floor: syntax check -----------------------------------------------------
if zsh -n "$target"; then
  print -r -- "ok: zsh -n proj.zsh"
else
  print -r -- "FAIL: zsh -n proj.zsh" >&2
  fail=1
fi

# --- Smoke: shim evals the $PROJ_EXEC line -----------------------------------
# Run in a subshell so stubs/overrides don't leak. The stub `proj` writes a
# sentinel to $PROJ_EXEC; `command` is stubbed so `command proj` hits it and the
# on-PATH check passes; `eval` is overridden to capture rather than execute.
smoke() {
  # Suppress the source-time auto-join hook: pretend we're already in tmux.
  TMUX=fake NO_AUTO_TMUX=1 source "$target"

  local captured=""
  captured_file="$(mktemp "${TMPDIR:-/tmp}/proj-smoke.XXXXXX")"

  # Stub `command`: `command -v proj` succeeds; `command proj ...` writes the
  # sentinel to $PROJ_EXEC (the binary's contract).
  command() {
    if [[ "$1" == "-v" ]]; then
      return 0
    fi
    if [[ "$1" == "proj" ]]; then
      print -r -- 'SENTINEL_ATTACH' >| "$PROJ_EXEC"
      return 0
    fi
    builtin command "$@"
  }

  # Override eval to capture the string the shim would have executed.
  eval() { print -r -- "$*" >| "$captured_file"; }

  proj somearg
  captured="$(<"$captured_file")"
  rm -f "$captured_file"

  [[ "$captured" == "SENTINEL_ATTACH" ]]
}

if ( smoke ); then
  print -r -- "ok: shim evals \$PROJ_EXEC line"
else
  print -r -- "FAIL: shim did not eval \$PROJ_EXEC line" >&2
  fail=1
fi

exit $fail
