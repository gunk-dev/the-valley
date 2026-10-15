# shellcheck shell=bash
# valley plan, end to end and unmocked: the real valley against local bare
# repositories that run no hook, as any git remote would. A graph is built
# with the real valley knot verbs, each at a fixed time given in
# VALLEY_NOW: milestones, nested items, a blocker chain, a blocker that was
# abandoned, a fresh claim and a stale one, decisions and an idea with
# owners, open and closed bugs, an edge to another project's knot and edges
# to knots that cannot be read. The text, with no terminal and no colour,
# and the JSON are compared with golden files. Then a project with no
# graph, --all over the host's projects with one that cannot be read, and
# the operator named by --owner and by no one.
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
set -euo pipefail

w="$TMPDIR/valley-plan"
mkdir -p "$w"
export HOME="$w"
export XDG_CACHE_HOME="$w/cache"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name valley-plan-check
git config --global user.email valley-plan-check@localhost
git config --global init.defaultBranch main
# Every commit outside the graph has one date, so the change's head is the
# same on every run.
export GIT_AUTHOR_DATE=2026-10-01T09:00:00Z GIT_COMMITTER_DATE=2026-10-01T09:00:00Z
unset NO_COLOR

# valley is bin/valley itself, run by bash, with valley-go on PATH.
mkdir -p "$w/bin"
printf '#!%s\nexec %q %q "$@"\n' "$BASH" "$BASH" "$cliScript" > "$w/bin/valley"
chmod +x "$w/bin/valley"
export PATH="$w/bin:$PATH"

fail() {
  printf 'valley-plan: %s\n' "$*" >&2
  for f in "$w"/out "$w"/err; do
    if [ -s "$f" ]; then cat "$f" >&2; fi
  done
  exit 1
}

# ok <command…>: the command succeeds, and its output is kept.
ok() {
  "$@" > "$w/out" 2> "$w/err" || fail "$* failed"
}

# at <time> <command…>: the command, with that time as now.
at() {
  local when="$1"
  shift
  VALLEY_NOW="$when" "$@"
}

# knot <time> <args…>: a knot verb at that time, printing nothing; new
# prints the knot's ID.
knot() {
  local when="$1"
  shift
  if [ "$1" = new ]; then
    at "$when" valley knot "$@" --json > "$w/out" 2> "$w/err" || fail "valley knot $* failed"
    jq -r .knot.id "$w/out"
  else
    at "$when" valley knot "$@" > "$w/out" 2> "$w/err" || fail "valley knot $* failed"
  fi
}

# same <golden> <file>: the file is the golden file, byte for byte.
same() {
  diff -u "$golden/$1" "$2" > "$w/diff" || {
    cat "$w/diff" >&2
    fail "$2 differs from $1"
  }
}

srv="$w/srv"
for project in the-valley cosmo quiet; do
  git init --quiet --bare "$srv/$project.git"
done
git init --quiet "$w/work"
cd "$w/work"
git remote add origin "$srv/the-valley.git"
git config valley.principal patrick

# main carries policy/instance, so this repository is its own instance,
# and the instance publishes its projects as valley-init writes the list:
# ghost is listed, and has no repository.
mkdir -p policy/instance
echo 'package verification' > policy/instance/floor.cue
git add policy
git commit --quiet -m "the instance"
git push --quiet origin main
projects="$(printf '%s\n' cosmo ghost quiet the-valley | git -C "$srv/the-valley.git" hash-object -w --stdin)"
git -C "$srv/the-valley.git" update-ref refs/the-valley/projects "$projects"

# A change ready for approval, under a fixed change ID.
git checkout --quiet -b plan-view
git commit --quiet --allow-empty -m "plan: the plan graph as a view"
git push --quiet origin plan-view
ok valley ready --change pqrstuvwxyzkpqrstuvwxyzkpqrstuvw plan-view
git checkout --quiet main

# ----------------------------------------------------------------------
# No graph yet: one line, and the command that starts one.
ok valley plan
[ "$(cat "$w/out")" = 'the-valley has no plan yet. Start one with: valley knot new outcome "<title>"' ] \
  || fail "a project with no graph did not say so in one line"
