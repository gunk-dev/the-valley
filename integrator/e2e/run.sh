#!/usr/bin/env bash
# The integrator, driven end to end over scratch repositories and throwaway
# keys. It runs the real binaries: `attest` produces the evidence, the
# `valley` deriver composes the policy, and the `integrator` judges and
# lands. Only the nix evaluator is stood in for (integrator/e2e/nix), and
# only because a nix build has no daemon to call.
#
# Each scenario below is one of the phase's exit criteria, observed rather
# than asserted about. Nothing here touches a repository outside its own
# scratch directory, and no ref is ever created anywhere but there.
set -euo pipefail

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

export HOME="$work"
export GIT_CONFIG_NOSYSTEM=1
export VALLEY_FAKE_STORE="$work/store"
export PATH="$here:$PATH" # the nix stand-ins, ahead of anything real

git config --global user.name valley-check
git config --global user.email valley-check@localhost
git config --global init.defaultBranch main
git config --global advice.detachedHead false

say() { printf '\n=== %s\n' "$*"; }
die() {
  printf '\nintegrator-e2e: %s\n' "$*" >&2
  exit 1
}
holds() { printf '  ok: %s\n' "$*"; }

# ----------------------------------------------------------------------
say "keys: a contributor, an integrator, and a stranger"

for who in contributor integrator stranger; do
  ssh-keygen -q -t ed25519 -N "" -C "$who" -f "$work/$who"
done
contributor=contributor.valley.invalid/attestations
integrator_name=integrator.valley.invalid/attestations
stranger=stranger.elsewhere.invalid/attestations

# The known-signers file is the registry's interim compilation
# (dcr-b87f6e8): the integrator accepts the contributor and nobody else.
attest key --key "$work/contributor" --name "$contributor" > "$work/known_signers"
# What a reader of a landed note holds: both parties to it.
cat "$work/known_signers" > "$work/reader_keys"
attest key --key "$work/integrator" --name "$integrator_name" >> "$work/reader_keys"
attest key --key "$work/stranger" --name "$stranger" > "$work/stranger_key"
# The key hash the integrator's own attestation refs are keyed by.
ihash="$(attest key --key "$work/integrator" --name "$integrator_name" | cut -d+ -f2)"
holds "the integrator holds one signer: $(cut -d+ -f1 < "$work/known_signers")"

# ----------------------------------------------------------------------
say "the served repository, and the instance policy layer"

origin="$work/project.git"
git init --quiet --bare "$origin"
repo="$work/project"
git init --quiet "$repo"
cd "$repo"
git remote add origin "$origin"

mkdir -p docs src deploy checks policy
echo "the readme" > docs/readme.md
echo "the source" > src/main.txt
echo "the deployment" > deploy/target.txt
# What each check reads, which the nix stand-in turns into its closure.
echo docs > checks/prose-format.inputs
echo src > checks/code-build.inputs

# The project layer: it declares its own classes and checks above the
# floor, and declines nothing.
cat > policy/project.cue <<'EOF'
package verification

project: {
	checks: {
		"prose-format": {runner: "nix", attribute: "prose-format"}
		"code-build": {runner: "nix", attribute: "code-build"}
		// An effectful check, with the validity window dcr-439b771 calls
		// Δ. Its observation transfers while nothing in the class that
		// requires it has landed under it and it is younger than this.
		reachable: {runner: "command", command: "true", validity: "1h"}
	}
	classes: {
		prose: {paths: "docs/**": true, requires: "prose-format": true}
		code: {paths: "src/**": true, requires: "code-build": true}
		deploy: {paths: "deploy/**": true, requires: reachable: true}
	}
	unclassified: {}
}
EOF

git add -A
git commit --quiet -m "seed the project"
git push --quiet origin main

# The instance layer lives outside the project's tree, and the integrator
# reads it from there: a change never supplies the policy that gates it.
instance="$work/instance"
mkdir -p "$instance"
cat > "$instance/floor.cue" <<'EOF'
package verification

