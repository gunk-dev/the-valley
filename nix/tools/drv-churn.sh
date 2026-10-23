#!/usr/bin/env bash
# drv-churn: which flake checks an edit gives a new derivation.
#
# A check whose derivation an edit leaves unchanged is a cache hit, and its
# evidence carries to the edited tree under the pure transfer rule. This
# script measures that. It exports a revision's tree the way attest does
# (git archive into a plain directory, so the flake has no rev), applies one
# representative edit at a time to a copy of it, evaluates every check's
# drvPath, and prints which checks changed. Nothing is built, except what
# the checks' evaluation imports from derivations.
#
#   nix/tools/drv-churn.sh                  measure HEAD's tree and definitions
#   nix/tools/drv-churn.sh origin/main      measure another revision's
#   nix/tools/drv-churn.sh "$(git stash create)"
#                                           measure uncommitted, staged work
#
# Options:
#   --git            evaluate each edit as a commit of a git flake, so self.rev
#                    changes with every edit, as it does for nix build run in
#                    a checkout. The default mode is attest's.
#   --history RANGE  instead of the fixed edits, replay the paths each
#                    non-merge commit in RANGE touched (for example
#                    origin/main~20..origin/main): every path the commit
#                    changed that exists in the measured tree is edited at
#                    once. A commit's changes to the Nix definitions
#                    themselves are replayed as comments, so this measures
#                    the measured revision's definitions, not the commit's.
#   --expect FILE    compare the fixed edits' results with FILE, a table
#                    written by --write, and fail on any difference.
#   --write FILE     write the fixed edits' results to FILE.
#                    Both use the fixed edits in the default mode.
#
# Every edit appends a comment line in the file's own syntax, adds a new
# file, or deletes one. An edit to a file that holds no input of any check
# must change no derivation; an edit to a check's input must change that
# check's.
set -euo pipefail

mode=path
history=
expect=
write=
rev=HEAD
while [ $# -gt 0 ]; do
  case "$1" in
    --git) mode=git ;;
    --history) history="$2"; shift ;;
    --expect) expect="$2"; shift ;;
    --write) write="$2"; shift ;;
    -h | --help) sed -n '2,/^set -euo/p' "$0" | sed '$d; s/^# \{0,1\}//'; exit 0 ;;
    -*) echo "drv-churn: unknown option $1" >&2; exit 2 ;;
    *) rev="$1" ;;
  esac
  shift
done
if { [ -n "$history" ] || [ "$mode" = git ]; } && { [ -n "$expect" ] || [ -n "$write" ]; }; then
  echo "drv-churn: --expect and --write record the fixed edits in the default mode" >&2
  exit 2
fi

repo="$(git rev-parse --show-toplevel)"
commit="$(git -C "$repo" rev-parse --verify "$rev^{commit}")"
system="$(nix eval --impure --raw --expr builtins.currentSystem)"
work="$(mktemp -d "${KLAUS_SCRATCH:-${TMPDIR:-/tmp}}/drv-churn.XXXXXX")"
trap 'chmod -R u+w "$work" 2>/dev/null; rm -rf "$work"' EXIT

base="$work/base"
mkdir "$base"
git -C "$repo" archive "$commit" | tar -x -C "$base"

# One line per check: its name and its drvPath.
evaluate() {
  local ref="$1"
  # shellcheck disable=SC2016 # the expression is Nix, not shell
  nix eval --raw "$ref#checks.$system" --apply '
    cs: builtins.concatStringsSep "" (map (n: n + " " + cs.${n}.drvPath + "\n")
      (builtins.attrNames cs))'
}

# The flake reference for a tree. In git mode the tree is committed first,
# with a fixed identity and date, so the same edit makes the same commit.
flakeOf() {
  local dir="$1" label="$2"
  if [ "$mode" = git ]; then
    git -C "$dir" add -A
    GIT_AUTHOR_DATE=@0 GIT_COMMITTER_DATE=@0 git -C "$dir" \
      -c user.name=drv-churn -c user.email=drv-churn@localhost \
      commit --quiet --allow-empty --no-verify -m "$label"
    echo "git+file://$dir?rev=$(git -C "$dir" rev-parse HEAD)"
  else
    echo "path:$dir"
  fi
}

# The comment line that file takes, by its name.
comment() {
  case "$1" in
    *.go | *.cue) echo "// drv-churn" ;;
    *.md | *.txt | *.json) echo "drv-churn" ;;
    *) echo "# drv-churn" ;;
  esac
}

# Apply one edit to dir: append to, add or delete path.
apply() {
  local dir="$1" op="$2" path="$3"
  case "$op" in
    append)
      [ -f "$dir/$path" ] || { echo "drv-churn: no file $path to edit" >&2; exit 1; }
      printf '\n%s\n' "$(comment "$path")" >> "$dir/$path"
      ;;
    add)
      [ ! -e "$dir/$path" ] || { echo "drv-churn: $path already exists" >&2; exit 1; }
      mkdir -p "$(dirname "$dir/$path")"
      comment "$path" > "$dir/$path"
      ;;
    delete)
      [ -f "$dir/$path" ] || { echo "drv-churn: no file $path to delete" >&2; exit 1; }
      rm "$dir/$path"
      ;;
    commit) ;;
  esac
}

