# shellcheck shell=bash
# bash completion for valley. The verb list mirrors the case statement in
# bin/valley — update both together.
#
# NEVER fetch from here: completion runs on every <TAB> and must be instant.
# Candidates come from the remote-tracking refs of the last fetch; valley's
# own verbs fetch before acting, so stale candidates cost nothing.

_valley_review_branches() {
  # Branches on origin not merged into origin/main, with the origin/ prefix
  # stripped and origin/HEAD + origin/main excluded. Silent outside a git
  # repo — no errors mid-typing.
  local ref
  while IFS= read -r ref; do
    case "$ref" in
      '' | origin/HEAD | origin/main) continue ;;
    esac
    printf '%s\n' "${ref#origin/}"
  done < <(git for-each-ref refs/remotes/origin \
    --format='%(refname:short)' --no-merged=origin/main 2>/dev/null)
}

_valley() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=()
  if [ "$COMP_CWORD" -eq 1 ]; then
    mapfile -t COMPREPLY < <(compgen -W 'pending review tail replay help' -- "$cur")
  elif [ "$COMP_CWORD" -eq 2 ] && [ "${COMP_WORDS[1]}" = review ]; then
    mapfile -t COMPREPLY < <(compgen -W "$(_valley_review_branches)" -- "$cur")
  elif [ "$COMP_CWORD" -eq 2 ] && [ "${COMP_WORDS[1]}" = replay ]; then
    mapfile -t COMPREPLY < <(compgen -d -- "$cur")
  fi
}

complete -F _valley valley
