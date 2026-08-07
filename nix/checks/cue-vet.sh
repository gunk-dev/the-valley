# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
cue vet -c "$schema" "$example"
cue export "$schema" "$example" > example.json
grep -q 'gunk-dev/the-valley' example.json
grep -q 'restic-sftp' example.json

# A declaration with no optional field set must stay valid —
# consumers written before backup or protection existed keep
# vetting.
cue vet -c "$schema" "$hosts/no-backup.cue"

# What the installer renders a hook from. A project that
# leaves the set out gets the default, a project that names
# patterns gets them in order, and a project that declares no
# protection exports none at all — which is how the installer
# tells a protected project from an open one.
protected=("$schema" "$hosts/protected.cue")
[ "$(cue export -e 'projects.guarded.protection.refs[0]' "${protected[@]}")" \
  = '"refs/heads/main"' ]
cue export -e 'projects.released.protection.refs' "${protected[@]}" > released.json
grep -q '"refs/heads/release/\*"' released.json
if cue export -e 'projects.open.protection' "${protected[@]}" > open.err 2>&1; then
  echo "cue-vet: a project declaring no protection exported one" >&2
  exit 1
fi

# The event schema accepts what the publisher hook emits …
cue vet -d '#RefUpdated' "$eventSchema" "$events/ref-updated.json"
# … and stays closed: a field outside the git-derivable
# identity — wall-clock time, a hostname — must be rejected,
# or replay determinism silently dies. An abbreviated object
# id is rejected for the same reason.
for bad in "$events"/rejected/*.json; do
  if cue vet -d '#RefUpdated' "$eventSchema" "$bad"; then
    echo "cue-vet: expected $(basename "$bad") to be rejected" >&2
    exit 1
  fi
done

# The rejected declarations, and the words each rejection must name.
while IFS=$'\t' read -r name names; do
  case "$name" in ''|'#'*) continue ;; esac
  if cue vet -c "$schema" "$hosts/rejected/$name.cue" > "$name.err" 2>&1; then
    echo "cue-vet: expected invalid declaration '$name' to be rejected" >&2
    exit 1
  fi
  if [ -n "$names" ] && ! grep -qF -- "$names" "$name.err"; then
    echo "cue-vet: '$name' was rejected without naming $names" >&2
    cat "$name.err" >&2
    exit 1
  fi
done < "$rejectedCases"
touch "$out"
