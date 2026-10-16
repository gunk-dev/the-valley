# shellcheck shell=bash
# The plan graph's two validators, held to agreeing: the Go one (knot/),
# which is the authority for refs/the-valley/knots, and the CUE schema
# with the knowledge lint, which read a graph kept in a project's tree.
# Both are run over the same graphs, the shared fixtures and graphs
# generated from a fixed seed, and the check fails on any graph where
# either verdict is not the one the case states. Every rejected case names
# the rule it breaks, and that rule must be one the two share.
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
set -euo pipefail

w="$TMPDIR/knot-agree"
mkdir -p "$w"

fail() {
  printf 'knot-agree: %s\n' "$*" >&2
  exit 1
}

# The constants the lint restates must be the contract's.
knotcheck contract > "$w/contract.json"
cutoff="$(sed -n 's/.*"cutoff":"\([^"]*\)".*/\1/p' "$w/contract.json")"
grep -qx "ID_RULE_CUTOFF = \"$cutoff\"" "$lint" \
  || fail "the lint's ID_RULE_CUTOFF is not the contract's $cutoff"
cue export -e '#Directories' "$nodeSchema" --out json | tr -d ' \n' > "$w/cue-dirs"
sed -n 's/.*"directories":\({[^}]*}\).*/\1/p' "$w/contract.json" > "$w/go-dirs"
python3 -c 'import json,sys; a,b=(json.load(open(p)) for p in sys.argv[1:]); sys.exit(a!=b)' \
  "$w/cue-dirs" "$w/go-dirs" || fail "the type directories differ: $(cat "$w/cue-dirs") against $(cat "$w/go-dirs")"

knotcheck rules > "$w/shared"
knotcheck fixtures "$w/fixtures"
knotcheck generate "$w/generated" 40 20261015

checked=0
for set in fixtures generated; do
  while IFS=$'\t' read -r case verdict rule; do
    dir="$w/$set/$case"
    if [ "$verdict" = reject ] && ! grep -qx -- "$rule" "$w/shared"; then
      fail "$case breaks $rule, which is no rule the validators share"
    fi
    by_go="accept"
    knotcheck validate "$dir/graph" > "$dir/go.out" || by_go="reject"
    by_lint="accept"
    mkdir -p "$dir/work"
    python3 "$lint" --tree "$dir/tree" --root knots --schema "$nodeSchema" \
      --work "$dir/work" > "$dir/lint.out" 2>&1 || by_lint="reject"
    if [ "$by_go" != "$verdict" ] || [ "$by_lint" != "$verdict" ]; then
      echo "knot-agree: $set/$case should be $verdict${rule:+ ($rule)}; Go says $by_go, the lint says $by_lint" >&2
      cat "$dir/go.out" "$dir/lint.out" >&2
      find "$dir/graph" -type f -print -exec cat {} \; >&2
      exit 1
    fi
    checked=$((checked + 1))
  done < "$w/$set/cases.tsv"
done
[ "$checked" -gt 60 ] || fail "only $checked graphs were checked"
echo "knot-agree: $checked graphs, the validators agree on each"
touch "$out"
