# shellcheck shell=bash
# A stand-in for one tool, linked first on PATH under that tool's name. It
# passes every call through to the real tool, except that it can fail one
# call on purpose, so a check can fail each call a program makes in turn
# and watch what the program does with the failure.
#
#   SHIM_REAL_PATH   the PATH to find the real tool on
#   SHIM_TOOL        the tool whose calls are counted; others pass through
#   SHIM_COUNT       a file holding the count of that tool's calls so far
#   SHIM_FAIL_AT     the call to fail, counting from 1; 0 fails none
#   SHIM_STATUS      the status the failed call exits with (default 2)
#   SHIM_TRUNCATE    when set to N, `git diff` writes its first N
#                    NUL-terminated fields and then one byte of the next,
#                    with no NUL after it, as a diff cut short would
tool="${0##*/}"
real="$(PATH="$SHIM_REAL_PATH" command -v "$tool")" || {
  echo "fail-nth: no real $tool on $SHIM_REAL_PATH" >&2
  exit 127
}
if [ "${SHIM_TOOL:-}" = "$tool" ]; then
  n=$(($(cat "$SHIM_COUNT") + 1))
  echo "$n" > "$SHIM_COUNT"
  if [ "$n" -eq "${SHIM_FAIL_AT:-0}" ]; then
    echo "$tool: injected failure of call $n" >&2
    exit "${SHIM_STATUS:-2}"
  fi
fi
if [ -n "${SHIM_TRUNCATE:-}" ] && [ "$tool" = git ] && [ "${1:-}" = diff ]; then
  PATH="$SHIM_REAL_PATH"
  whole="$(mktemp)"
  "$real" "$@" > "$whole" || exit
  head -z -n "$SHIM_TRUNCATE" "$whole"
  tail -z -n +"$((SHIM_TRUNCATE + 1))" "$whole" | head -c 1
  rm -f "$whole"
  exit 0
fi
exec "$real" "$@"
