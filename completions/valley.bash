# shellcheck shell=bash
# bash completion for valley. The verb list mirrors the case statement in
# bin/valley — update both together.
#
# NEVER fetch from here: completion runs on every <TAB> and must be instant.
# Candidates come from the remote-tracking refs of the last fetch; valley's
# own verbs fetch before acting, so stale candidates cost nothing.

_valley_branches() {
  # Cached topic branches on origin, with the origin/ prefix stripped.
  # Review filters out merged branches; status needs those too. Silent
  # outside a git repo — no errors mid-typing.
  local ref
  while IFS= read -r ref; do
    case "$ref" in
      '' | refs/remotes/origin/HEAD | refs/remotes/origin/main) continue ;;
    esac
    printf '%s\n' "${ref#refs/remotes/origin/}"
  done < <(git for-each-ref refs/remotes/origin \
    --format='%(refname)' "$@" 2>/dev/null)
}

_valley() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=()
  if [ "$COMP_CWORD" -eq 1 ]; then
    mapfile -t COMPREPLY < <(compgen -W 'pending review status checks tail replay help' -- "$cur")
  elif [ "${COMP_WORDS[1]}" = checks ] && [ "${cur:0:1}" = - ]; then
    mapfile -t COMPREPLY < <(compgen -W '--schema --instance --project' -- "$cur")
  elif [ "$COMP_CWORD" -eq 2 ] && [ "${COMP_WORDS[1]}" = review ]; then
    mapfile -t COMPREPLY < <(compgen -W "$(_valley_branches --no-merged=origin/main)" -- "$cur")
  elif [ "$COMP_CWORD" -eq 2 ] && [ "${COMP_WORDS[1]}" = status ]; then
    mapfile -t COMPREPLY < <(compgen -W "$(_valley_branches)" -- "$cur")
  elif [ "$COMP_CWORD" -eq 2 ] && [ "${COMP_WORDS[1]}" = replay ]; then
    mapfile -t COMPREPLY < <(compgen -d -- "$cur")
  fi
}

complete -F _valley valley