ok valley plan --json
jq -e '.schema == "valley-plan/v1" and (.projects | length) == 1 and .projects[0].tip == ""
  and .projects[0].knots == [] and .projects[0].waiting_on_you == null' "$w/out" > /dev/null \
  || fail "the JSON of a project with no graph is not empty"

# ----------------------------------------------------------------------
# The graph. Day one: two milestones, one finished by the end.
d1=2026-10-01T09:00:00Z d2=2026-10-02T09:00:00Z d3=2026-10-03T09:00:00Z
d14=2026-10-14T21:30:00Z now=2026-10-15T09:00:00Z
other="$(knot "$d1" new outcome "Land the module" --project cosmo)"
one="$(knot "$d1" new outcome "Slice one lands")"
two="$(knot "$d1" new outcome "Slice two lands")"
hook="$(knot "$d1" new bug "The hook reads too much" --parent "$one")"
case="$(knot "$d1" new outcome "Write the hook case" --parent "$one")"
# Created a day before its siblings, so listed first, though its title
# sorts last.
once="$(knot "$d1" new outcome "Read the graph once" --parent "$two")"

# Day two: the work under slice two.
tree="$(knot "$d2" new outcome "Draw the tree" --parent "$two" --owner klaus-env)"
width="$(knot "$d2" new bug "Width is off by one" --parent "$tree")"
level="$(knot "$d2" new bug "The tree misses a level" --parent "$tree")"
golden_files="$(knot "$d2" new outcome "Golden files" --parent "$tree" --blocked-by "$width")"
ship="$(knot "$d2" new outcome "Ship it" --parent "$two" --blocked-by "$golden_files" \
  --blocked-by "cosmo:$other" --blocked-by ghost:oc-0123456)"
widget="$(knot "$d2" new outcome "Use a tree widget" --parent "$two")"
schema="$(knot "$d2" new outcome "Document the schema" --parent "$two" --blocked-by "$widget" \
  --owner patrick --target cosmo:oc-7654321)"
glyphs="$(knot "$d2" new decision "Choose the glyphs" --parent "$two" --owner patrick)"
colour="$(knot "$d2" new idea "Colour the glyphs" --parent "$two" --owner patrick)"
old="$(knot "$d2" new outcome "Old work" --parent "$two")"
knot "$d2" claim "$old" --run run-0

# Day three: a milestone created last whose title sorts first, and two
# knots under no milestone.
late="$(knot "$d3" new outcome "A late milestone")"
stray="$(knot "$d3" new bug "A stray report")"
web="$(knot "$d3" new idea "The graph as a web page")"
dropped="$(knot "$d3" new idea "A dropped thought")"

# Then the work moves.
knot "$d3" set "$hook" status=closed
knot "$d3" set "$case" status=done
knot "$d3" set "$once" status=done
knot "$d3" set "$level" status=closed
knot "$d3" set "$widget" status=abandoned
knot "$d3" set "$dropped" status=discarded
knot "$d14" claim "$tree" --run run-7
: "$late" "$stray" "$web" "$ship" "$schema" "$glyphs" "$colour"

# ----------------------------------------------------------------------
# The view, with no terminal and no colour, and its JSON.
ok at "$now" valley plan
cp "$w/out" "$w/plan.txt"
same plan.txt "$w/plan.txt"
if grep -q $'\x1b' "$w/plan.txt"; then fail "plan printed an escape code off a terminal"; fi
LC_ALL=C.UTF-8 awk -v w=80 '{ if (length($0) > w) { print "line " NR " is " length($0) " columns"; bad = 1 } } END { exit bad }' \
  "$w/plan.txt" > "$w/err" || fail "a line runs past 80 columns"

# The JSON is compared with each object ID made <sha>: the graph's
# commits carry the random IDs of their operations.
ok at "$now" valley plan --json
jq 'walk(if type == "string" and test("^[0-9a-f]{40}$") then "<sha>" else . end)' "$w/out" > "$w/plan.json"
same plan.json "$w/plan.json"

