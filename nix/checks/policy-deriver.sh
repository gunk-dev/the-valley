# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
export HOME="$TMPDIR"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name valley-check
git config --global user.email valley-check@localhost
git config --global init.defaultBranch main

policy="$TMPDIR/policy"
mkdir -p "$policy/instance" "$policy/project"
cp "$fixtures/floor.cue" "$policy/instance/floor.cue"
cp "$fixtures/template.cue" "$policy/instance/template.cue"
cp "$fixtures/project.cue" "$policy/project/policy.cue"

repo="$TMPDIR/tree"
git init --quiet "$repo"
cd "$repo" || exit 1
mkdir -p docs notes src/deep a/x/y
echo moved > docs/moved.md
for f in docs/kept.md docs/gone.md src/one.rs src/deep/two.rs \
  a/b a/x/y/b Makefile; do
  echo seed > "$f"
done
git add -A
git commit --quiet -m base
base="$(git rev-parse HEAD)"

derive() {
  valley checks --schema "$schema" \
    --instance "$policy/instance" --project "$policy/project" \
    "$base" HEAD
}

# The check names in the deriver's table, sorted, on one line.
named() {
  awk '/^required checks:$/ {f = 1; next}
       /^$/ {f = 0}
       f && /^  / {print $1}' <<<"$report" | sort | paste -sd' ' -
}

# Start a case from the base tree; assert its check table.
case_start() { git checkout --quiet -B "$1" "$base"; }
expect() {
  local got
  got="$(named)"
  if [ "$got" != "$1" ]; then
    echo "policy-deriver: $2" >&2
    echo "  expected: $1" >&2
    echo "  got:      $got" >&2
    echo "$report" >&2
    exit 1
  fi
}

# `**` crosses segments, `*` does not. One file under src/
# matches both patterns; one a segment deeper matches only
# the `**` one.
case_start flat
echo edit > src/one.rs
git commit --quiet -am flat
report="$(derive)"
expect "c-deep c-flat" "src/*.rs must match a file directly under src/"

case_start deep
echo edit > src/deep/two.rs
git commit --quiet -am deep
report="$(derive)"
expect "c-deep" "src/*.rs must not match across a path separator"

# A `**` between two segments matches no segment at all …
case_start nested-none
echo edit > a/b
git commit --quiet -am nested-none
report="$(derive)"
expect "c-nested" "a/**/b must match a/b"

# … and any number of them.
case_start nested-many
echo edit > a/x/y/b
git commit --quiet -am nested-many
report="$(derive)"
expect "c-nested" "a/**/b must match a/x/y/b"

# A rename counts on both sides: the class covering the old
# path is required as much as the one covering the new.
case_start renamed
git mv docs/moved.md notes/moved.md
git commit --quiet -m renamed
git diff --name-status -M "$base" HEAD | grep -q '^R' \
  || { echo "policy-deriver: the rename case is not a rename" >&2; exit 1; }
report="$(derive)"
expect "c-docs c-notes c-tmpl" "a rename must count on both sides"

# A deleted path is still a changed path.
case_start deleted
git rm --quiet docs/gone.md
git commit --quiet -m deleted
report="$(derive)"
expect "c-docs c-tmpl" "a deletion must count"

# The form of the value is the mandatory bit: the floor's
# concrete `true` against the template's `bool | *true`.
grep -qE '^  c-docs +mandatory +docs$' <<<"$report"
grep -qE '^  c-tmpl +default +docs$' <<<"$report"

# A path no class covers takes the unclassified set, and says
# so by name.
case_start uncovered
echo edit > Makefile
git commit --quiet -am uncovered
report="$(derive)"
expect "c-uncovered" "an uncovered path must take the unclassified set"
grep -q '^classes matched: unclassified$' <<<"$report"
grep -q '^unclassified: 1 path(s) matched no class$' <<<"$report"
grep -q '^  Makefile$' <<<"$report"

touch "$out"