floor: {
	checks: {}
	classes: {}
	unclassified: {}
}
EOF
holds "policy composes from $instance and the project's own policy/"

# ----------------------------------------------------------------------
# Helpers.

# attest_change <branch> <check spec…> — run the checks over the branch's
# tip and publish the branch with its attestation ref, atomically.
attest_change() {
  local branch="$1" key="${2:-$work/contributor}" name="${3:-$contributor}"
  shift 3
  attest run --repo "$repo" --key "$key" --name "$name" \
    --push origin --branch "$branch" "$@"
}

# request <change> <target> <commit> — the contributor asks for integration
# by pushing one ref. That ref is the whole of the request.
request() {
  git push --quiet origin "$3:refs/the-valley/integration-requests/$2/$1"
}

integrate() {
  integrator reconcile \
    --repo "$origin" \
    --key "$work/integrator" --name "$integrator_name" \
    --known-signers "$work/known_signers" \
    --instance "$instance" --project-policy policy \
    --schema "$SCHEMA_VERIFICATION" \
    --attest-schema "$SCHEMA_ATTESTATION" \
    --event-schema "$SCHEMA_EVENTS" \
    ${VALLEY_E2E_NOW:+--now "$VALLEY_E2E_NOW"} \
    2>&1 | tee "$work/last.out"
  grep -q . "$work/last.out" || die "the integrator said nothing"
}

tip() { git -C "$origin" rev-parse refs/heads/main; }
forget() { git -C "$origin" update-ref -d "refs/the-valley/integration-requests/main/$1"; }

# ----------------------------------------------------------------------
say "1. a change with valid transferring evidence lands"

before="$(tip)"
git checkout --quiet -b c1
echo "a better readme" > docs/readme.md
echo "a new target" > deploy/second.txt
git add -A
git commit --quiet -m "improve the readme and add a target"
attest_change c1 "" "" --check prose-format --command reachable=true
request c1 main "$(git rev-parse c1)"
integrate

grep -q "^c1 -> refs/heads/main .*: land$" "$work/last.out" || die "c1 did not land"
grep -q "check    prose-format .*transferred" "$work/last.out" || die "prose-format did not transfer"
grep -q "check    reachable .*transferred (untouched)" "$work/last.out" || die "the effectful check did not transfer"
grep -q '"event":"integration-succeeded"' "$work/last.out" || die "no integration-succeeded event"
[ "$(tip)" != "$before" ] || die "the protected ref did not move"
[ "$(tip)" = "$(git rev-parse c1)" ] || die "main is not at the change's head"
git -C "$origin" rev-parse --verify --quiet refs/the-valley/integration-requests/main/c1 > /dev/null \
  && die "the request ref outlived the landing"
holds "main moved $(git rev-parse --short "$before") -> $(git -C "$origin" rev-parse --short refs/heads/main)"

# The landed note carries both signatures, under one text, and verifies.
# The integrator writes under its own key hash, so its ref never races the
# contributor's over the same subject.
landed="refs/the-valley/attestations/$(attest digest --repo "$repo" --rev c1 | cut -d: -f2)/$ihash"
git -C "$origin" rev-parse --verify --quiet "$landed" > /dev/null \
  || die "no attestation ref by the integrator for the landed tree"
git -C "$origin" cat-file blob "$landed:prose-format/statement.note" > "$work/landed.note"
siblings="$(grep -c '^— ' "$work/landed.note")"
[ "$siblings" -eq 2 ] || die "the landed note carries $siblings signatures, not two"
attest verify --note "$work/landed.note" --known-keys "$work/reader_keys" \
  --schema "$SCHEMA_ATTESTATION" --repo "$repo" --rev c1 \
  --signer "$contributor" --signer "$integrator_name" > "$work/landed.verify"