# Each derived fact the text shows is in the JSON too.
holds() {
  local cond
  for cond in "$@"; do
    jq -e "$cond" "$w/out" > /dev/null || fail "$cond does not hold"
  done
}
p='.projects[0]'
k() { printf '%s.knots[] | select(.id == "%s")' "$p" "$1"; }
holds "$p.roots == [\"$one\", \"$two\", \"$late\"]" \
  "$(k "$one").progress == {\"done\": 2, \"of\": 2}" \
  "$(k "$two").progress == {\"done\": 2, \"of\": 10}" \
  "$(k "$tree").progress == {\"done\": 1, \"of\": 3}" \
  "$(k "$golden_files").state == \"blocked\"" \
  "$(k "$schema").state == \"ready\"" \
  "$(k "$schema").cleared_by_ending == [\"$widget\"]" \
  "$(k "$old").claim.stale == true" \
  "$(k "$tree").claim.stale == false" \
  "$(k "$tree").claim.age_seconds == 41400" \
  "[$(k "$ship").edges[] | {ref, state, why}] == [{\"ref\": \"cosmo:$other\", \"state\": \"resolved\", \"why\": null}, {\"ref\": \"ghost:oc-0123456\", \"state\": \"dangling\", \"why\": \"unreadable\"}]" \
  "[$(k "$schema").edges[] | {field, state, why}] == [{\"field\": \"target\", \"state\": \"dangling\", \"why\": \"missing\"}]" \
  "$p.waiting_on_you.knots == [\"$glyphs\", \"$schema\"]" \
  "$p.waiting_on_you.changes | map(.handle) == [\"pqrs\"]" \
  "$p.in_flight == [\"$old\", \"$tree\"]" \
  "$p.open_residuals == [{\"parent\": null, \"bugs\": [\"$stray\"]}, {\"parent\": \"$tree\", \"bugs\": [\"$width\"]}]"

# ----------------------------------------------------------------------
# The operator: --owner names another, and with no principal named the
# view says how to name one.
ok at "$now" valley plan --owner klaus-env --json
holds '.operator == "klaus-env"' "$p.waiting_on_you.knots == [\"$tree\"]"
git config --unset valley.principal
ok at "$now" valley plan
grep -qx '  set git config valley.principal <name>, or pass --owner <name>, to see the knots you own' "$w/out" \
  || fail "with no operator named, plan did not say how to name one"
ok at "$now" valley plan --json
holds '.operator == null' "$p.waiting_on_you.knots == null"
git config valley.principal patrick

# ----------------------------------------------------------------------
# Another project, by --project, and every project the host publishes.
ok at "$now" valley plan --project quiet
[ "$(cat "$w/out")" = 'quiet has no plan yet. Start one with: valley knot new outcome "<title>" --project quiet' ] \
  || fail "--project naming a project with no graph did not say so"
ok at "$now" valley plan --all
cp "$w/out" "$w/plan-all.txt"
same plan-all.txt "$w/plan-all.txt"
ok at "$now" valley plan --all --json
holds '[.projects[].project] == ["cosmo", "ghost", "quiet", "the-valley"]' \
  '.projects[1].error != null' '.projects[2].tip == "" and .projects[2].error == null' \
  '.projects[0].knots | length == 1'

# Refusals.
fails() {
  local says="$1" status=0
  shift
  "$@" > "$w/out" 2> "$w/err" || status=$?
  [ "$status" = 1 ] || fail "$* exited $status, not 1"
  grep -qE -- "$says" "$w/err" || fail "$* did not say $says"
}
fails 'not both' valley plan --all --project cosmo
fails 'does not take --frob' valley plan --frob
fails 'is not one' valley plan --owner 'not a name'
status=0
VALLEY_NOW=yesterday valley plan > "$w/out" 2> "$w/err" || status=$?
[ "$status" = 2 ] || fail "a VALLEY_NOW that is no time exited $status, not 2"
grep -q 'RFC 3339' "$w/err" || fail "a VALLEY_NOW that is no time was not refused"

touch "$out"