# The fixed edits, as label, operation and path. A Go module's edit is to
# its first source file that is not a test.
edits() {
  cat <<'EOF'
commit only	commit	-
bin/valley	append	bin/valley
completions	append	completions/_valley
doc	append	docs/architecture.md
doc: README	append	README.md
doc: module README	append	sigverify/README.md
doc: archive	append	archive/design/roadmap.md
doc: research	append	research/README.md
doc: added	add	docs/churn-probe.md
schema: attestation	append	schema/attestation.cue
schema: events	append	schema/events.cue
schema: identity	append	schema/identity.cue
schema: node	append	schema/node.cue
schema: valley	append	schema/valley.cue
schema: verification	append	schema/verification.cue
check script	append	nix/checks/valley-status.sh
check script: shared	append	nix/checks/fail-nth.sh
check script: added	add	nix/checks/churn-probe.sh
nix: module	append	nix/valley-host.nix
nix: packages	append	nix/packages.nix
nix: knowledge lint	append	nix/knowledge-lint.py
flake.nix	append	flake.nix
policy	append	policy/project/policy.cue
fixture: host	append	examples/hosts/protected.cue
fixture: smoke policy	append	examples/policy/smoke/project/policy.cue
fixture: deriver policy	append	examples/policy/deriver/project.cue
fixture: registry	append	examples/identity/registry/registry.cue
fixture: graph	append	examples/graphs/consumer/README.md
fixture: graph deleted	delete	examples/graphs/consumer/docs/design.md
fixture: integrator e2e	append	integrator/e2e/run.sh
fixture: plan golden	append	status/testdata/plan/plan.txt
fixture: attest vectors	append	attest/conformance/README.md
go test: status	append	status/status_test.go
EOF
  local mod file
  while IFS= read -r mod; do
    file="$(cd "$base/$mod" && find . -maxdepth 1 -name '*.go' ! -name '*_test.go' | LC_ALL=C sort | head -n1)"
    [ -n "$file" ] || continue
    printf 'go: %s\tappend\t%s\n' "$mod" "$mod/${file#./}"
  done < <(cd "$base" && find . -name go.mod ! -path './archive/*' -printf '%h\n' | sed 's|^\./||' | LC_ALL=C sort)
}

# The history's edits: one per non-merge commit in the range, oldest
# first, as label and the paths it touched.
historyEdits() {
  local c
  git -C "$repo" rev-list --reverse --no-merges "$history" | while IFS= read -r c; do
    printf '%s %s\t' "$(git -C "$repo" rev-parse --short "$c")" \
      "$(git -C "$repo" log -1 --format=%s "$c" | cut -c1-60)"
    git -C "$repo" diff-tree --no-commit-id --name-only -r "$c" | tr '\n' ' '
    echo
  done
}

# The checks whose drvPath differs between two evaluations.
changed() {
  join -j1 <(LC_ALL=C sort "$1") <(LC_ALL=C sort "$2") |
    awk '$2 != $3 { print $1 }' | tr '\n' ' ' | sed 's/ $//'
}

if [ "$mode" = git ]; then
  git -C "$base" init --quiet --initial-branch=main
fi
evaluate "$(flakeOf "$base" base)" > "$work/base.drvs"
total="$(wc -l < "$work/base.drvs")"
echo "# drv-churn: $(git -C "$repo" rev-parse --short "$commit") ($mode mode), $total checks" >&2

results="$work/results"
: > "$results"
run() {
  local label="$1" dir="$work/edit"
  shift
  if [ "$mode" = git ]; then
    dir="$base"
    git -C "$dir" checkout --quiet --force --detach main
    git -C "$dir" clean --quiet -fdx
  else
    rm -rf "$dir"
    cp -a "$base" "$dir"
  fi
  while [ $# -gt 0 ]; do
    apply "$dir" "$1" "$2"
    shift 2
  done
  evaluate "$(flakeOf "$dir" "$label")" > "$work/edit.drvs"
  local names
  names="$(changed "$work/base.drvs" "$work/edit.drvs")"
  printf '%s\t%s\n' "$label" "$names" >> "$results"
  printf '%-34s %3d  %s\n' "$label" "$(wc -w <<< "$names")" "$names"
}

if [ -n "$history" ]; then
  sum=0
  count=0
  while IFS=$'\t' read -r label paths; do
    args=()
    for p in $paths; do
      [ -f "$base/$p" ] && args+=(append "$p")
    done
    run "$label" "${args[@]}"
    sum=$((sum + $(tail -n1 "$results" | cut -f2 | wc -w)))
    count=$((count + 1))
  done < <(historyEdits)
  awk -v s="$sum" -v c="$count" -v t="$total" \
    'BEGIN { printf "mean: %.1f of %d checks changed per commit, over %d commits\n", s / c, t, c }'
else
  while IFS=$'\t' read -r label op path; do
    run "$label" "$op" "$path"
  done < <(edits)
fi

if [ -n "$write" ]; then
  cp "$results" "$write"
fi
if [ -n "$expect" ]; then
  diff -u "$expect" "$results" >&2 || {
    echo "drv-churn: the edits changed other checks than $expect records" >&2
    exit 1
  }
fi