grep -q 'matches the recorded subject digest' "$work/landed.verify" || die "the landed note does not bind to the tree"
holds "the landed note carries contributor and integrator sibling signatures and verifies"

# And the integrator's own transfer statement sits beside it.
git -C "$origin" cat-file blob "$landed:transfer.note" > "$work/transfer.note"
head -n1 "$work/transfer.note" | grep -qx 'the-valley/attestation/v1' || die "the transfer note is not a statement"
grep -qx 'predicateType the-valley/integration/transfer/v1' "$work/transfer.note" || die "wrong predicate type"
grep -qx 'predicate.change c1' "$work/transfer.note" || die "the transfer statement names no change"
grep -q '^predicate.policy.sha256 ' "$work/transfer.note" || die "the transfer statement names no policy"
attest verify --note "$work/transfer.note" --known-keys "$work/reader_keys" \
  --schema "$SCHEMA_ATTESTATION" --signer "$integrator_name" > /dev/null
holds "the transfer statement records the verdict and the policy it was derived under"

# ----------------------------------------------------------------------
say "2. an intervening landing invalidates one check's closure"

git checkout --quiet main
git pull --quiet --ff-only origin main

# c2 is authored against the current tip and reads both classes.
git checkout --quiet -b c2
echo "a readme with more in it" > docs/readme.md
echo "the source, revised" > src/main.txt
git add -A
git commit --quiet -m "revise the readme and the source"
attest_change c2 "" "" --check prose-format --check code-build
c2="$(git rev-parse c2)"

# Meanwhile another change lands, touching only what code-build reads.
git checkout --quiet main
git checkout --quiet -b intervening
echo "a second source file" > src/other.txt
git add -A
git commit --quiet -m "add a second source file"
attest_change intervening "" "" --check code-build
request intervening main "$(git rev-parse intervening)"
integrate
grep -q "^intervening -> refs/heads/main .*: land$" "$work/last.out" || die "the intervening change did not land"
holds "the intervening change landed first; c2's base has moved"

request c2 main "$c2"
integrate
grep -q "^c2 -> refs/heads/main .*: stale$" "$work/last.out" || die "c2 was not stale"
grep -q "check    prose-format .*transferred (closure)" "$work/last.out" \
  || die "prose-format should have transferred by closure equality"
grep -q "check    code-build .*input closure changed" "$work/last.out" \
  || die "code-build's closure should have moved"
grep -q '"event":"request-stale"' "$work/last.out" || die "no request-stale event"
grep -q '"checks":\["code-build"\]' "$work/last.out" || die "the event does not name exactly code-build"
git -C "$origin" rev-parse --verify --quiet refs/the-valley/integration-requests/main/c2 > /dev/null \
  || die "the request ref was dropped; there is nothing to resubmit"
holds "one request-stale naming exactly code-build; prose-format reported transferred"
forget c2

# ----------------------------------------------------------------------
say "2b. evidence that survives the rebase lands on the moved tip"

# The same shape as 2, with the intervening landing outside what the
# change's checks read. The tree that lands is not the tree that was
# attested, and the evidence stands anyway — which is the whole of what
# content-addressed evidence buys.
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c2b
echo "a readme revised again" > docs/readme.md
git add -A
git commit --quiet -m "revise the readme once more"
attest_change c2b "" "" --check prose-format
c2b="$(git rev-parse c2b)"
attested="$(attest digest --repo "$repo" --rev c2b | cut -d: -f2)"

git checkout --quiet main
git checkout --quiet -b elsewhere
echo "a third source file" > src/third.txt
git add -A
git commit --quiet -m "add a third source file"
attest_change elsewhere "" "" --check code-build
request elsewhere main "$(git rev-parse elsewhere)"
integrate
grep -q "^elsewhere -> refs/heads/main .*: land$" "$work/last.out" || die "the intervening change did not land"

