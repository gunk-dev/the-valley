# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
while IFS=$'\t' read -r case names; do
  case "$case" in ''|'#'*) continue ;; esac
  mkdir -p "work/$case"
  if python3 "$lint" \
    --tree "$graphs/rejected/$case" \
    --schema "$nodeSchema" \
    --work "work/$case" 2> "$case.err"
  then
    echo "knowledge-lint: expected graph '$case' to be rejected" >&2
    exit 1
  fi
  if ! grep -qF -- "$names" "$case.err"; then
    echo "knowledge-lint: '$case' was rejected without naming $names" >&2
    cat "$case.err" >&2
    exit 1
  fi
done < "$rejectedCases"
touch "$out"
