# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
export HOME="$TMPDIR"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name valley-check
git config --global user.email valley-check@localhost
git config --global init.defaultBranch main

# The real floor is in the owning valley's repository, which a build cannot
# fetch. The example floor stands in for it: it has the same prose class
# and unclassified rule. The docs template is composed beside it, as an
# instance's templates are, and this layer does not embed it.
policy="$TMPDIR/policy"
mkdir -p "$policy/instance" "$policy/project"
cp "$floor" "$policy/instance/floor.cue"
cp "$template" "$policy/instance/template.cue"
cp "$project"/*.cue "$policy/project/"

# The layer composes.
cue vet -c "$schema" "$policy"/instance/*.cue "$policy"/project/*.cue

# Every check the composition names is a flake check this flake defines.
# A policy naming a check nobody can build would be found only when a
# change that requires it is approved.
cat > "$TMPDIR/attributes.cue" <<'EOF'
package verification

import "strings"

_attributes: strings.Join([
	for n, c in policy.checks if c.runner == "nix" {c.attribute},
], "\n")
EOF
cue eval --out text -e _attributes "$schema" "$policy"/instance/*.cue \
  "$policy"/project/*.cue "$TMPDIR/attributes.cue" > attributes.txt
[ -s attributes.txt ] || { echo "own-policy: no nix check named" >&2; exit 1; }
missing=0
while IFS= read -r a; do
  case " $checkNames " in
    *" $a "*) ;;
    *)
      echo "own-policy: the policy names $a, which is not a flake check" >&2
      missing=1
      ;;
  esac
done < attributes.txt
[ "$missing" -eq 0 ] || exit 1

# A change to the docs requires the floor's prose-format alone, and a
# change to the hook requires the hook's tests. Classes that matched too much or nothing
# at all would break one or the other.
repo="$TMPDIR/tree"
git init --quiet "$repo"
cd "$repo" || exit 1
mkdir -p docs valleyhook nix/checks
echo seed > docs/architecture.md
echo seed > valleyhook/main.go
echo seed > nix/packages.nix
# One path in each scope the carry modes below are asserted over.
for f in integrator/main.go sigverify/keys.go identity/registry.go \
  schema/identity.cue schema/verification.cue policy/project/policy.cue \
  policy/valley examples/policy/project/policy.cue \
  examples/identity/registry/registry.cue research/notes.txt; do
  mkdir -p "$(dirname "$f")"
  echo seed > "$f"
done
for f in $checkFiles; do
  echo seed > "nix/checks/$f"
done
git add -A
git commit --quiet -m base
base="$(git rev-parse HEAD)"

required() {
  valley checks --schema "$schema" \
    --instance "$policy/instance" --project "$policy/project" \
    --names "$base" HEAD
}

git checkout --quiet -B docs "$base"
echo edit > docs/architecture.md
git commit --quiet -am docs
got="$(required)"
[ "$got" = prose-format ] || {
  echo "own-policy: a docs-only change must require prose-format alone, got:" >&2
  echo "$got" >&2
  exit 1
}

git checkout --quiet -B hook "$base"
echo edit > valleyhook/main.go
git commit --quiet -am hook
got="$(required)"
grep -qx valleyhook-unit <<<"$got" || {
  echo "own-policy: a hook-only change must require valleyhook-unit, got:" >&2
  echo "$got" >&2
  exit 1
}
if grep -qx prose-format <<<"$got"; then
  echo "own-policy: a hook-only change fell to the unclassified rule" >&2
  exit 1
fi

# A check's own files require that check. Every file under nix/checks/
# named after a flake check requires that check, and every other script
# there requires some check the nix class does not. A script that matched
# the nix class alone could be changed without its check being required. Other files that
# are not named after a check are definitions or data shared by several
# checks, which the policy may leave to the nix class.
git checkout --quiet -B shared "$base"
echo edit > nix/packages.nix
git commit --quiet -am shared
shared="$(required | LC_ALL=C sort)"
failed=0
for f in $checkFiles; do
  name="${f%.*}"
  case " $checkNames " in
    *" $name "*) named=1 ;;
    *) named=0 ;;
  esac
  [ "$named" -eq 1 ] || [ "${f##*.}" = sh ] || continue
  git checkout --quiet -B file "$base"
  echo edit > "nix/checks/$f"
  git commit --quiet -am "$f"
  got="$(required | LC_ALL=C sort)"
  if [ "$named" -eq 1 ]; then
    grep -qx -- "$name" <<<"$got" && continue
    echo "own-policy: a change to nix/checks/$f must require $name, got:" >&2
  else
    [ -n "$(LC_ALL=C comm -23 <(echo "$got") <(echo "$shared"))" ] && continue
    echo "own-policy: a change to nix/checks/$f requires no check of its own, got:" >&2
  fi
  echo "$got" >&2
  failed=1
done
[ "$failed" -eq 0 ] || exit 1

# Each scope's carry mode, as the deriver reports it for a change to one
# path in it. The hook, the integrator, the signature verifier and the
# identity compiler are same-class, the policy is exact, and everything
# else, unclassified paths included, is same-paths.
while read -r path mode; do
  git checkout --quiet -B carry "$base"
  echo edit > "$path"
  git commit --quiet -am "$path"
  got="$(valley checks --schema "$schema" \
    --instance "$policy/instance" --project "$policy/project" "$base" HEAD \
    | sed -n 's/^carry mode: //p')"
  [ "$got" = "$mode" ] && continue
  echo "own-policy: a change to $path must be $mode, got '$got'" >&2
  failed=1
done <<'EOF'
valleyhook/main.go same-class
integrator/main.go same-class
sigverify/keys.go same-class
identity/registry.go same-class
schema/identity.cue same-class
policy/project/policy.cue exact
policy/valley exact
schema/verification.cue exact
examples/policy/project/policy.cue same-paths
examples/identity/registry/registry.cue same-paths
docs/architecture.md same-paths
research/notes.txt same-paths
EOF
[ "$failed" -eq 0 ] || exit 1

# The scopes the carry classes newly classify keep every check they
# required before those classes existed. Each line is a path and the checks
# a change to it required then, composed with this same floor.
# schema/verification.cue requires more now: the integrator's checks read
# it.
while read -r path before; do
  git checkout --quiet -B classified "$base"
  echo edit > "$path"
  git commit --quiet -am "$path"
  got="$(required)"
  for check in ${before//,/ }; do
    grep -qx -- "$check" <<<"$got" && continue
    echo "own-policy: a change to $path no longer requires $check, got:" >&2
    echo "$got" >&2
    failed=1
  done
done <<'EOF'
identity/registry.go identity-e2e,identity-unit
schema/identity.cue attest-schema,cue-vet,identity-schema
policy/project/policy.cue cue-vet,own-policy,policy-deriver
policy/valley cue-vet,own-policy,policy-deriver
schema/verification.cue attest-schema,cue-vet,identity-schema,own-policy,policy-deriver
EOF
[ "$failed" -eq 0 ] || exit 1

# The restacker's cap is the default.
cap="$(cue export -e policy.restack.maxAutomaticVersions "$schema" \
  "$policy"/instance/*.cue "$policy"/project/*.cue)"
[ "$cap" = 64 ] || {
  echo "own-policy: the restacker's cap is $cap, not 64" >&2
  exit 1
}

touch "$out"