request c2b main "$c2b"
integrate
grep -q "^c2b -> refs/heads/main .*: land$" "$work/last.out" || die "c2b did not land"
grep -q "check    prose-format .*transferred (closure)" "$work/last.out" \
  || die "the evidence did not transfer by closure equality"
landedtree="$(git -C "$origin" rev-parse refs/heads/main^{tree})"
[ "$(git -C "$origin" rev-parse "refs/heads/main^")" = "$(git rev-parse elsewhere)" ] \
  || die "the landing is not a fast-forward from the tip it was judged against"
[ "$(git -C "$origin" rev-parse refs/heads/main^{tree})" != "$(git rev-parse "$c2b^{tree}")" ] \
  || die "the tree did not actually move; the scenario proves nothing"
holds "the delta landed on a tree it was never attested over, and no check re-ran"

# Two attestation refs now, because the two statements are about two
# different trees: the contributor's is keyed by the tree it was made over,
# and the integrator's transfer statement by the tree that landed.
git fetch --quiet origin main
landed_digest="$(attest digest --repo "$repo" --rev "$(git -C "$origin" rev-parse refs/heads/main)" | cut -d: -f2)"
[ "$landed_digest" != "$attested" ] || die "the landed tree is the attested tree; the scenario proves nothing"
git -C "$origin" cat-file blob \
  "refs/the-valley/attestations/$attested/$ihash:prose-format/statement.note" > "$work/rebased.note"
[ "$(grep -c '^— ' "$work/rebased.note")" -eq 2 ] \
  || die "the countersigned note lost a sibling signature"
grep -qx "subject.digest.valley-tree-v1 $attested" "$work/rebased.note" \
  || die "the countersigned statement is not about the tree it was made over"
git -C "$origin" cat-file blob \
  "refs/the-valley/attestations/$landed_digest/$ihash:transfer.note" > "$work/rebased.transfer"
grep -qx "subject.digest.valley-tree-v1 $landed_digest" "$work/rebased.transfer" \
  || die "the transfer statement is not about the landed tree"
grep -qx "predicate.base.valley-tree-v1 $attested" "$work/rebased.transfer" \
  || die "the transfer statement does not cite the base the evidence was produced over"
grep -qx "predicate.checks.0.rule closure" "$work/rebased.transfer" \
  || die "the transfer statement does not record the rule that carried the evidence"
holds "the contributor's statement stays about its own tree; the transfer statement names the landed one"

# ----------------------------------------------------------------------
say "3. a conflicting delta is stale, and demands full re-attestation"

git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c3
echo "the readme, as c3 would have it" > docs/readme.md
git add -A
git commit --quiet -m "rewrite the readme one way"
attest_change c3 "" "" --check prose-format
c3="$(git rev-parse c3)"

git checkout --quiet main
git checkout --quiet -b conflicting
echo "the readme, as the other change would have it" > docs/readme.md
git add -A
git commit --quiet -m "rewrite the readme another way"
attest_change conflicting "" "" --check prose-format
request conflicting main "$(git rev-parse conflicting)"
integrate
grep -q "^conflicting -> refs/heads/main .*: land$" "$work/last.out" || die "the conflicting change did not land"

request c3 main "$c3"
integrate
grep -q "^c3 -> refs/heads/main .*: stale$" "$work/last.out" || die "c3 was not stale"
grep -q 'resolving it is new authorship' "$work/last.out" || die "the conflict was not reported as new authorship"
grep -q '"reason":"conflict"' "$work/last.out" || die "the event does not say conflict"
grep -q '"checks":\["prose-format"\]' "$work/last.out" || die "a conflict must re-demand every required check"
holds "a conflict is stale, and every required check is re-demanded"
forget c3

# ----------------------------------------------------------------------
say "4. an unknown signer's attestation does not transfer"

