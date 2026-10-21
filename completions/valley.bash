# shellcheck shell=bash
# bash completion for valley. The verb list mirrors the case statement in
# bin/valley — update both together.
#
# NEVER fetch from here: completion runs on every <TAB> and must be instant.
# Candidates come from the remote-tracking refs of the last fetch; valley's
# own verbs fetch before acting, so stale candidates cost nothing.

_valley_branches() {
  # Cached topic branches on origin, with the origin/ prefix stripped.
  # Review, approve, rebase and ready filter out merged branches; status needs
  # those too. Silent outside a git repo — no errors mid-typing.
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
    mapfile -t COMPREPLY < <(compgen -W 'pending changes review approve rebase status ready unready withdraw knot plan checks tail replay self-update help' -- "$cur")
  elif [ "${COMP_WORDS[1]}" = checks ] && [ "${COMP_WORDS[COMP_CWORD - 1]}" = --cost ]; then
    mapfile -t COMPREPLY < <(compgen -W 'quick slow' -- "$cur")
  elif [ "${COMP_WORDS[1]}" = checks ] && [ "${cur:0:1}" = - ]; then
    mapfile -t COMPREPLY < <(compgen -W '--schema --instance --project --names --cost' -- "$cur")
  elif [ "${COMP_WORDS[1]}" = approve ] && [ "${cur:0:1}" = - ]; then
    mapfile -t COMPREPLY < <(compgen -W '--head' -- "$cur")
  elif [ "${COMP_WORDS[1]}" = approve ] && [ "${COMP_WORDS[COMP_CWORD - 1]}" = --head ]; then
    return
  elif [ "${COMP_WORDS[1]}" = approve ]; then
    mapfile -t COMPREPLY < <(compgen -W "$(_valley_branches --no-merged=origin/main)" -- "$cur")
  elif [ "${COMP_WORDS[1]}" = review ] && [ "${cur:0:1}" = - ]; then
    mapfile -t COMPREPLY < <(compgen -W '-v' -- "$cur")
  elif [ "$COMP_CWORD" -eq 2 ] && [[ "${COMP_WORDS[1]}" =~ ^(review|rebase)$ ]]; then
    mapfile -t COMPREPLY < <(compgen -W "$(_valley_branches --no-merged=origin/main)" -- "$cur")
  elif [ "${COMP_WORDS[1]}" = pending ] && [ "${cur:0:1}" = - ]; then
    mapfile -t COMPREPLY < <(compgen -W '--all --repo --checks --json -v' -- "$cur")
  elif [ "${COMP_WORDS[1]}" = changes ] && [ "${cur:0:1}" = - ]; then
    mapfile -t COMPREPLY < <(compgen -W '--json --all --repo -v' -- "$cur")
  elif [ "${COMP_WORDS[1]}" = status ] && [ "${cur:0:1}" = - ]; then
    mapfile -t COMPREPLY < <(compgen -W '--json -v' -- "$cur")
  elif [ "${COMP_WORDS[1]}" = status ]; then
    mapfile -t COMPREPLY < <(compgen -W "$(_valley_branches)" -- "$cur")
  elif [ "${COMP_WORDS[1]}" = ready ] && [ "${cur:0:1}" = - ]; then
    mapfile -t COMPREPLY < <(compgen -W '--head --change --if-active --stamp' -- "$cur")
  elif [ "${COMP_WORDS[1]}" = ready ] && [[ ! "${COMP_WORDS[COMP_CWORD - 1]}" =~ ^--(head|change|if-active)$ ]]; then
    mapfile -t COMPREPLY < <(compgen -W "$(_valley_branches --no-merged=origin/main)" -- "$cur")
  elif [ "$COMP_CWORD" -eq 2 ] && [ "${COMP_WORDS[1]}" = knot ]; then
    mapfile -t COMPREPLY < <(compgen -W 'new show list set link unlink edit comment claim unclaim schema' -- "$cur")
  elif [ "${COMP_WORDS[1]}" = knot ] && { [ "$COMP_CWORD" -eq 3 ] && [ "${COMP_WORDS[2]}" = new ] || [ "${COMP_WORDS[COMP_CWORD - 1]}" = --type ]; }; then
    mapfile -t COMPREPLY < <(compgen -W 'outcome bug idea question' -- "$cur")
  elif [ "${COMP_WORDS[1]}" = knot ] && [ "${cur:0:1}" = - ]; then
    mapfile -t COMPREPLY < <(compgen -W '--project --json --parent --blocked-by --owner --target --body --type --status --ready --if --force --change -m --run' -- "$cur")
  elif [ "${COMP_WORDS[1]}" = plan ] && [ "${cur:0:1}" = - ]; then
    mapfile -t COMPREPLY < <(compgen -W '--project --all --owner --json' -- "$cur")
  elif [ "$COMP_CWORD" -eq 2 ] && [ "${COMP_WORDS[1]}" = replay ]; then
    mapfile -t COMPREPLY < <(compgen -d -- "$cur")
  elif [ "${COMP_WORDS[1]}" = self-update ] && [ "${cur:0:1}" = - ]; then
    mapfile -t COMPREPLY < <(compgen -W '--verbose --retry --rollback --pin --unpin --repo --branch' -- "$cur")
  elif [ "$COMP_CWORD" -eq 2 ] && [ "${COMP_WORDS[1]}" = help ]; then
    mapfile -t COMPREPLY < <(compgen -W 'pending changes review approve rebase status ready unready withdraw knot plan checks tail replay self-update' -- "$cur")
  fi
}

complete -F _valley valley
