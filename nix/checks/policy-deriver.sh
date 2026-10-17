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
    "$@" "$base" HEAD
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

# Assert the carry mode the report states. The floor gives flat
# `same-class` and nested `exact`, and no other class a mode.
expect_carry() {
  local got
  got="$(sed -n 's/^carry mode: //p' <<<"$report")"
  if [ "$got" != "$1" ]; then
    echo "policy-deriver: $2" >&2
    echo "  expected carry mode: $1" >&2
    echo "  got:                 $got" >&2
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
# Overlapping classes compose to the strictest mode: flat's same-class
# over deep's same-paths.
expect_carry same-class "a path in a same-class class and a same-paths one must be same-class"

case_start deep
echo edit > src/deep/two.rs
git commit --quiet -am deep
report="$(derive)"
expect "c-deep" "src/*.rs must not match across a path separator"
expect_carry same-paths "a class that asks for no mode must be same-paths"

# A `**` between two segments matches no segment at all …
case_start nested-none
echo edit > a/b
git commit --quiet -am nested-none
report="$(derive)"
expect "c-nested" "a/**/b must match a/b"
expect_carry exact "a path in an exact class must be exact"

# Paths in different classes compose the same way: exact over same-class.
case_start strictest
echo edit > a/b
echo edit > src/one.rs
git commit --quiet -am strictest
report="$(derive)"
expect "c-deep c-flat c-nested" "a change to two classes must require both"
expect_carry exact "an exact path beside a same-class one must be exact"

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

# A check's cost is a declared property and changes nothing required. The
# same diff under a project layer that marks c-docs slow requires the same
# checks, for the same classes, with the same mandatory bits: every line above the
# cost summary is the same. Only the summary moves c-docs.
cp -R "$policy/project" "$policy/project-slow"
cat > "$policy/project-slow/slow.cue" <<'EOF'
package verification
project: checks: "c-docs": {runner: "nix", attribute: "c-docs", cost: "slow"}
EOF
derive_slow() {
  valley checks --schema "$schema" \
    --instance "$policy/instance" --project "$policy/project-slow" \
    "$@" "$base" HEAD
}
quick_report="$report"
report="$(derive_slow)"
expect "c-docs c-tmpl" "a slow check must be required as a quick one is"
above_summary() { sed -e '/^quick:/,$d' -e '/^policy:/d' <<<"$1"; }
[ "$(above_summary "$quick_report")" = "$(above_summary "$report")" ] || {
  echo "policy-deriver: marking a check slow changed what is required" >&2
  diff <(above_summary "$quick_report") <(above_summary "$report") >&2
  exit 1
}
grep -qx 'quick: c-docs, c-tmpl' <<<"$quick_report"
grep -qx 'slow: none' <<<"$quick_report"
grep -qx 'quick: c-tmpl' <<<"$report"
grep -qx 'slow: c-docs' <<<"$report"

# The names alone are what an agent reads, so they are exactly the required
# names, one per line, and --cost filters them.
[ "$(derive_slow --names)" = "$(printf 'c-docs\nc-tmpl')" ]
[ "$(derive_slow --names --cost quick)" = c-tmpl ]
[ "$(derive_slow --names --cost slow)" = c-docs ]
[ "$(derive --names --cost slow)" = "" ]
if derive_slow --names --cost fast 2> "$TMPDIR/cost.err"; then
  echo "policy-deriver: --cost fast was accepted" >&2
  exit 1
fi
grep -q "cost takes quick or slow, not 'fast'" "$TMPDIR/cost.err"
if derive_slow --cost quick 2> /dev/null; then
  echo "policy-deriver: --cost without --names was accepted" >&2
  exit 1
fi

# A change that requires nothing prints no names and no cost summary.
report="$(valley checks --schema "$schema" \
  --instance "$policy/instance" --project "$policy/project-slow" "$base" "$base")"
grep -qx 'required checks: none' <<<"$report"
expect_carry same-paths "a change that touches nothing must be same-paths"
if grep -q '^quick:' <<<"$report"; then
  echo "policy-deriver: a change that requires nothing printed a cost summary" >&2
  exit 1
fi
[ "$(valley checks --schema "$schema" --names \
  --instance "$policy/instance" --project "$policy/project-slow" "$base" "$base")" = "" ]

# A schema that predates `cost` cannot render the composed policy. The
# deriver refuses rather than reading the empty render as a policy that
# requires nothing.
grep -v '^	cost: \*"quick" | "slow"$' "$schema" > "$TMPDIR/costless.cue"
if cmp -s "$schema" "$TMPDIR/costless.cue"; then
  echo "policy-deriver: the costless schema still declares cost, so this case proves nothing" >&2
  exit 1
fi
if valley checks --schema "$TMPDIR/costless.cue" \
  --instance "$policy/instance" --project "$policy/project" \
  "$base" HEAD > "$TMPDIR/costless.out" 2>&1; then
  echo "policy-deriver: a schema the render cannot read derived an answer" >&2
  cat "$TMPDIR/costless.out" >&2
  exit 1
fi
grep -q 'does not render' "$TMPDIR/costless.out"

# The same for a schema that predates the carry modes: with no resolved
# mode to render, the deriver refuses rather than reading every class as
# same-paths.
grep -v '^_mode: ' "$schema" > "$TMPDIR/modeless.cue"
if cmp -s "$schema" "$TMPDIR/modeless.cue"; then
  echo "policy-deriver: the modeless schema still resolves modes, so this case proves nothing" >&2
  exit 1
fi
if valley checks --schema "$TMPDIR/modeless.cue" \
  --instance "$policy/instance" --project "$policy/project" \
  "$base" HEAD > "$TMPDIR/modeless.out" 2>&1; then
  echo "policy-deriver: a schema with no carry modes derived an answer" >&2
  cat "$TMPDIR/modeless.out" >&2
  exit 1
fi
grep -q 'does not render' "$TMPDIR/modeless.out"

# A document may refine `policy` directly, and a mode written there counts.
# The floor gives the deep class no mode, and a project document that
# refines `policy` makes it exact.
cp -R "$policy/project" "$policy/project-refined"
cat > "$policy/project-refined/refined.cue" <<'EOF'
package verification
policy: classes: deep: carry: exact: true
EOF
git checkout --quiet deep
report="$(valley checks --schema "$schema" \
  --instance "$policy/instance" --project "$policy/project-refined" "$base" HEAD)"
expect "c-deep" "refining policy's carry must not change what deep requires"
expect_carry exact "a mode written by refining policy must count"

# A policy string holding a newline cannot forge a line of the render. Each
# forged layer adds a pattern that would end its own line and start another:
# a looser carry mode for the nested class, which the floor makes exact, or
# a second copy of a requirement. There are three walls, and each is tested
# with the walls before it taken down. The schema refuses the pattern, so
# the layers do not compose. Under a schema that admits it, the render
# refuses the line. Under a copy of the script whose render admits it too,
# the deriver refuses the second line.
for forged in carry requires; do
  mkdir -p "$policy/forged-$forged"
  cp "$policy/project/policy.cue" "$policy/forged-$forged/policy.cue"
done
cat > "$policy/forged-carry/forged.cue" <<'EOF'
package verification
project: classes: forged: paths: "unused\ncarry\tnested\tsame-paths": true
EOF
cat > "$policy/forged-requires/forged.cue" <<'EOF'
package verification
project: classes: forged: paths: "unused\nrequires\tnested\tc-nested": true
EOF

# The schema with #Pattern admitting anything, and the script with its
# render's line check removed. Each must differ from what it copies, or the
# case below it proves nothing.
[ "$(grep -c '^#Pattern: ' "$schema")" = 1 ] || {
  echo "policy-deriver: #Pattern is not one line of the schema, so the loose schema proves nothing" >&2
  exit 1
}
{ grep -v '^#Pattern: ' "$schema"; echo '#Pattern: string'; } > "$TMPDIR/loose.cue"
sed -e 's/" & line}$/"}/' -e '/^let line = /d' "$cliScript" > "$TMPDIR/valley-unguarded"
if ! grep -q '" & line}$' "$cliScript" || grep -q '& line}\|^let line' "$TMPDIR/valley-unguarded"; then
  echo "policy-deriver: the unguarded script still checks its render's lines, so it proves nothing" >&2
  exit 1
fi

# refused WHAT TEXT COMMAND…: COMMAND must fail, print nothing on stdout,
# and say TEXT.
refused() {
  local what="$1" text="$2"
  shift 2
  if "$@" > "$TMPDIR/forged.out" 2> "$TMPDIR/forged.err"; then
    echo "policy-deriver: $what derived an answer" >&2
    cat "$TMPDIR/forged.out" >&2
    exit 1
  fi
  [ ! -s "$TMPDIR/forged.out" ] || {
    echo "policy-deriver: $what printed to stdout" >&2
    exit 1
  }
  grep -qF -- "$text" "$TMPDIR/forged.err" || {
    echo "policy-deriver: $what was refused without saying $text" >&2
    cat "$TMPDIR/forged.err" >&2
    exit 1
  }
}

# forged_derive SCHEMA LAYER [SCRIPT]: derive the case at HEAD under SCHEMA
# and the project layer LAYER, with the real deriver or with SCRIPT.
forged_derive() {
  local -a deriver=(valley)
  [ $# -lt 3 ] || deriver=(bash "$3")
  "${deriver[@]}" checks --schema "$1" --instance "$policy/instance" \
    --project "$policy/$2" "$base" HEAD
}

case_start forged
echo edit > a/b
git commit --quiet -am forged
# Without the walls, an honest layer still derives exact here, so each
# refusal below is the forgery's doing.
report="$(forged_derive "$TMPDIR/loose.cue" project "$TMPDIR/valley-unguarded")"
expect "c-nested" "the unguarded script must derive an honest layer as the real one does"
expect_carry exact "the unguarded script must derive an honest layer's carry mode"

refused "a forged carry line" "do not compose" \
  forged_derive "$schema" forged-carry
refused "a forged carry line under a schema that admits it" "does not render" \
  forged_derive "$TMPDIR/loose.cue" forged-carry
refused "a forged carry line under a render that admits it" \
  "rendered 'carry nested' twice" \
  forged_derive "$TMPDIR/loose.cue" forged-carry "$TMPDIR/valley-unguarded"
refused "a forged requirement line under a render that admits it" \
  "rendered 'requires nested c-nested' twice" \
  forged_derive "$TMPDIR/loose.cue" forged-requires "$TMPDIR/valley-unguarded"

# A path no class covers takes the unclassified set, and says
# so by name. It is same-paths.
case_start uncovered
echo edit > Makefile
git commit --quiet -am uncovered
report="$(derive)"
expect "c-uncovered" "an uncovered path must take the unclassified set"
grep -q '^classes matched: unclassified$' <<<"$report"
grep -q '^unclassified: 1 path(s) matched no class$' <<<"$report"
grep -q '^  Makefile$' <<<"$report"
expect_carry same-paths "an unclassified path must be same-paths"

# A replacement ref changes nothing the derivation reads. Here
# refs/replace/<head> makes git read the uncovered case's head as
# the flat case's, so a deriver that left replacement refs on
# would derive the src/ checks for a change that touches only
# the Makefile — and the caller asking for them is not listened
# to either.
git update-ref "refs/replace/$(git rev-parse uncovered)" "$(git rev-parse flat)"
git diff --name-only "$base" uncovered | grep -x src/one.rs > /dev/null \
  || { echo "policy-deriver: the replacement did not take, so this case proves nothing" >&2; exit 1; }
git checkout --quiet uncovered
report="$(GIT_CONFIG_PARAMETERS="'core.usereplacerefs'='true'" derive)"
expect "c-uncovered" "a replacement ref must not change the derived checks"
git update-ref -d "refs/replace/$(git rev-parse uncovered)"

# ----------------------------------------------------------------------
# Every producer fails closed. The deriver's calls of git, sort and cue
# are failed one at a time, one per run, by fail-nth standing in for the
# tool first on PATH. A producer read through a process substitution
# loses its status, and its failure then reads as an empty list — no
# paths, no catalogue, no classes — which derives a change that
# requires nothing. So every failure must end the run non-zero, say so, and print
# nothing on stdout: no report, and no names.
#
# This runs bin/valley itself, because the package's wrapper puts its own
# git ahead of anything on PATH. The case touches a class, a second class
# through `**`, the template's default, and an uncovered path, so every
# producer has something to lose.
case_start mixed
echo edit > docs/kept.md
echo edit > src/one.rs
echo edit > Makefile
git commit --quiet -am mixed
# An unclassified path beside a same-class one makes the change no looser.
report="$(derive)"
expect_carry same-class "an unclassified path must not loosen a same-class change"

shims="$TMPDIR/shims"
mkdir -p "$shims"
for tool in git sort cue; do ln -s "$failNth" "$shims/$tool"; done

# shimmed TOOL N [option…]: derive the mixed case with TOOL's Nth call
# failing (0 fails none). The count of TOOL's calls is left in
# $TMPDIR/shim-count.
shimmed() {
  local tool="$1" at="$2" status=2
  shift 2
  [ "$tool" != git ] || status=128 # git's own status for a fatal error
  echo 0 > "$TMPDIR/shim-count"
  SHIM_REAL_PATH="$PATH" SHIM_TOOL="$tool" SHIM_FAIL_AT="$at" \
    SHIM_STATUS="$status" SHIM_COUNT="$TMPDIR/shim-count" PATH="$shims:$PATH" \
    bash "$cliScript" checks --schema "$schema" \
    --instance "$policy/instance" --project "$policy/project" \
    "$@" "$base" HEAD
}

# The check names a run's stdout requires, sorted, on one line.
required_in() {
  if [ "$1" = names ]; then
    sort "$2" | paste -sd' ' -
  else
    report="$(cat "$2")"
    named
  fi
}

shim_fail() {
  echo "policy-deriver: $1" >&2
  for f in shim.out shim.err; do
    echo "--- $f" >&2
    cat "$TMPDIR/$f" >&2
  done
  exit 1
}

for tool in git sort cue; do
  for mode in report names; do
    opts=()
    [ "$mode" = report ] || opts=(--names)
    shimmed "$tool" 0 "${opts[@]}" > "$TMPDIR/shim.want"
    want="$(required_in "$mode" "$TMPDIR/shim.want")"
    [ "$want" = "c-deep c-docs c-flat c-tmpl c-uncovered" ] || {
      echo "policy-deriver: the mixed case requires $want" >&2
      exit 1
    }
    calls="$(cat "$TMPDIR/shim-count")"
    [ "$calls" -gt 0 ] || {
      echo "policy-deriver: the $mode run never called $tool, so failing it proves nothing" >&2
      exit 1
    }
    for ((at = 1; at <= calls; at++)); do
      what="$mode run, $tool call $at of $calls"
      if shimmed "$tool" "$at" "${opts[@]}" > "$TMPDIR/shim.out" 2> "$TMPDIR/shim.err"; then
        # The one failure a run may survive is a cue probe after the
        # composition and the render: it asks whether a requirement is
        # mandatory, and a probe that fails reads as mandatory, the strict
        # answer. What is required must not move.
        [ "$tool" = cue ] && [ "$at" -gt 2 ] \
          || shim_fail "the $what failed and the deriver still answered"
        [ "$(required_in "$mode" "$TMPDIR/shim.out")" = "$want" ] \
          || shim_fail "the $what failed and changed what is required"
        continue
      fi
      [ ! -s "$TMPDIR/shim.out" ] \
        || shim_fail "the $what failed and the deriver printed to stdout"
      grep -q '^valley: ' "$TMPDIR/shim.err" \
        || shim_fail "the $what failed and the deriver did not say so"
    done
  done
done

# A diff cut short mid-record is a failure too, even though git exited 0:
# cut inside the first path, after one status, and inside the second
# status, after one whole record. The last is the one a plain `while
# read` ends on quietly, dropping the record it cut.
for cut in 1 2; do
  for opts in "" --names; do
    # shellcheck disable=SC2086 # an empty $opts is no option at all
    if SHIM_TRUNCATE="$cut" SHIM_REAL_PATH="$PATH" PATH="$shims:$PATH" \
      bash "$cliScript" checks --schema "$schema" \
      --instance "$policy/instance" --project "$policy/project" $opts \
      "$base" HEAD > "$TMPDIR/shim.out" 2> "$TMPDIR/shim.err"; then
      shim_fail "a diff cut after $cut field(s) derived an answer"
    fi
    [ ! -s "$TMPDIR/shim.out" ] \
      || shim_fail "a diff cut after $cut field(s) printed to stdout"
    grep -q 'truncated diff output' "$TMPDIR/shim.err" \
      || shim_fail "a diff cut after $cut field(s) was not named as one"
  done
done

touch "$out"