git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c4
echo "a readme signed by a stranger" > docs/readme.md
git add -A
git commit --quiet -m "a change a stranger attested"
attest_change c4 "$work/stranger" "$stranger" --check prose-format
request c4 main "$(git rev-parse c4)"
integrate
grep -q "^c4 -> refs/heads/main .*: stale$" "$work/last.out" || die "c4 was not stale"
grep -q 'no signer the integrator holds' "$work/last.out" || die "the unknown signer was not reported"
grep -q '"checks":\["prose-format"\]' "$work/last.out" || die "the event does not name prose-format"
[ "$(tip)" = "$(git -C "$origin" rev-parse refs/heads/main)" ] || die "main moved"
holds "evidence from a signer the integrator does not hold does not transfer"
forget c4

# ----------------------------------------------------------------------
say "5. a tampered statement is rejected, not called stale"

git checkout --quiet main
git checkout --quiet -b c5
echo "an honest readme" > docs/readme.md
git add -A
git commit --quiet -m "an honest change"
attest_change c5 "" "" --check prose-format
c5="$(git rev-parse c5)"
c5digest="$(attest digest --repo "$repo" --rev c5 | cut -d: -f2)"

# Edit one value in the signed text and store the result where the real
# note was. Everything else about the note is untouched.
c5ref="$(git -C "$origin" for-each-ref --format='%(refname)' \
  "refs/the-valley/attestations/$c5digest" | head -n1)"
git -C "$origin" cat-file blob "$c5ref:prose-format/statement.note" \
  | sed 's/^predicate.result passed$/predicate.result failed/' > "$work/tampered.note"
cmp -s "$work/tampered.note" <(git -C "$origin" cat-file blob "$c5ref:prose-format/statement.note") \
  && die "the tamper changed nothing; the test proves nothing"
blob="$(git -C "$origin" hash-object -w --stdin < "$work/tampered.note")"
sub="$(printf '100644 blob %s\tstatement.note\n' "$blob" | git -C "$origin" mktree)"
top="$(printf '040000 tree %s\tprose-format\n' "$sub" | git -C "$origin" mktree)"
git -C "$origin" update-ref "$c5ref" "$top"

request c5 main "$c5"
integrate
grep -q "^c5 -> refs/heads/main .*: reject$" "$work/last.out" || die "a tampered statement was not rejected"
grep -q 'does not verify as the statement it claims to be' "$work/last.out" || die "the refusal was not reported"
grep -q 'request-stale' "$work/last.out" && die "a forgery was announced as staleness"
holds "a tampered statement is a rejection, and no outcome event claims otherwise"
forget c5

# ----------------------------------------------------------------------
say "6. an attestation about a different tree is rejected"

git checkout --quiet main
git checkout --quiet -b c6
echo "the tree that was actually attested" > docs/readme.md
git add -A
git commit --quiet -m "the attested change"
attest_change c6 "" "" --check prose-format
c6ref="$(git -C "$origin" for-each-ref --format='%(refname)' \
  "refs/the-valley/attestations/$(attest digest --repo "$repo" --rev c6 | cut -d: -f2)" | head -n1)"

# A second, different tree — with no attestation of its own — and the
# first tree's valid, correctly signed note filed under its digest.
git checkout --quiet -b c6-lifted
echo "the tree that was not" > docs/readme.md
git add -A
git commit --quiet -m "the change the evidence was lifted onto"
git push --quiet origin c6-lifted
lifted="$(attest digest --repo "$repo" --rev c6-lifted | cut -d: -f2)"
git -C "$origin" update-ref \
  "refs/the-valley/attestations/$lifted/${c6ref##*/}" \
  "$(git -C "$origin" rev-parse "$c6ref^{tree}")"

request c6 main "$(git rev-parse c6-lifted)"
integrate
grep -q "^c6 -> refs/heads/main .*: reject$" "$work/last.out" || die "a lifted attestation was not rejected"
grep -q 'tree digest mismatch' "$work/last.out" || die "the refusal does not name the tree digest"
holds "a signature cannot be lifted onto a tree it was not made about"
forget c6

printf '\nintegrator-e2e: every scenario held\n'
