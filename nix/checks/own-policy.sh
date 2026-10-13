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
# and unclassified rule, and the template defines the #docs this layer
# embeds.
policy="$TMPDIR/policy"
mkdir -p "$policy/instance" "$policy/project"
cp "$floor" "$policy/instance/floor.cue"
cp "$template" "$policy/instance/template.cue"
cp "$project"/*.cue "$policy/project/"

# The layer composes.
cue vet -c "$schema" "$policy"/instance/*.cue "$policy"/project/*.cue

# Every check the composition names is a flake check this flake defines.
# A policy naming a check nobody can build would be found only when a
# change owing it is approved.
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

# A change to the docs owes the floor's prose-format alone, and a change to
# the hook owes the hook's tests. Classes that matched too much or nothing
# at all would break one or the other.
repo="$TMPDIR/tree"
git init --quiet "$repo"
cd "$repo" || exit 1
mkdir -p docs valleyhook nix/checks
echo seed > docs/architecture.md
echo seed > valleyhook/main.go
echo seed > nix/packages.nix
for f in $checkFiles; do
  echo seed > "nix/checks/$f"
done
git add -A
git commit --quiet -m base
base="$(git rev-parse HEAD)"

owed() {
  valley checks --schema "$schema" \
    --instance "$policy/instance" --project "$policy/project" \
    --names "$base" HEAD
}

git checkout --quiet -B docs "$base"
echo edit > docs/architecture.md
git commit --quiet -am docs
got="$(owed)"
[ "$got" = prose-format ] || {
  echo "own-policy: a docs-only change must owe prose-format alone, got:" >&2
  echo "$got" >&2
  exit 1
}

git checkout --quiet -B hook "$base"
echo edit > valleyhook/main.go
git commit --quiet -am hook
got="$(owed)"
grep -qx valleyhook-unit <<<"$got" || {
  echo "own-policy: a hook-only change must owe valleyhook-unit, got:" >&2
  echo "$got" >&2
  exit 1
}
if grep -qx prose-format <<<"$got"; then
  echo "own-policy: a hook-only change fell to the unclassified rule" >&2
  exit 1
fi

# A check's own files owe that check. Every file under nix/checks/ named
# after a flake check owes that check, and every other script there owes
# some check the nix class does not. A script that matched the nix class
# alone could be changed without its check being owed. Other files that
# are not named after a check are definitions or data shared by several
# checks, which the policy may leave to the nix class.
git checkout --quiet -B shared "$base"
echo edit > nix/packages.nix
git commit --quiet -am shared
shared="$(owed | LC_ALL=C sort)"
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
  got="$(owed | LC_ALL=C sort)"
  if [ "$named" -eq 1 ]; then
    grep -qx -- "$name" <<<"$got" && continue
    echo "own-policy: a change to nix/checks/$f must owe $name, got:" >&2
  else
    [ -n "$(LC_ALL=C comm -23 <(echo "$got") <(echo "$shared"))" ] && continue
    echo "own-policy: a change to nix/checks/$f owes no check of its own, got:" >&2
  fi
  echo "$got" >&2
  failed=1
done
[ "$failed" -eq 0 ] || exit 1

touch "$out"
