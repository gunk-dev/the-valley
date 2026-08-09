# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
cue vet -c "$schema" "$registry"/*.cue
cue export "$schema" "$registry"/*.cue > registry.json
grep -q '"genesis": "patrick"' registry.json
# The signing name is per key, not per entry: one entry holds keys signing
# under two different names, and that is what the compiler renders from.
grep -q '"signs": "makers-mark/attestations"' registry.json
grep -q '"signs": "patrick"' registry.json

# The registries the schema must reject, and the words each rejection must
# name. The floor's properties are here — mandatory expiry where a raw key
# is held, no external principal governing the registry, a genesis entry
# that governs it — and a rejection that stopped naming its field would be a
# floor nobody can act on.
while IFS=$'\t' read -r name names; do
  case "$name" in '' | '#'*) continue ;; esac
  if cue vet -c "$schema" "$rejected/$name.cue" > "$name.err" 2>&1; then
    echo "identity-schema: expected invalid registry '$name' to be rejected" >&2
    exit 1
  fi
  if [ -n "$names" ] && ! grep -qF -- "$names" "$name.err"; then
    echo "identity-schema: '$name' was rejected without naming $names" >&2
    cat "$name.err" >&2
    exit 1
  fi
done < "$rejectedCases"
touch "$out"
