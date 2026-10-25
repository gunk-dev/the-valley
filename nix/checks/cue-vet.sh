# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
cue vet -c "$schema" "$example"
cue export "$schema" "$example" > example.json
grep -q 'mirror.example.org:the-valley' example.json
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

# The norm: protection that names no writer. It exports an
# empty list rather than nothing, so the installer renders a
# hook whose exception list is empty rather than missing.
[ "$(cue export -e 'projects.sealed.protection.writers' "${protected[@]}")" = '[]' ]

# The same for grants: none by default, so the hook reads a
# project that opens nothing as exactly that, whether or not it
# declares protection. A grant exports its patterns and its
# writers as declared.
[ "$(cue export -e 'projects.sealed.grants' "${protected[@]}")" = '{}' ]
[ "$(cue export -e 'projects.open.grants' "${protected[@]}")" = '{}' ]
cue export -e 'projects.released.grants' "${protected[@]}" > grants.json
grep -q '"refs/tags/release/\*"' grants.json

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

# The worked policy example: its instance floor and templates composed with
# its project layer, as schema/verification.cue's header shows. The
# deriver's own fixtures are policy-deriver's.
cue vet -c "$verificationSchema" "$policies"/instance/*.cue \
  "$policies"/project/*.cue

# The carry modes, case by case. Each fixture under examples/policy/carry/
# composes a floor, a template and a project in one document, or refines
# `policy` itself. Each must vet, and each class must resolve to the mode
# the layers mean: the strictest mode whose key is true, and same-paths when
# none is.
carry="$policies/carry"
while read -r fixture class mode; do
  cue vet -c "$verificationSchema" "$carry/$fixture.cue"
  got="$(cue eval --out text -e "_mode.$class" "$verificationSchema" "$carry/$fixture.cue")"
  [ "$got" = "$mode" ] || {
    echo "cue-vet: carry/$fixture.cue resolves $class to '$got', not $mode" >&2
    exit 1
  }
done <<'EOF'
floor-pins-exact guarded exact
template-default-declined guarded same-paths
template-default-declined kept same-class
no-carry guarded same-paths
project-adds-exact guarded exact
policy-refines-carry guarded exact
policy-refines-carry added same-class
EOF

# The restacker's cap is 64 unless a layer sets it.
[ "$(cue export -e policy.restack.maxAutomaticVersions "$verificationSchema" \
  "$carry/no-carry.cue")" = 64 ]
[ "$(cue export -e policy.restack.maxAutomaticVersions "$verificationSchema" \
  "$carry/project-adds-exact.cue")" = 16 ]

# The compositions the schema must reject, and the field each rejection must
# name: a project dropping the floor's mode, a carry key that is not a
# mode, a cap below 1, a cap outside every layer, and a pattern that would
# forge a line of the policy `valley checks` renders.
while read -r fixture names; do
  if cue vet -c "$verificationSchema" "$carry/rejected/$fixture.cue" \
    > "$fixture.err" 2>&1; then
    echo "cue-vet: expected carry/rejected/$fixture.cue to be rejected" >&2
    exit 1
  fi
  grep -qF -- "$names" "$fixture.err" || {
    echo "cue-vet: carry/rejected/$fixture.cue was rejected without naming $names" >&2
    cat "$fixture.err" >&2
    exit 1
  }
done <<'EOF'
project-drops-floor-exact policy.classes.guarded.carry.exact
not-a-mode policy.classes.guarded.carry."same-file"
cap-below-one policy.restack.maxAutomaticVersions
restack-outside-project restack: conflicting values "INVALID: unknown top-level field
forged-carry-row policy.classes.injected.paths."unused\ncarry\tguarded\tsame-paths": field not allowed
EOF

# The hardware requirement, case by case. Each fixture under
# examples/policy/approval/ composes the three layers in one document. Each
# must vet, and the composed policy must say what the layers mean: true when
# the floor or the project requires a hardware key, false when a project
# turned a template's default off, and no `approval` field at all when no
# layer wrote one.
approval="$policies/approval"
while read -r fixture hardware; do
  cue vet -c "$verificationSchema" "$approval/$fixture.cue"
  cue export --out json -e policy "$verificationSchema" "$approval/$fixture.cue" \
    > "$fixture.json"
  if [ "$hardware" = absent ]; then
    if grep -q '"approval"' "$fixture.json"; then
      echo "cue-vet: approval/$fixture.cue exports an approval field no layer wrote" >&2
      exit 1
    fi
    continue
  fi
  got="$(cue export -e policy.approval.hardware "$verificationSchema" "$approval/$fixture.cue")"
  [ "$got" = "$hardware" ] || {
    echo "cue-vet: approval/$fixture.cue composes approval.hardware to '$got', not $hardware" >&2
    exit 1
  }
done <<'EOF'
floor-requires-hardware true
project-requires-hardware true
template-default-declined false
no-requirement absent
EOF

# The hardware requirements the schema must reject, and the field each
# rejection must name: a project dropping the floor's requirement, a field
# `approval` does not have, and a requirement that is not a boolean.
while read -r fixture names; do
  if cue vet -c "$verificationSchema" "$approval/rejected/$fixture.cue" \
    > "$fixture.err" 2>&1; then
    echo "cue-vet: expected approval/rejected/$fixture.cue to be rejected" >&2
    exit 1
  fi
  grep -qF -- "$names" "$fixture.err" || {
    echo "cue-vet: approval/rejected/$fixture.cue was rejected without naming $names" >&2
    cat "$fixture.err" >&2
    exit 1
  }
done <<'EOF'
project-drops-floor-hardware policy.approval.hardware: conflicting values false and true
not-a-requirement policy.approval.touch: field not allowed
not-a-bool policy.approval.hardware: conflicting values "yes" and bool
EOF

# No control character reaches a string `valley checks` renders as a field
# of a line: a path pattern, a class name, a check name in the catalogue,
# in a class's requirements or in the unclassified set, an attribute, or a
# command line. Each case is written once with a dash where the character
# goes, and must vet, so that its failure with the character is the
# character's doing. The last case refines `policy` itself rather than a
# layer.
while read -r field doc; do
  printf 'package verification\n%s\n' "${doc//@/-}" > control.cue
  cue vet -c "$verificationSchema" control.cue || {
    echo "cue-vet: the $field case does not vet without a control character, so it proves nothing" >&2
    exit 1
  }
  for char in '\n' '\r' '\t' '\u0000' '\u001b' '\u007f'; do
    printf 'package verification\n%s\n' "${doc//@/"$char"}" > control.cue
    if cue vet -c "$verificationSchema" control.cue > control.err 2>&1; then
      echo "cue-vet: a $field holding $char vetted" >&2
      exit 1
    fi
  done
done <<'EOF'
pattern project: classes: c: paths: "a@b": true
class project: classes: "a@b": paths: "a": true
check project: checks: "a@b": {runner: "nix", attribute: "a"}
requirement project: {checks: "a-b": {runner: "nix", attribute: "a"}, classes: c: {paths: "a": true, requires: "a@b": true}}
unclassified project: {checks: "a-b": {runner: "nix", attribute: "a"}, unclassified: "a@b": true}
attribute project: checks: c: {runner: "nix", attribute: "a@b"}
command project: checks: c: {runner: "command", command: "a@b"}
refined-pattern policy: classes: c: paths: "a@b": true
EOF

# The web visibility declarations (schema/web.cue): the worked instance and
# project declarations, and the-valley's own, which must say public or the
# publisher keeps it private.
cue vet -c "$webSchema" "$webPolicies"/instance/*.cue
[ "$(cue export -e instance.projects.app.visibility "$webSchema" "$webPolicies"/instance/*.cue)" \
  = '"public"' ]
[ "$(cue export -e instance.projects.site.visibility "$webSchema" "$webPolicies"/instance/*.cue)" \
  = '"private"' ]
cue vet -c "$webSchema" "$webPolicies"/project/*.cue
[ "$(cue export -e project.visibility "$webSchema" "$ownWeb"/*.cue)" = '"public"' ]

# The declarations the schema must reject, and the field each rejection
# must name. A refused declaration keeps its project private, so a schema
# that stopped refusing one would make public what nobody declared.
while read -r fixture names; do
  if cue vet -c "$webSchema" "$webPolicies/rejected/$fixture.cue" > "$fixture.err" 2>&1; then
    echo "cue-vet: expected web/rejected/$fixture.cue to be rejected" >&2
    exit 1
  fi
  grep -qF -- "$names" "$fixture.err" || {
    echo "cue-vet: web/rejected/$fixture.cue was rejected without naming $names" >&2
    cat "$fixture.err" >&2
    exit 1
  }
done <<'EOF'
top-level-visibility visibility: conflicting values "INVALID: unknown top-level field
unknown-visibility project.visibility
unknown-project-field project.shares: field not allowed
unsafe-project-name instance.projects."../app": field not allowed
instance-list instance.public: field not allowed
EOF
[ "$(find "$webPolicies/rejected" -name '*.cue' | wc -l)" = 5 ] || {
  echo "cue-vet: web/rejected/ holds a case the list above does not name" >&2
  exit 1
}
touch "$out"
