# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
# The flags are one string so the formatter app and this check cannot
# drift; split it back into argv.
read -ra fmtArgs <<< "$proseFmtArgs"
cd "$tree" || exit 1
find . -name '*.md' -print0 | xargs -0 --no-run-if-empty \
  prettier "${fmtArgs[@]}" --check
touch "$out"
