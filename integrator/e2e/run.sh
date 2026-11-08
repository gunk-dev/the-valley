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
# A scenario's controllers run in the background, and end with the run.
trap 'kill $(jobs -p) 2> /dev/null || true; wait || true; rm -rf "$work"' EXIT
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

export HOME="$work"
export GIT_CONFIG_NOSYSTEM=1
# Scratch goes where the run puts it, as it does under the unit, so what
# a pass leaves behind is observable rather than scattered through /tmp.
export TMPDIR="$work/tmp"
mkdir -p "$TMPDIR"
export VALLEY_FAKE_STORE="$work/store"
export PATH="$here:$PATH" # the nix stand-ins, ahead of anything real
# What the nix stand-in was asked to do, one line per call (integrator/e2e/nix).
export VALLEY_E2E_NIX_LOG="$work/nix.log"

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

mkdir -p docs src deploy checks policy/project
echo "the readme" > docs/readme.md
echo "the source" > src/main.txt
echo "the deployment" > deploy/target.txt
# What each check reads, which the nix stand-in turns into its closure.
echo docs > checks/prose-format.inputs
echo src > checks/code-build.inputs

# The project layer: it declares its own classes and checks above the
# floor, and declines nothing. It sits at policy/project, which is where the
# integrator looks without being told (dcr-f41f718).
cat > policy/project/policy.cue <<'EOF'
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
# reads it from there: a change never supplies the policy that gates it. The
# floor is the group's own repository's, read at the tip that has been
# integrated (dcr-f41f718), and it sits at policy/instance in that tip.
instance_repo="$work/instance.git"
git init --quiet --bare "$instance_repo"
floor="$work/instance-project"
git init --quiet "$floor"
mkdir -p "$floor/policy/instance"
cat > "$floor/policy/instance/floor.cue" <<'EOF'
package verification

floor: {
	checks: {}
	classes: {}
	unclassified: {}
}
EOF
git -C "$floor" add -A
git -C "$floor" commit --quiet -m "seed the floor"
git -C "$floor" push --quiet "$instance_repo" main

# The fallback source, for a host that serves no instance repository: the
# same documents, materialized as a directory. One scenario below reads the
# floor that way, and every other reads it from the repository.
instance="$work/instance"
mkdir -p "$instance"
cp "$floor/policy/instance/floor.cue" "$instance/floor.cue"

holds "the floor is policy/instance at $instance_repo's tip, and the project's layer is policy/project at its own"

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

# request <change> <target> <commit> — the contributor files an integration
# request by pushing one ref. That ref is the whole of the request.
request() {
  git push --quiet origin "$3:refs/the-valley/integration-requests/$2/$1"
}

# reconcile [flag…] — one pass. A flag given here overrides the one set
# before it.
reconcile() {
  # Neither policy path is stated: both layers come from where the
  # integrator defaults to looking, which is the whole of what a deployment
  # following the convention says about policy.
  integrator reconcile \
    --repo "$origin" \
    --key "$work/integrator" --name "$integrator_name" \
    --known-signers "$work/known_signers" \
    --instance-repo "$instance_repo" \
    --schema "$SCHEMA_VERIFICATION" \
    --attest-schema "$SCHEMA_ATTESTATION" \
    --event-schema "$SCHEMA_EVENTS" \
    ${VALLEY_E2E_NOW:+--now "$VALLEY_E2E_NOW"} \
    "$@"
}

# integrate [flag…] — one pass, its output kept in last.out and what it
# asked of the nix stand-in in last.nix. pre is main's tip on origin before
# the pass.
integrate() {
  pre="$(tip)"
  : > "$VALLEY_E2E_NIX_LOG"
  reconcile "$@" 2>&1 | tee "$work/last.out"
  cp "$VALLEY_E2E_NIX_LOG" "$work/last.nix"
  grep -q . "$work/last.out" || die "the integrator said nothing"
}

# asked_nix LINE… — the last pass asked the nix stand-in for exactly these
# calls, in this order, and for nothing else.
asked_nix() {
  [ "$(cat "$work/last.nix")" = "$(if [ $# -gt 0 ]; then printf '%s\n' "$@"; fi)" ] \
    || die "the pass asked nix for $(paste -sd, "$work/last.nix"), not $(IFS=,; echo "$*")"
}

tip() { git -C "$origin" rev-parse refs/heads/main; }
# landed_as HEAD BEFORE [REPO [NAME]] — main in REPO, origin by default,
# is the one commit the integrator signing as NAME made for the change
# whose head is HEAD, on BEFORE, the tip main was at before the pass:
# HEAD's tree, the integrator as committer, BEFORE as its one parent, and
# main one commit ahead of BEFORE. So none of HEAD's own commits reached
# main in the pass.
landed_as() {
  local in="${3:-$origin}" name="${4:-$integrator_name}" main
  main="$(git -C "$in" rev-parse refs/heads/main)"
  [ "$main" != "$1" ] || die "main is at $1 itself, not a commit the integrator made"
  [ "$(git -C "$in" rev-parse "$main^{tree}")" = "$(git -C "$in" rev-parse "$1^{tree}")" ] \
    || die "main does not hold the tree of $1"
  [ "$(git -C "$in" rev-list --no-walk --parents "$main")" = "$main $2" ] \
    || die "main's tip is not one commit whose one parent is $2, the tip before the pass"
  [ "$(git -C "$in" rev-list --count "$2..$main")" = 1 ] \
    || die "main gained more than one commit in the pass"
  [ "$(git -C "$in" log -1 --format=%cn "$main")" = "$name" ] \
    || die "main's tip was not committed by the integrator"
}
# transfer_path COMMIT — where an attestation ref holds the transfer
# statement for a landing as COMMIT (change.TransferPath).
transfer_path() { printf 'transfer.d/%s/%s/%s.note' "${1:0:2}" "${1:2:2}" "${1:4}"; }
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
# The landing's own ref-updated event, vetted against #RefUpdated like the rest.
grep -qF "event    ref-updated {\"event\":\"ref-updated\",\"repo\":" "$work/last.out" \
  || die "no ref-updated event"
[ "$(tip)" != "$before" ] || die "the protected ref did not move"
landed_as "$(git rev-parse c1)" "$pre"
asked_nix
holds "the tree that landed is the tree attested, so nothing was evaluated"
git -C "$origin" rev-parse --verify --quiet refs/the-valley/integration-requests/main/c1 > /dev/null \
  && die "the request ref outlived the landing"
holds "main moved $(git rev-parse --short "$before") -> $(git -C "$origin" rev-parse --short refs/heads/main)"

# The landing is recorded per head: a commit with no parent, holding one
# file that names the landed commit, the request and the head. The outcome
# record, written in the same transaction, says land.
record="refs/the-valley/integration-landings/main/$(git rev-parse c1)"
[ "$(git -C "$origin" cat-file blob "$record:landing")" = "$(printf 'landed %s\nrequest c1\nhead %s' "$(tip)" "$(git rev-parse c1)")" ] \
  || die "the landing record does not name the landing: $(git -C "$origin" cat-file blob "$record:landing" 2>&1)"
[ "$(git -C "$origin" rev-list --no-walk --parents "$record" | wc -w)" = 1 ] || die "the landing record's commit has a parent"
[ "$(git -C "$origin" cat-file blob refs/the-valley/integration-outcomes/main/c1 | cut -d' ' -f1,2,4)" = "$(git rev-parse c1) $before land" ] \
  || die "the outcome record does not say land at c1 on the tip it was judged against"
grep -qx "  record   $record" "$work/last.out" || die "the landing record was not reported"
holds "the landing record and the outcome record name the landing"

# The decision is followed by one timing line: JSON after a fixed label,
# naming the request and what was decided, with the time each stage took.
# Every stage a landing reaches is there, as a whole number of
# milliseconds, and the stages add up to no more than the whole. A pass
# run by hand is its process's only pass, which first saw the request.
[ "$(grep -c '^  timing   ' "$work/last.out")" = 1 ] || die "one decision did not print exactly one timing line"
timing="$(sed -n 's/^  timing   //p' "$work/last.out")"
jq -e --arg head "$(git rev-parse c1)" --arg tip "$before" '
  keys == ["change", "duration_ms", "first_seen", "first_seen_at_start", "first_seen_pass", "head", "listed_to_began_ms",
    "outcome", "pass", "pass_listed", "pass_reason", "process", "project", "request", "seen_to_began_ms", "stages_ms",
    "target", "time", "tip"]
  and .pass == 1 and .pass_reason == "reconcile" and .first_seen_pass == 1 and .first_seen_at_start == true
  and .first_seen == .pass_listed and .first_seen <= .time and .seen_to_began_ms == .listed_to_began_ms
  and .seen_to_began_ms >= 0 and (.process | type == "string" and length > 0)
  and .request == "refs/the-valley/integration-requests/main/c1" and .change == "c1"
  and .head == $head and .target == "refs/heads/main" and .tip == $tip and .outcome == "land"
  and (.project | type == "string" and length > 0)
  and (.time | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}[.][0-9]{3}Z$"))
  and (.duration_ms | type == "number" and . >= 0 and . == floor)
  and (.stages_ms | type == "object"
    and (keys - ["refusal", "checkout", "evidence", "policy", "apply", "closures", "sign", "land", "record", "publish"] == [])
    and all(.refusal, .checkout, .evidence, .policy, .sign, .land, .record, .publish; type == "number")
    and all(.[]; type == "number" and . >= 0 and . == floor))
  and ([.stages_ms[]] | add) <= .duration_ms' <<< "$timing" > /dev/null \
  || die "c1's timing line does not say what a landing's should: $timing"
holds "the landing is timed: $timing"

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
git -C "$origin" cat-file blob "$landed:$(transfer_path "$(tip)")" > "$work/transfer.note"
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
  || die "the request ref was dropped; there is nothing to approve again"
holds "one request-stale naming exactly code-build; prose-format reported transferred"
asked_nix "eval shared code-build prose-format"
holds "both checks were evaluated together on the moved tree, in one evaluation, and nothing was built"
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
intervened="$(tip)"

request c2b main "$c2b"
integrate
grep -q "^c2b -> refs/heads/main .*: land$" "$work/last.out" || die "c2b did not land"
grep -q "check    prose-format .*transferred (closure)" "$work/last.out" \
  || die "the evidence did not transfer by closure equality"
landedtree="$(git -C "$origin" rev-parse refs/heads/main^{tree})"
[ "$(git -C "$origin" rev-parse "refs/heads/main^")" = "$intervened" ] \
  || die "the landing is not a fast-forward from the tip it was judged against"
[ "$(git -C "$origin" rev-parse refs/heads/main^{tree})" != "$(git rev-parse "$c2b^{tree}")" ] \
  || die "the tree did not actually move; the scenario proves nothing"
holds "the delta landed on a tree it was never attested over, and no check re-ran"
asked_nix "eval prose-format"
holds "the one check was evaluated once, alone"

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
  "refs/the-valley/attestations/$landed_digest/$ihash:$(transfer_path "$(tip)")" > "$work/rebased.transfer"
grep -qx "subject.digest.valley-tree-v1 $landed_digest" "$work/rebased.transfer" \
  || die "the transfer statement is not about the landed tree"
grep -qx "predicate.base.valley-tree-v1 $attested" "$work/rebased.transfer" \
  || die "the transfer statement does not cite the base the evidence was produced over"
grep -qx "predicate.checks.0.rule closure" "$work/rebased.transfer" \
  || die "the transfer statement does not record the rule that carried the evidence"
holds "the contributor's statement stays about its own tree; the transfer statement names the landed one"

# ----------------------------------------------------------------------
say "2c. a re-parenting landing states its own committer, and needs none from the environment"

# Re-parenting is the one landing that makes a commit of the integrator's
# own, so it is the one that needs a committer. Under the unit there is
# nothing to take one from: no git config the service can read, no GIT_* in
# its environment, and a hostname git will not guess an address from. So the
# integrator states both identities itself — the asker authored the change,
# the integrator committed the commit that carries it onto the tip — and the
# pass below runs with everything git could have fallen back to taken away.

# A scrubbed environment: no global or system config, no GIT_* identity, and
# auto-detection refused outright, so that a host whose own hostname does
# resolve cannot hide a dependency on it.
scrubbed() {
  env -u GIT_AUTHOR_NAME -u GIT_AUTHOR_EMAIL -u GIT_AUTHOR_DATE \
    -u GIT_COMMITTER_NAME -u GIT_COMMITTER_EMAIL -u GIT_COMMITTER_DATE -u EMAIL \
    GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 \
    GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=user.useConfigOnly GIT_CONFIG_VALUE_0=true \
    "$@"
}
scrubbed git -C "$origin" var GIT_COMMITTER_IDENT > /dev/null 2>&1 \
  && die "the scrub left an identity for git to find; the scenario proves nothing"

git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c2c
echo "a readme revised by a stated author" > docs/readme.md
git add -A
# A distinct author, so that what the landed commit carries over from the
# head and what it states of its own are told apart by reading it.
GIT_AUTHOR_NAME="a contributor" GIT_AUTHOR_EMAIL="contributor@valley.invalid" \
  git commit --quiet -m "revise the readme under an author of its own"
attest_change c2c "" "" --check prose-format
c2c="$(git rev-parse c2c)"

# The intervening landing, outside what prose-format reads, is what moves
# c2c's base and makes the landing below a re-parent rather than a
# fast-forward.
git checkout --quiet main
git checkout --quiet -b under-c2c
echo "a fifth source file" > src/fifth.txt
git add -A
git commit --quiet -m "add a fifth source file"
attest_change under-c2c "" "" --check code-build
request under-c2c main "$(git rev-parse under-c2c)"
integrate
grep -q "^under-c2c -> refs/heads/main .*: land$" "$work/last.out" || die "the intervening change did not land"

request c2c main "$c2c"
pre="$(tip)"
scrubbed integrator reconcile \
  --repo "$origin" \
  --key "$work/integrator" --name "$integrator_name" \
  --known-signers "$work/known_signers" \
  --instance-repo "$instance_repo" \
  --schema "$SCHEMA_VERIFICATION" \
  --attest-schema "$SCHEMA_ATTESTATION" \
  --event-schema "$SCHEMA_EVENTS" \
  ${VALLEY_E2E_NOW:+--now "$VALLEY_E2E_NOW"} \
  2>&1 | tee "$work/last.out"
grep -q "^c2c -> refs/heads/main .*: land$" "$work/last.out" \
  || die "a landing with no git identity in the environment did not land: $(cat "$work/last.out")"

landed_commit="$(git -C "$origin" rev-parse refs/heads/main)"
[ "$(git -C "$origin" merge-base "$c2c" "$pre")" != "$pre" ] \
  || die "c2c's base is the tip it landed on, so main did not move under it; the scenario proves nothing"
[ "$(git -C "$origin" rev-list --no-walk --parents "$landed_commit")" = "$landed_commit $pre" ] \
  || die "c2c did not land as one commit on the tip before the pass"
[ "$(git -C "$origin" log -1 --format='%an <%ae>' "$landed_commit")" = "a contributor <contributor@valley.invalid>" ] \
  || die "the landed commit is not authored by the asker: $(git -C "$origin" log -1 --format='%an <%ae>' "$landed_commit")"
[ "$(git -C "$origin" log -1 --format='%cn <%ce>' "$landed_commit")" = "$integrator_name <integrator@the-valley.invalid>" ] \
  || die "the landed commit is not committed by the integrator: $(git -C "$origin" log -1 --format='%cn <%ce>' "$landed_commit")"
# The commit id is a function of what the pass decided from and nothing
# else, so a pass that dies before the ref write recomputes this same
# commit rather than leaving another behind.
[ "$(git -C "$origin" log -1 --format=%aI "$landed_commit")" = "$(git -C "$origin" log -1 --format=%cI "$landed_commit")" ] \
  || die "the committer date is not the author's, so the landed commit id is not reproducible"
holds "the integrator committed under its own name with nothing in the environment to take one from"

# ----------------------------------------------------------------------
say "2d. a check that does not evaluate on the moved tree is stale alone"

# c2d reads both classes. Landings under it then keep code-build from
# evaluating on the tree that would land, while prose-format still
# evaluates there: first with an error no evaluation can catch, which ends
# the shared evaluation of both, then by taking the check out of the tree.
# Each time the two checks together fail, each is evaluated alone, so
# prose-format still transfers and code-build alone is stale.
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c2d
echo "a readme revised beside the source" > docs/readme.md
echo "the source, revised beside the readme" > src/main.txt
git add -A
git commit --quiet -m "revise the readme and the source together"
attest_change c2d "" "" --check prose-format --check code-build
c2d="$(git rev-parse c2d)"

# land_unclassified BRANCH MESSAGE — commit what is staged, on BRANCH off
# main, and land it. It touches only checks/, which no class covers, so it
# requires no check.
land_unclassified() {
  git commit --quiet -m "$2"
  request "$1" main "$(git rev-parse "$1")"
  integrate
  grep -q "^$1 -> refs/heads/main .*: land$" "$work/last.out" || die "$1 did not land"
  git checkout --quiet main
  git pull --quiet --ff-only origin main
}

# c2d_is_stale_alone WHY — c2d, requested now, is stale, prose-format
# transfers, and code-build alone could not be recomputed, for WHY.
c2d_is_stale_alone() {
  request c2d main "$c2d"
  integrate
  grep -q "^c2d -> refs/heads/main .*: stale$" "$work/last.out" || die "c2d was not stale"
  grep -q "check    prose-format .*transferred (closure)" "$work/last.out" \
    || die "prose-format did not transfer beside the check that does not evaluate"
  grep -q "check    code-build .*could not be recomputed on the landed tree: attest: evaluating .*$1" "$work/last.out" \
    || die "code-build is not reported as not evaluating: $(grep 'check    code-build' "$work/last.out")"
  grep -q '"checks":\["code-build"\]' "$work/last.out" || die "the event does not name exactly code-build"
  forget c2d
}

git checkout --quiet main
git checkout --quiet -b uncaught
echo "error: code-build does not evaluate, and nothing can catch it" > checks/code-build.uncaught
git add -A
land_unclassified uncaught "break code-build beyond catching"
c2d_is_stale_alone "nothing can catch it"
# Together, then in the shared evaluation's place each alone, then each in
# a call of its own: 2N+1 evaluations of N checks, where it is N alone.
asked_nix "eval shared code-build prose-format" "eval code-build" "eval prose-format" \
  "eval code-build" "eval prose-format"
holds "an error that ends the shared evaluation leaves prose-format transferred and code-build alone stale"

git checkout --quiet -b missing
git rm --quiet checks/code-build.uncaught checks/code-build.inputs
land_unclassified missing "take code-build out of the tree"
c2d_is_stale_alone "declares no check code-build"
asked_nix "eval shared code-build prose-format" "eval code-build" "eval code-build" "eval prose-format"
holds "a check missing from the tree leaves prose-format transferred and code-build alone stale"

git checkout --quiet -b restored
echo src > checks/code-build.inputs
git add -A
land_unclassified restored "put code-build back"

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
grep -q 'CONFLICT (content): Merge conflict in docs/readme.md' "$work/last.out" \
  || die "the conflict does not name what could not be merged"
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

# ----------------------------------------------------------------------
say "7. an attestation of the wrong kind is no evidence"

# The policy runs prose-format with nix, so its evidence is content-
# addressed. This attestation is for a notarization instead, and its class
# was untouched by the intervening landing — so under the rule it nominated
# for itself it would sail onto a base it was never produced over. The
# integrator counts a note of the wrong kind as no evidence, so the request
# is stale.
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c7
echo "a readme attested as a notarization" > docs/readme.md
git add -A
git commit --quiet -m "a change whose attestation lies about its kind"
attest_change c7 "" "" --command prose-format=true
c7="$(git rev-parse c7)"

git checkout --quiet main
git checkout --quiet -b under-c7
echo "a fourth source file" > src/fourth.txt
git add -A
git commit --quiet -m "add a fourth source file"
attest_change under-c7 "" "" --check code-build
request under-c7 main "$(git rev-parse under-c7)"
integrate
grep -q "^under-c7 -> refs/heads/main .*: land$" "$work/last.out" || die "the intervening change did not land"

request c7 main "$c7"
integrate
grep -q "^c7 -> refs/heads/main .*: stale$" "$work/last.out" || die "a note of the wrong kind was not treated as no evidence"
grep -q 'requires a pure check and the attestation is for an effectful check' "$work/last.out" \
  || die "the verdict does not say the policy decides the rule"
holds "the transfer rule follows the policy's runner, never the attestation's own claim"
forget c7

# ----------------------------------------------------------------------
say "8. evidence pushed after a no-evidence verdict re-judges and lands"

git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c8
echo "a readme with no evidence yet" > docs/readme.md
git add -A
git commit --quiet -m "a change submitted before its checks ran"
git push --quiet origin c8
request c8 main "$(git rev-parse c8)"
integrate
grep -q "^c8 -> refs/heads/main .*: stale$" "$work/last.out" || die "a change with no evidence was not stale"
grep -q 'no attestation for this check accompanies the change' "$work/last.out" \
  || die "the absent attestation was not reported"

# Nothing about the request or the tip moves; only the evidence arrives.
attest_change c8 "" "" --check prose-format
integrate
grep -q "^c8 -> refs/heads/main .*: land$" "$work/last.out" \
  || die "the request was not re-judged after its evidence arrived"
landed_as "$(git rev-parse c8)" "$pre"
holds "a pass re-judges when evidence arrives, with the request and the tip unmoved"

# ----------------------------------------------------------------------
say "9. a ref that is not a request does not hold up one that is"

git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c9
echo "a readme beside a garbage ref" > docs/readme.md
git add -A
git commit --quiet -m "a change beside a malformed request"
attest_change c9 "" "" --check prose-format
# A name with no target segment at all, and one with a segment too many.
# This repository is served with no pre-receive hook, so the namespace takes
# whatever is pushed; on a host only a holder of the request grant writes
# it, and a holder can still write a name that is not a request.
git push --quiet origin "$(git rev-parse c9):refs/the-valley/integration-requests/garbage"
git push --quiet origin "$(git rev-parse c9):refs/the-valley/integration-requests/main/too/deep"
request c9 main "$(git rev-parse c9)"
integrate
grep -q 'refs/the-valley/integration-requests/garbage: not a request ref' "$work/last.out" \
  || die "the malformed ref was not reported"
grep -q 'refs/the-valley/integration-requests/main/too/deep: not a request ref' "$work/last.out" \
  || die "the over-deep ref was not reported"
grep -q "^c9 -> refs/heads/main .*: land$" "$work/last.out" \
  || die "a malformed ref stopped the pass reaching a valid request"
holds "malformed refs are reported and skipped; the valid request beside them landed"
git -C "$origin" update-ref -d refs/the-valley/integration-requests/garbage
git -C "$origin" update-ref -d refs/the-valley/integration-requests/main/too/deep

# ----------------------------------------------------------------------
say "10. a pass leaves no scratch behind, and a leaked worktree is swept"

# Nine scenarios of real passes, landings and rejections alike, and the
# scratch root is empty: every worktree a pass takes is given back on the
# way out, on the failure path as much as the success one.
leftover="$(find "$TMPDIR" -maxdepth 1 -name 'valley-integrator-wt*')"
[ -z "$leftover" ] || die "nine passes left scratch behind: $leftover"
[ "$(git -C "$origin" worktree list --porcelain | grep -c '^worktree ')" -eq 1 ] \
  || die "nine passes left a worktree attached to the repository"
holds "no pass left a worktree or a scratch directory behind"

# What a crash leaves: the checkout and its administrative entry, both
# still valid, so nothing prunes them. Beside it, a worktree of the kind
# an operator might attach by hand, which is not the integrator's to
# remove.
leak="$TMPDIR/valley-integrator-wt-crashed"
mine="$work/an-operators-own-worktree"
git -C "$origin" worktree add --quiet --detach "$leak" refs/heads/main
git -C "$origin" worktree add --quiet --detach "$mine" refs/heads/main
timeout 5 integrator watch --interval 1s \
  --repo "$origin" \
  --key "$work/integrator" --name "$integrator_name" \
  --known-signers "$work/known_signers" \
  --instance "$instance" \
  --schema "$SCHEMA_VERIFICATION" \
  --attest-schema "$SCHEMA_ATTESTATION" \
  --event-schema "$SCHEMA_EVENTS" \
  ${VALLEY_E2E_NOW:+--now "$VALLEY_E2E_NOW"} \
  > "$work/watch.out" 2>&1 || true
[ ! -e "$leak" ] || die "a leaked scratch worktree survived the sweep"
if git -C "$origin" worktree list --porcelain | grep -q "^worktree $leak$"; then
  die "the leaked worktree is gone from disk and still registered"
fi
[ -e "$mine" ] || die "the sweep removed a worktree the integrator did not make"
git -C "$origin" worktree remove --force "$mine"
holds "a crash-leaked scratch worktree is swept at watch start; another hand's is not"

# ----------------------------------------------------------------------
say "11. a scratch root it may not write is reported as that, and nothing else"

# The deployment failure this guards is a sandbox failure: the root TMPDIR
# names is not writable. That is a statement about the unit, not about the
# change being judged, and it has to arrive as one — with the path and where
# the path came from — rather than as git complaining about the leading
# directories of a .git file.
refused="$work/refused"
mkdir -p "$refused"
chmod a-w "$refused"
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c10
echo "a change that never gets a worktree" > docs/readme.md
git add -A
git commit --quiet -m "a change the integrator cannot make scratch for"
attest_change c10 "" "" --check prose-format
request c10 main "$(git rev-parse c10)"
export TMPDIR="$refused"
integrate || true
export TMPDIR="$work/tmp"
chmod u+w "$refused"
grep -q "scratch directory under $refused, which TMPDIR names" "$work/last.out" \
  || die "an unwritable scratch root was not reported as one: $(cat "$work/last.out")"
forget c10
holds "an unwritable scratch root names the path and where it came from"

# ----------------------------------------------------------------------
say "12. a floor the instance repository does not carry names the directory looked in"

# The deployment failure this guards is a controller pointed at the wrong
# directory: the floor is somewhere, and not where this controller looked.
# The report has to name the repository, the directory and the ref, because
# those three are what the operator compares against the repository in front
# of them. A floor is either not there at all, or there under a name that
# holds no documents, and both arrive that way.
#
# A missing floor is refused rather than read as requiring nothing, which is
# the direction the other layer's absence defaults in (dcr-f41f718). The
# refusal says why in its own sentence, because the operator reading it is
# being told that the deployment is not one that can land anything.
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c11
echo "a change judged under no floor" > docs/readme.md
git add -A
git commit --quiet -m "a change whose floor the controller cannot find"
attest_change c11 "" "" --check prose-format
request c11 main "$(git rev-parse c11)"

# The sentence every refusal to find a floor ends with.
unwritten="a valley without a written floor is not a valley whose changes land"

floorless() {
  integrator reconcile \
    --repo "$origin" \
    --key "$work/integrator" --name "$integrator_name" \
    --known-signers "$work/known_signers" "$@" \
    --schema "$SCHEMA_VERIFICATION" \
    --attest-schema "$SCHEMA_ATTESTATION" \
    --event-schema "$SCHEMA_EVENTS" \
    ${VALLEY_E2E_NOW:+--now "$VALLEY_E2E_NOW"} \
    2>&1 | tee "$work/last.out"
}

# The project's own repository carries policy/project and no policy/instance,
# so the default directory is not there.
floorless --instance-repo "$origin" || true
grep -qF "no instance policy layer: $origin has no policy/instance/ at refs/heads/main: $unwritten" "$work/last.out" \
  || die "a missing floor did not name the directory looked in, and why that is refused: $(cat "$work/last.out")"

# And the flat layout, which is a directory that exists and holds no
# documents of its own: policy/ has the two layers under it and no *.cue in
# it. Said as that, rather than as an empty composition further on.
#
# Under a second request name, because the refusal above is now this
# controller's recorded answer to c11 and it stands until the request, the
# tip, the evidence or the inputs move — which is scenario 14's whole
# subject. A request is what an answer is remembered against, so a second
# one asks the question again.
request c11-flat main "$(git rev-parse c11)"
floorless --instance-repo "$origin" --instance-policy policy || true
grep -qF "no instance policy layer: $origin holds no *.cue in policy/ at refs/heads/main: $unwritten" "$work/last.out" \
  || die "a floor directory holding no documents did not say so: $(cat "$work/last.out")"

# And the materialized layer, which is how a host that serves no instance
# repository is given the floor. A directory with nothing in it is the same
# absence and is refused the same way.
request c11-bare main "$(git rev-parse c11)"
nofloor="$work/no-floor-here"
mkdir -p "$nofloor"
floorless --instance "$nofloor" || true
grep -qF "no instance policy layer: $nofloor holds no *.cue: $unwritten" "$work/last.out" \
  || die "an empty materialized floor was not refused: $(cat "$work/last.out")"
forget c11
forget c11-flat
forget c11-bare
holds "a floor that is not where the controller looked names the repository, the directory and the ref"

# ----------------------------------------------------------------------
say "13. a change the policy requires no check of lands, and says so"

# Requiring nothing is an ordinary outcome, not an edge: this project's
# classes cover docs/, src/ and deploy/, and its `unclassified` set is
# empty, so a path outside all three is answered with nothing required. The
# landing still writes a transfer statement — a commit point that records
# nothing is a landing nothing attests to — and that statement says the
# required set was empty rather than carrying a list with nothing in it,
# which has no written form at all.
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c12
echo "outside every class this policy covers" > notes.txt
git add -A
git commit --quiet -m "a change no class covers"
request c12 main "$(git rev-parse c12)"
integrate
grep -q '^c12 -> refs/heads/main .*: land$' "$work/last.out" \
  || die "a change with nothing required did not land: $(cat "$work/last.out")"
grep -q 'the policy requires no check of the paths this change touches' "$work/last.out" \
  || die "the verdict did not say the policy required nothing"
landed_as "$(git rev-parse c12)" "$pre"
git -C "$origin" rev-parse --verify --quiet "refs/the-valley/integration-requests/main/c12" > /dev/null \
  && die "the request ref survived the landing"

nothing="refs/the-valley/attestations/$(attest digest --repo "$repo" --rev c12 | cut -d: -f2)/$ihash"
git -C "$origin" cat-file blob "$nothing:$(transfer_path "$(tip)")" > "$work/nothing.note"
grep -qx 'predicate.required none' "$work/nothing.note" \
  || die "the transfer statement does not say the required set was empty: $(cat "$work/nothing.note")"
grep -q '^predicate.checks' "$work/nothing.note" \
  && die "the transfer statement carries a check list beside the empty-set line"
attest verify --note "$work/nothing.note" --known-keys "$work/reader_keys" \
  --repo "$repo" --rev c12 --signer "$integrator_name" > /dev/null \
  || die "the transfer statement for a landing with nothing required does not verify"
holds "a landing with no required checks is signed, stored, and consumed its request"

# ----------------------------------------------------------------------
say "14. a request the controller cannot judge is reported once, not every tick"

# The failure this guards is the one that turns a broken deployment into a
# broken deployment nobody can read: a pass that cannot act on its verdict,
# retried at tick rate, filling the journal with one refusal per interval
# forever. The same request, tip and evidence produce the same failure, so
# repeating it says nothing new. It is reported once and then the answer
# stands, exactly as an outcome stands.
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c13
echo "a change judged under no floor, repeatedly" > docs/readme.md
git add -A
git commit --quiet -m "a change whose floor the controller cannot find, under watch"
attest_change c13 "" "" --check prose-format
request c13 main "$(git rev-parse c13)"

# Five seconds at a one-second tick: five passes over one request the
# controller cannot judge.
timeout 5 integrator watch --interval 1s \
  --repo "$origin" \
  --key "$work/integrator" --name "$integrator_name" \
  --known-signers "$work/known_signers" \
  --instance-repo "$origin" --instance-policy policy \
  --schema "$SCHEMA_VERIFICATION" \
  --attest-schema "$SCHEMA_ATTESTATION" \
  --event-schema "$SCHEMA_EVENTS" \
  ${VALLEY_E2E_NOW:+--now "$VALLEY_E2E_NOW"} \
  > "$work/repeat.out" 2>&1 || true
reports="$(grep -c 'no instance policy layer' "$work/repeat.out" || true)"
[ "$reports" -eq 1 ] \
  || die "the same refusal was reported $reports times over five ticks: $(cat "$work/repeat.out")"
forget c13
holds "a failure a pass cannot get past is reported once and then backs off"

# ----------------------------------------------------------------------
say "14b. a failure is recorded before its timing line, which nobody has to read"

# The timing line is printed after the outcome is recorded, and is dropped
# when it cannot be written. A pass that cannot act on its verdict prints
# nothing before its timing line, so with standard output a pipe whose
# reader has gone, the timing line is the first write to fail. The failure
# is recorded all the same. The line the integrator prints after it, naming
# the failure, is its own output rather than timing, and may still end the
# pass there.
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c14b
echo "a change judged with nobody reading" > docs/readme.md
git add -A
git commit --quiet -m "a change judged with nobody reading the integrator"
attest_change c14b "" "" --check prose-format
request c14b main "$(git rev-parse c14b)"

# A pipe with no reader. The FIFO is opened to read and write, so opening it
# to write does not wait; closing the first leaves the writer alone.
mkfifo "$work/unread"
exec 3<> "$work/unread"
exec 4> "$work/unread"
exec 3<&-
status=0
timeout 120 integrator reconcile \
  --repo "$origin" \
  --key "$work/integrator" --name "$integrator_name" \
  --known-signers "$work/known_signers" \
  --instance-repo "$origin" --instance-policy policy \
  --schema "$SCHEMA_VERIFICATION" \
  --attest-schema "$SCHEMA_ATTESTATION" \
  --event-schema "$SCHEMA_EVENTS" \
  ${VALLEY_E2E_NOW:+--now "$VALLEY_E2E_NOW"} \
  >&4 2> "$work/unread.err" || status=$?
exec 4>&-
[ "$status" != 124 ] || die "the pass did not finish with nobody reading its output"
recorded="$(git -C "$origin" cat-file blob refs/the-valley/integration-outcomes/main/c14b 2> /dev/null || true)"
[ "$(echo "$recorded" | cut -d' ' -f4)" = failed ] \
  || die "with nobody reading its output, the pass did not record its failure (exit $status): ${recorded:-no record} $(cat "$work/unread.err")"
forget c14b
holds "with nobody reading its output, the pass recorded its failure before its timing line (exit $status)"

# ----------------------------------------------------------------------
say "15. a target that carries no project layer of its own is judged under the floor alone"

# The bootstrap this guards is the first policy commit: a project that has
# not written a layer yet cannot land the commit that writes one, because a
# change never supplies the policy that gates it. The project layer only
# ever adds (dcr-f41f718), so its absence adds nothing, and what is left is
# the floor — which still governs, and is observed governing here.

# The floor grows a requirement of its own first, so that what remains after
# the project layer goes away is something rather than nothing.
cat > "$floor/policy/instance/floor.cue" <<'EOF'
package verification

floor: {
	checks: "prose-format": {runner: "nix", attribute: "prose-format"}
	classes: prose: {paths: "docs/**": true, requires: "prose-format": true}
	unclassified: {}
}
EOF
git -C "$floor" commit --quiet -am "the floor requires prose-format of docs/"
git -C "$floor" push --quiet "$instance_repo" main

# The project stops carrying a layer. Nothing classifies policy/project
# itself, so this lands with nothing required — which is how a project could
# have arrived without one in the first place.
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c14
git rm --quiet -r policy/project
git commit --quiet -m "stop carrying a project policy layer"
request c14 main "$(git rev-parse c14)"
integrate
grep -q "^c14 -> refs/heads/main .*: land$" "$work/last.out" \
  || die "dropping the project layer did not land: $(cat "$work/last.out")"

git checkout --quiet main
git pull --quiet --ff-only origin main
[ ! -e policy/project ] || die "the project layer is still at the tip; the scenario proves nothing"
git checkout --quiet -b c15
echo "a readme judged under the floor alone" > docs/readme.md
git add -A
git commit --quiet -m "a change to a project that declares no policy of its own"
attest_change c15 "" "" --check prose-format
request c15 main "$(git rev-parse c15)"
integrate
grep -q "^c15 -> refs/heads/main .*: land$" "$work/last.out" \
  || die "a change to a project with no layer of its own did not land: $(cat "$work/last.out")"
grep -qF "policy   no project layer at policy/project; the floor alone" "$work/last.out" \
  || die "the pass did not say it composed the floor alone"
grep -q "check    prose-format .*transferred" "$work/last.out" \
  || die "the floor's own requirement did not govern a project with no layer"
landed_as "$(git rev-parse c15)" "$pre"
holds "no project layer is no addition; the floor alone required prose-format and the change landed"

# ----------------------------------------------------------------------
say "16. a floor that requires nothing, written down, composes and judges"

# Emptiness a floor states is not the same thing as a floor nobody wrote.
# The document is there, somebody landed it, and what it composes to is a
# policy that requires nothing — so a change under it lands requiring nothing,
# rather than being refused the way scenario 12's missing floor is.
cat > "$floor/policy/instance/floor.cue" <<'EOF'
package verification

floor: {
	checks: {}
	classes: {}
	unclassified: {}
}
EOF
git -C "$floor" commit --quiet -am "the floor requires nothing, and says so"
git -C "$floor" push --quiet "$instance_repo" main

git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c16
echo "a readme under a floor that requires nothing" > docs/readme.md
git add -A
git commit --quiet -m "a change under an empty floor and no project layer"
request c16 main "$(git rev-parse c16)"
integrate
grep -q "^c16 -> refs/heads/main .*: land$" "$work/last.out" \
  || die "a change under an explicitly empty floor did not land: $(cat "$work/last.out")"
grep -q 'the policy requires no check of the paths this change touches' "$work/last.out" \
  || die "the verdict did not say the policy required nothing"
grep -qF "policy   no project layer at policy/project; the floor alone" "$work/last.out" \
  || die "the pass did not say it composed the floor alone"
landed_as "$(git rev-parse c16)" "$pre"
holds "a floor that states it requires nothing composes, judges, and lands the change requiring nothing"

# ----------------------------------------------------------------------
say "17. a planted replacement ref changes nothing the controller reads"

# refs/replace/<id> makes git read another object wherever it looks <id> up.
# Two are planted here, and each alone would land a change that requires
# evidence and carries none. One swaps the floor's blob in the instance
# repository for a floor that requires nothing. The other swaps the change's
# head in the served repository for a commit that touches no class. Neither
# moves a ref the controller watches. A controller that left replacement
# refs on would read both and land the change; this one reads the objects
# the refs actually name, and holds the change stale.
cat > "$floor/policy/instance/floor.cue" <<'EOF'
package verification

floor: {
	checks: "prose-format": {runner: "nix", attribute: "prose-format"}
	classes: prose: {paths: "docs/**": true, requires: "prose-format": true}
	unclassified: {}
}
EOF
git -C "$floor" commit --quiet -am "the floor requires prose-format of docs/ again"
git -C "$floor" push --quiet "$instance_repo" main
landed_floor="$(git -C "$instance_repo" rev-parse main:policy/instance/floor.cue)"
empty_floor="$(git -C "$instance_repo" rev-parse main~1:policy/instance/floor.cue)"
git -C "$instance_repo" update-ref "refs/replace/$landed_floor" "$empty_floor"
git -C "$instance_repo" show main:policy/instance/floor.cue | grep 'checks: {}' > /dev/null \
  || die "the floor's replacement did not take, so this scenario proves nothing"

git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c17-decoy
echo "a note no class covers" > notes.txt
git commit --quiet -am "a decoy that touches no class"
git checkout --quiet main
git checkout --quiet -b c17
echo "a readme no evidence covers" > docs/readme.md
git commit --quiet -am "a change that requires prose-format and carries no evidence"
git push --quiet origin c17 c17-decoy
git -C "$origin" update-ref "refs/replace/$(git rev-parse c17)" "$(git rev-parse c17-decoy)"
git -C "$origin" diff --name-only main c17 | grep -x notes.txt > /dev/null \
  || die "the head's replacement did not take, so this scenario proves nothing"
request c17 main "$(git rev-parse c17)"

# The environment asks for replacement refs too, and is not listened to.
before="$(tip)"
GIT_CONFIG_PARAMETERS="'core.usereplacerefs'='true'" integrate
grep -q "^c17 -> refs/heads/main .*: stale$" "$work/last.out" \
  || die "a planted replacement ref decided the verdict: $(cat "$work/last.out")"
grep -q "check    prose-format" "$work/last.out" \
  || die "the landed floor's requirement was not what the controller read"
grep -q 'no attestation for this check accompanies the change' "$work/last.out" \
  || die "the missing evidence was not reported"
[ "$(tip)" = "$before" ] || die "main moved under a change that requires evidence"
git -C "$instance_repo" update-ref -d "refs/replace/$landed_floor"
git -C "$origin" update-ref -d "refs/replace/$(git rev-parse c17)"
forget c17
holds "the floor and the change were read as landed and as pushed, past both replacements"

# ----------------------------------------------------------------------
say "18. a request approved again while its previous head lands stands for the next pass"

# A second approval is leased against the request it read, and
# nothing stops it reading the head this pass is about to land. So the
# request can move between the pass reading it and the pass consuming it.
# The landing is still the old head's, and the request that moved is a new
# request: consuming it would drop a change nobody judged.
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c18
echo "a readme requested once" > docs/readme.md
git commit --quiet -am "the first request"
attest_change c18 "" "" --check prose-format
request c18 main "$(git rev-parse c18)"
# main moves under the request, outside anything the check reads. The pass
# then recomputes the check's closure over the landed tree, and that
# evaluation is where the second approval happens.
git checkout --quiet main
echo "a note landed in between" > notes.txt
git commit --quiet -am "a note landed in between"
git push --quiet origin main
git checkout --quiet c18
git checkout --quiet -b c18-again
echo "a readme requested again" > docs/readme.md
git commit --quiet -am "the second request"
git push --quiet origin c18-again
again="$(git rev-parse c18-again)"
moving="refs/the-valley/integration-requests/main/c18"
before="$(tip)"
VALLEY_E2E_DURING_PASS="git -C '$origin' update-ref '$moving' '$again'" integrate
grep -q "^c18 -> refs/heads/main .*: land$" "$work/last.out" \
  || die "the first request did not land: $(cat "$work/last.out")"
[ "$(tip)" != "$before" ] || die "main did not move for the first request"
[ "$(git -C "$origin" rev-parse --verify --quiet "$moving")" = "$again" ] \
  || die "the request approved again was consumed with the landing of the one before it: $(git -C "$origin" for-each-ref refs/the-valley/integration-requests) $(cat "$work/last.out")"
grep -q "request  $moving moved to .* while .* landed; it stands for the next pass" "$work/last.out" \
  || die "the moved request was not reported: $(cat "$work/last.out")"
forget c18
holds "a request that moved while its previous head landed was left standing, and said so"

# ----------------------------------------------------------------------
say "19. a bus that never answers does not hold up a landing"

# The landing's own record comes first: main moves, the move is queued for
# the mirrors, the evidence is stored and the request consumed. Only then
# does the integrator publish, and each publish is cut off at its deadline.
# The shipped wrapper puts the real nats first on the path, so the bare
# binary runs here, behind a nats that never exits.
mkdir "$work/hung-bus" "$work/queue" "$work/wake"
cat > "$work/hung-bus/nats" <<EOF
#!$(command -v bash)
exec sleep 1000
EOF
chmod +x "$work/hung-bus/nats"
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c19
echo "landed past a bus that never answers" > docs/bus.md
git add -A
git commit --quiet -m "past a hung bus"
attest_change c19 "" "" --check prose-format
request c19 main "$(git rev-parse c19)"
before="$(tip)"
started=$SECONDS
PATH="$work/hung-bus:$PATH" timeout 120 "$INTEGRATOR_UNWRAPPED" reconcile \
  --repo "$origin" \
  --key "$work/integrator" --name "$integrator_name" \
  --known-signers "$work/known_signers" \
  --instance-repo "$instance_repo" \
  --schema "$SCHEMA_VERIFICATION" \
  --attest-schema "$SCHEMA_ATTESTATION" \
  --event-schema "$SCHEMA_EVENTS" \
  --bus nats://127.0.0.1:1 \
  --publish-queue "$work/queue" \
  --wake "$work/wake" \
  2>&1 | tee "$work/last.out" \
  || die "the pass did not finish with a bus that never answers: $(cat "$work/last.out")"
elapsed=$((SECONDS - started))
grep -q "^c19 -> refs/heads/main .*: land$" "$work/last.out" || die "c19 did not land: $(cat "$work/last.out")"
landed_as "$(git rev-parse c19)" "$before"
[ "$(cat "$work"/queue/*)" = "$before $(tip) refs/heads/main" ] \
  || die "the move was not queued for the mirrors: $(ls -A "$work/queue")"
git -C "$origin" rev-parse --verify --quiet refs/the-valley/integration-requests/main/c19 > /dev/null \
  && die "the request outlived the landing"
grep -q "^  evidence " "$work/last.out" || die "the evidence was not stored"
first_evidence="$(grep -n "^  evidence " "$work/last.out" | head -n1 | cut -d: -f1)"
first_bus="$(grep -n "^  bus " "$work/last.out" | head -n1 | cut -d: -f1)"
[ "$first_evidence" -lt "$first_bus" ] || die "the integrator asked the bus before it stored the evidence"
grep -q "bus      publish of valley.git.project.ref-updated FAILED: no answer within 10s" "$work/last.out" \
  || die "the hung ref-updated publish was not cut off: $(cat "$work/last.out")"
grep -q "bus      publish of valley.git.project.integration-succeeded FAILED: no answer within 10s" "$work/last.out" \
  || die "the hung integration-succeeded publish was not cut off"
[ "$elapsed" -lt 60 ] || die "the pass took ${elapsed}s behind a bus that never answers"
# The check worker is marked due by the landing, before the mirror queue
# and the bus, and the wake directory holds the one empty file.
[ "$(ls -A "$work/wake")" = due ] && [ ! -s "$work/wake/due" ] \
  || die "the landing did not leave one empty due: $(ls -A "$work/wake")"
first_wake="$(grep -n "^  wake     the check worker is due$" "$work/last.out" | head -n1 | cut -d: -f1)"
first_queue="$(grep -n "^  publish  " "$work/last.out" | head -n1 | cut -d: -f1)"
[ -n "$first_wake" ] && [ "$first_wake" -lt "$first_queue" ] && [ "$first_wake" -lt "$first_bus" ] \
  || die "the worker was not marked due before the mirror queue and the bus: $(cat "$work/last.out")"
holds "c19 marked the check worker due before its mirror queue and its hung bus"
holds "c19 landed, queued and consumed its request before a hung bus was asked, and each publish gave up after 10s (${elapsed}s in all)"

# ----------------------------------------------------------------------
say "20. a cue that never answers a payload's vet does not hold up a landing"

# The payload is vetted before it is published, and the vet is bounded the
# same way. This cue hangs only when asked to vet against the event schema;
# every other use, judging included, goes to the real one.
real_cue="$(command -v cue)"
mkdir "$work/hung-cue" "$work/queue20"
cat > "$work/hung-cue/cue" <<EOF
#!$(command -v bash)
for arg; do [ "\$arg" = "$SCHEMA_EVENTS" ] && exec sleep 1000; done
exec "$real_cue" "\$@"
EOF
chmod +x "$work/hung-cue/cue"
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c20
echo "landed past a cue that never answers" > docs/cue.md
git add -A
git commit --quiet -m "past a hung cue"
attest_change c20 "" "" --check prose-format
request c20 main "$(git rev-parse c20)"
before="$(tip)"
started=$SECONDS
PATH="$work/hung-cue:$PATH" timeout 120 "$INTEGRATOR_UNWRAPPED" reconcile \
  --repo "$origin" \
  --key "$work/integrator" --name "$integrator_name" \
  --known-signers "$work/known_signers" \
  --instance-repo "$instance_repo" \
  --schema "$SCHEMA_VERIFICATION" \
  --attest-schema "$SCHEMA_ATTESTATION" \
  --event-schema "$SCHEMA_EVENTS" \
  --publish-queue "$work/queue20" \
  2>&1 | tee "$work/last.out" \
  || die "the pass did not finish with a cue that never answers: $(cat "$work/last.out")"
elapsed=$((SECONDS - started))
grep -q "^c20 -> refs/heads/main .*: land$" "$work/last.out" || die "c20 did not land: $(cat "$work/last.out")"
landed_as "$(git rev-parse c20)" "$before"
[ "$(cat "$work"/queue20/*)" = "$before $(tip) refs/heads/main" ] \
  || die "the move was not queued for the mirrors: $(ls -A "$work/queue20")"
git -C "$origin" rev-parse --verify --quiet refs/the-valley/integration-requests/main/c20 > /dev/null \
  && die "the request outlived the landing"
grep -q "^  evidence " "$work/last.out" || die "the evidence was not stored"
grep -q "bus      ref-updated not published: cue vet against #RefUpdated gave no answer within 10s" "$work/last.out" \
  || die "the hung vet of ref-updated was not cut off: $(cat "$work/last.out")"
grep -q "bus      integration-succeeded not published: cue vet against #IntegrationSucceeded gave no answer within 10s" "$work/last.out" \
  || die "the hung vet of integration-succeeded was not cut off"
[ "$elapsed" -lt 60 ] || die "the pass took ${elapsed}s behind a cue that never answers"
holds "c20 landed, queued and consumed its request, and each hung vet gave up after 10s and skipped its publish (${elapsed}s in all)"

# ----------------------------------------------------------------------
say "21. a request named for a change that is ready lands"

# A change with a record is requested as c-<change ID>. The records are
# written by the valley command itself, as its owner would write them; this
# repository is served without the hook that guards them on a host. The
# integrator reads them only to refuse.
git config valley.principal contributor
# ready_change <branch>: the branch's head on origin readied as a new
# change, whose ID is left in $readied.
ready_change() {
  valley ready "$1" > "$work/ready.out" || die "valley ready $1 failed"
  readied="$(sed -n 's/^The change ID \([k-z]*\) is new\..*/\1/p' "$work/ready.out")"
  [ "${#readied}" = 32 ] || die "valley ready $1 made no change ID: $(cat "$work/ready.out")"
}
outcome_of() { git -C "$origin" cat-file blob "refs/the-valley/integration-outcomes/main/$1" | cut -d' ' -f4; }

git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c21
echo "a change with a record" > docs/c21.md
git add -A
git commit --quiet -m "a change with a record"
attest_change c21 "" "" --check prose-format
ready_change c21
id21="$readied"
request "c-$id21" main "$(git rev-parse c21)"
integrate
grep -q "^c-$id21 -> refs/heads/main .*: land$" "$work/last.out" || die "c-$id21 did not land"
landed_as "$(git rev-parse c21)" "$pre"
landed="refs/the-valley/attestations/$(attest digest --repo "$repo" --rev c21 | cut -d: -f2)/$ihash"
git -C "$origin" cat-file blob "$landed:$(transfer_path "$(tip)")" | grep -qx "predicate.change c-$id21" \
  || die "the transfer statement does not name the request c-$id21"
holds "a ready change's request, named for its ID, landed, and its transfer statement names it so"

# ----------------------------------------------------------------------
say "22. a change unreadied after its request was filed is refused, and lands once it is ready again"

git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c22
echo "a change paused after it was approved" > docs/c22.md
git add -A
git commit --quiet -m "a change paused after it was approved"
attest_change c22 "" "" --check prose-format
ready_change c22
id22="$readied"
request "c-$id22" main "$(git rev-parse c22)"
valley unready "$id22" > /dev/null 2>&1 || die "valley unready failed"
before="$(tip)"
integrate
grep -q "^c-$id22 -> refs/heads/main .*: refused-not-ready$" "$work/last.out" \
  || die "a draft's request was not refused: $(cat "$work/last.out")"
grep -q "change $id22 is draft, not ready" "$work/last.out" || die "the refusal did not say the change is a draft"
[ "$(tip)" = "$before" ] || die "a draft's request moved main"
git -C "$origin" rev-parse --verify --quiet "refs/the-valley/integration-requests/main/c-$id22" > /dev/null \
  || die "the refused request was dropped"
[ "$(outcome_of "c-$id22")" = refused-not-ready ] || die "the outcome record does not say refused-not-ready"

# The refusal is decided again on every pass, and said once.
timeout 4 integrator watch --interval 1s \
  --repo "$origin" \
  --key "$work/integrator" --name "$integrator_name" \
  --known-signers "$work/known_signers" \
  --instance-repo "$instance_repo" \
  --schema "$SCHEMA_VERIFICATION" \
  --attest-schema "$SCHEMA_ATTESTATION" \
  --event-schema "$SCHEMA_EVENTS" \
  > "$work/repeat.out" 2>&1 || true
[ "$(grep -c "c-$id22 .*refused-not-ready" "$work/repeat.out" || true)" -eq 0 ] \
  || die "a standing refusal was reported again: $(cat "$work/repeat.out")"
[ "$(tip)" = "$before" ] || die "a draft's request moved main under watch"

# Readied again at the same head, the same request lands on the next pass.
valley ready --change "$id22" c22 > /dev/null 2>&1 || die "valley ready --change $id22 failed"
integrate
grep -q "^c-$id22 -> refs/heads/main .*: land$" "$work/last.out" \
  || die "the change readied again did not land: $(cat "$work/last.out")"
landed_as "$(git rev-parse c22)" "$pre"
holds "unready stopped a filed request, the refusal was said once, and ready let the same request land"

# ----------------------------------------------------------------------
say "23. a request named for a branch, at a draft's head, is refused"

# Whatever a request is named, a head that is a version of a change that
# is not ready does not land.
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c23
echo "a draft requested under its branch's name" > docs/c23.md
git add -A
git commit --quiet -m "a draft requested under its branch's name"
attest_change c23 "" "" --check prose-format
ready_change c23
id23="$readied"
valley unready "$id23" > /dev/null 2>&1 || die "valley unready failed"
request c23 main "$(git rev-parse c23)"
before="$(tip)"
integrate
grep -q "^c23 -> refs/heads/main .*: refused-not-ready$" "$work/last.out" \
  || die "a branch-named request at a draft's head was not refused: $(cat "$work/last.out")"
grep -q "is v1 of change $id23, which is draft, not ready" "$work/last.out" \
  || die "the refusal did not name the draft"
[ "$(tip)" = "$before" ] || die "a draft's head landed under its branch's name"
[ "$(outcome_of c23)" = refused-not-ready ] || die "the outcome record does not say refused-not-ready"
forget c23
holds "a draft's head was refused under a branch's name too"

# ----------------------------------------------------------------------
say "24. a request whose commits hold a merge is refused"

# A merge can make other work look landed by ancestry, so the integrator
# lands a line of commits and nothing else.
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c24-side
echo "a side line" > docs/c24-side.md
git add -A
git commit --quiet -m "a side line"
git checkout --quiet main
git checkout --quiet -b c24
echo "a line with a merge in it" > docs/c24.md
git add -A
git commit --quiet -m "a line with a merge in it"
git merge --quiet --no-ff --no-edit c24-side
git push --quiet origin c24
request c24 main "$(git rev-parse c24)"
before="$(tip)"
integrate
grep -q "^c24 -> refs/heads/main .*: refused-merge$" "$work/last.out" \
  || die "a request holding a merge was not refused: $(cat "$work/last.out")"
grep -q "include the merge $(git rev-parse c24 | cut -c1-12)" "$work/last.out" || die "the refusal did not name the merge"
[ "$(tip)" = "$before" ] || die "a request holding a merge moved main"
[ "$(outcome_of c24)" = refused-merge ] || die "the outcome record does not say refused-merge"
forget c24
holds "a request holding a merge was refused, and main did not move"

# ----------------------------------------------------------------------
say "25. a line of commits above a draft's head is refused, naming the draft"

# A request brings every commit from its merge base to its head. So a child
# of a draft's head, approved under another name, would land the draft too.
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c25-draft
echo "a draft below another change" > docs/c25-draft.md
git add -A
git commit --quiet -m "a draft below another change"
git push --quiet origin c25-draft
ready_change c25-draft
id25="$readied"
valley unready "$id25" > /dev/null 2>&1 || die "valley unready failed"
git checkout --quiet -b c25
echo "a change above a draft" > docs/c25.md
git add -A
git commit --quiet -m "a change above a draft"
git push --quiet origin c25
request c25 main "$(git rev-parse c25)"
before="$(tip)"
integrate
grep -q "^c25 -> refs/heads/main .*: refused-not-ready$" "$work/last.out" \
  || die "a child of a draft's head was not refused: $(cat "$work/last.out")"
grep -q "the commit $(git rev-parse c25-draft | cut -c1-12) it brings is v1 of change $id25, which is draft, not ready" \
  "$work/last.out" || die "the refusal did not name the draft"
[ "$(tip)" = "$before" ] || die "a child of a draft's head moved main"
forget c25
holds "a line above a draft's head was refused, and the refusal named the draft"

# ----------------------------------------------------------------------
say "26. withdrawing a change voids its request, and readying it again needs a fresh approval"

# integrate_quiet: a pass that may have nothing to say.
integrate_quiet() {
  integrator reconcile \
    --repo "$origin" \
    --key "$work/integrator" --name "$integrator_name" \
    --known-signers "$work/known_signers" \
    --instance-repo "$instance_repo" \
    --schema "$SCHEMA_VERIFICATION" \
    --attest-schema "$SCHEMA_ATTESTATION" \
    --event-schema "$SCHEMA_EVENTS" \
    2>&1 | tee "$work/last.out"
}
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c26
echo "a change withdrawn after it was approved" > docs/c26.md
git add -A
git commit --quiet -m "a change withdrawn after it was approved"
attest_change c26 "" "" --check prose-format
ready_change c26
id26="$readied"
request "c-$id26" main "$(git rev-parse c26)"
valley withdraw "$id26" > /dev/null 2>&1 || die "valley withdraw failed"
before="$(tip)"
integrate
grep -q "^c-$id26 -> refs/heads/main .*: voided-withdrawn$" "$work/last.out" \
  || die "a withdrawn change's request was not voided: $(cat "$work/last.out")"
git -C "$origin" rev-parse --verify --quiet "refs/the-valley/integration-requests/main/c-$id26" > /dev/null \
  && die "the voided request is still there"
[ "$(outcome_of "c-$id26")" = voided-withdrawn ] || die "the outcome record does not say voided-withdrawn"
[ "$(tip)" = "$before" ] || die "a withdrawn change moved main"

# Readied again at the same head, the change has no request: the old
# approval is gone, and nothing lands.
valley ready --change "$id26" c26 > /dev/null 2>&1 || die "valley ready --change $id26 failed"
integrate_quiet
git -C "$origin" rev-parse --verify --quiet "refs/the-valley/integration-requests/main/c-$id26" > /dev/null \
  && die "the old request came back"
[ "$(tip)" = "$before" ] || die "a change readied again landed with no fresh approval"

# A fresh approval at the same head lands.
request "c-$id26" main "$(git rev-parse c26)"
integrate
grep -q "^c-$id26 -> refs/heads/main .*: land$" "$work/last.out" \
  || die "the fresh approval did not land: $(cat "$work/last.out")"
landed_as "$(git rev-parse c26)" "$pre"
holds "withdraw voided the request on the next pass; readied again, the change landed only on a fresh approval"

# ----------------------------------------------------------------------
say "27. a stack is rebased whole, a landed change under it drops out, and approving its top lands it as one commit"

# approve reads the floor where the integrator does: the owning valley,
# which policy/valley names at main's tip.
git checkout --quiet main
git pull --quiet --ff-only origin main
mkdir -p policy
echo "$instance_repo" > policy/valley
git add policy/valley
git commit --quiet -m "name the owning valley"
git push --quiet origin main

# A change of two commits, a plain branch on it, and two changes on that.
git checkout --quiet -b c27-a
echo "the first half of a" > docs/c27-a1.md
git add -A
git commit --quiet -m "c27: the first half of a"
echo "the second half of a" > docs/c27-a2.md
git add -A
git commit --quiet -m "c27: the second half of a"
attest_change c27-a "" "" --check prose-format
ready_change c27-a
id27a="$readied"
for layer in p b c; do
  git checkout --quiet -b "c27-$layer"
  echo "layer $layer" > "docs/c27-$layer.md"
  git add -A
  git commit --quiet -m "c27: layer $layer"
  git push --quiet origin "c27-$layer"
done
ready_change c27-b
id27b="$readied"
ready_change c27-c
id27c="$readied"
a="$(git rev-parse c27-a)" p="$(git rev-parse c27-p)" b="$(git rev-parse c27-b)" c="$(git rev-parse c27-c)"

# main moves under the stack, outside what prose-format reads, and then a
# lands through an integrator merge: one new commit, not its own two.
git checkout --quiet main
git checkout --quiet -b c27-x
mkdir -p notes
echo "main moves on" > notes/c27-x.txt
git add -A
git commit --quiet -m "c27: main moves on"
git push --quiet origin c27-x
request c27-x main "$(git rev-parse c27-x)"
integrate
grep -q "^c27-x -> refs/heads/main .*: land$" "$work/last.out" || die "c27-x did not land"
request "c-$id27a" main "$a"
integrate
grep -q "^c-$id27a -> refs/heads/main .*: land$" "$work/last.out" || die "c-$id27a did not land"
[ "$(git -C "$origin" merge-base "$a" "$pre")" != "$pre" ] \
  || die "a's base is the tip it landed on, so main did not move under it; the scenario proves nothing"
main27="$(tip)"
[ "$(git -C "$origin" rev-list --no-walk --parents "$main27")" = "$main27 $pre" ] \
  || die "a did not land as one commit on the tip before the pass"

# The rebase reads a's landing from its transfer statement, so a is no
# layer, and its two commits under the plain branch drop out. Each layer
# keeps its own diff, on the rebased layer below.
export VALLEY_KNOWN_SIGNERS="$work/reader_keys" VALLEY_INTEGRATOR_SIGNER="$integrator_name"
valley status --json "$id27a" | grep -q '"landing": *"landed-recorded"' || die "a's landing was not read as recorded"
valley rebase "$id27c" > "$work/rebase.out" 2>&1 || die "the rebase failed: $(cat "$work/rebase.out")"
git fetch --quiet origin
np="$(git rev-parse origin/c27-p)" nb="$(git rev-parse origin/c27-b)" nc="$(git rev-parse origin/c27-c)"
git merge-base --is-ancestor "$main27" "$np" || die "the bottom is not on main"
git merge-base --is-ancestor "$np" "$nb" || die "b is not on the rebased plain branch"
git merge-base --is-ancestor "$nb" "$nc" || die "c is not on b"
[ "$(git rev-list --count "$main27..$np")" = 1 ] || die "a's commits did not drop out from under the plain branch"
[ "$(git diff "$a" "$p")" = "$(git diff "$main27" "$np")" ] || die "the plain branch's diff changed"
[ "$(git diff "$p" "$b")" = "$(git diff "$np" "$nb")" ] || die "b's diff changed"
[ "$(git diff "$b" "$c")" = "$(git diff "$nb" "$nc")" ] || die "c's diff changed"
grep -q "^  c27-p .* origin/c27-p pushed$" "$work/rebase.out" || die "the manifest did not list the plain branch: $(cat "$work/rebase.out")"
holds "the stack moved whole onto main, and the landed change's commits dropped out from under it"

# Approving the rebased top, and landing it, lands the whole stack as one
# commit the integrator makes, with landing evidence for the top alone. A
# lower layer's landing evidence comes with per-layer landing, which lands
# a stack as one commit per layer.
VALLEY_ATTEST_KEY="$work/contributor" VALLEY_ATTEST_NAME="$contributor" \
  valley approve --build-here --head "$nc" "$id27c" > "$work/approve.out" 2>&1 \
  || die "approving the rebased top failed: $(cat "$work/approve.out")"
integrate
grep -q "^c-$id27c -> refs/heads/main .*: land$" "$work/last.out" || die "c-$id27c did not land"
landed_as "$nc" "$pre"
[ "$(git -C "$origin" rev-parse "$(tip)^")" = "$main27" ] || die "the stack did not land as one commit on the tip"
for commit in "$np" "$nb" "$nc"; do
  git -C "$origin" merge-base --is-ancestor "$commit" "$(tip)" && die "the stack's own commit $commit is on main"
done
valley status --json "$id27c" | grep -q '"landing": *"landed-recorded"' || die "the top's landing was not read as recorded"
unset VALLEY_KNOWN_SIGNERS VALLEY_INTEGRATOR_SIGNER
holds "the rebased stack landed as one commit, none of its own commits is on main, and the top reads landed-recorded"

# ----------------------------------------------------------------------
say "28. a signer restricted to some projects counts in those, and is unknown in every other"

# A check worker's key is in the known-signers file, as it is for every
# controller that shares the file, and so is the integrator's, as a host's
# registry holds it. The signer projects file lets the worker count in this
# project alone. A controller takes the project from the repository it
# serves, so this one's is "project".
ssh-keygen -q -t ed25519 -N "" -C worker -f "$work/worker"
worker=worker.valley.invalid/attestations
cat "$work/known_signers" > "$work/worker_signers"
attest key --key "$work/worker" --name "$worker" >> "$work/worker_signers"
attest key --key "$work/integrator" --name "$integrator_name" >> "$work/worker_signers"
printf '# the check worker builds for this project alone\n%s project\n' "$worker" > "$work/signer_projects"
restricted=(--known-signers "$work/worker_signers" --signer-projects "$work/signer_projects")
# The second project's controller signs under a name and key of its own,
# as a controller on another host sharing the registry does, and holds the
# same known-signers file, the first controller's key included. Nothing in
# a note marks the first controller's signature as a countersignature, so
# this host declares it: its signer projects file names the first
# controller with no project, and the worker with this project alone.
ssh-keygen -q -t ed25519 -N "" -C second-integrator -f "$work/second_integrator"
second_integrator=second-integrator.valley.invalid/attestations
printf '%s project\n%s\n' "$worker" "$integrator_name" > "$work/second_signer_projects"
second=(--repo "$work/elsewhere.git" --key "$work/second_integrator" --name "$second_integrator"
  --known-signers "$work/worker_signers" --signer-projects "$work/second_signer_projects")

git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c28
echo "a readme a check worker built" > docs/c28.md
git add -A
git commit --quiet -m "a change a check worker attested"
attest_change c28 "$work/worker" "$worker" --check prose-format
c28="$(git rev-parse c28)"
c28digest="$(attest digest --repo "$repo" --rev c28 | cut -d: -f2)"
c28ref="refs/the-valley/attestations/$c28digest/$(attest key --key "$work/worker" --name "$worker" | cut -d+ -f2)"
request c28 main "$c28"

# A second project, served the same way, holds the same commits, the same
# note and the same request. Its controller's project is "elsewhere".
elsewhere="$work/elsewhere.git"
git init --quiet --bare "$elsewhere"
git -C "$origin" push --quiet "$elsewhere" main c28 "$c28ref" refs/the-valley/integration-requests/main/c28
[ "$(git -C "$elsewhere" rev-parse "$c28ref")" = "$(git -C "$origin" rev-parse "$c28ref")" ] \
  || die "the second project does not hold the same note"
before28="$(git -C "$elsewhere" rev-parse main)" before="$(tip)"
integrate "${second[@]}"
grep -q "^c28 -> refs/heads/main .*: stale$" "$work/last.out" \
  || die "the worker's evidence counted in a project not listed for it"
grep -q "check    prose-format .*signed by no signer the integrator holds" "$work/last.out" \
  || die "the worker's note was not reported as an unknown signer's"
[ "$(git -C "$elsewhere" rev-parse main)" = "$before28" ] || die "main moved in the second project"
holds "in a project not listed for it, the worker's note is classified as a stranger's"

# A signer projects file the controller cannot read stops it before it
# judges anything: one that names a signer twice, one that names a
# verifier key line rather than a signer, one whose projects are joined by
# a no-break space rather than a space, and one that is not there. So does
# one that restricts the integrator's own name, under which a worker would
# also sign transfer statements.
printf '%s project\n%s elsewhere\n' "$worker" "$worker" > "$work/named_twice"
attest key --key "$work/worker" --name "$worker" > "$work/key_line"
printf '%s project\302\240elsewhere\n' "$worker" > "$work/no_break_space"
printf '%s project\n' "$integrator_name" > "$work/names_the_integrator"
for list in named_twice key_line no_break_space missing names_the_integrator; do
  if reconcile "${restricted[@]}" --signer-projects "$work/$list" > "$work/$list.out" 2>&1; then
    die "a controller given the $list signer projects file ran: $(cat "$work/$list.out")"
  fi
  grep -q "signer projects file" "$work/$list.out" \
    || die "the $list signer projects file was not named as the reason: $(cat "$work/$list.out")"
  grep -q "^c28 " "$work/$list.out" && die "a controller given the $list signer projects file judged c28"
  [ "$(tip)" = "$before" ] || die "main moved under the $list signer projects file"
done
grep -qF "names $integrator_name, the name the integrator signs under" "$work/names_the_integrator.out" \
  || die "the integrator's own name was not the reason: $(cat "$work/names_the_integrator.out")"
holds "a signer projects file that does not read, or restricts the integrator's name, stops the controller, and nothing lands"

# In the project listed for it, the same note lands the change.
integrate "${restricted[@]}"
grep -q "^c28 -> refs/heads/main .*: land$" "$work/last.out" \
  || die "the worker's evidence did not count in the project listed for it"
grep -q "check    prose-format .*transferred" "$work/last.out" || die "prose-format did not transfer"
landed_as "$c28" "$pre"
holds "in the project listed for it, the same note lands c28"

# Landing countersigned the worker's note: the first controller stored it,
# with its own signature added, under its own key hash. Copied into the
# second project, the countersigned note still counts only through the
# worker, who does not count there: the second controller holds the first
# one's key, and its signer projects file lets that signature count
# nowhere.
countersigned="refs/the-valley/attestations/$c28digest/$ihash"
git -C "$origin" cat-file blob "$countersigned:prose-format/statement.note" | grep -qF " $integrator_name " \
  || die "the landing did not countersign the worker's note"
git -C "$origin" push --quiet "$elsewhere" "$countersigned"
integrate "${second[@]}"
grep -q "^c28 -> refs/heads/main .*: stale$" "$work/last.out" \
  || die "another controller's countersignature made the worker's note count in a project not listed for it"
[ "$(git -C "$elsewhere" rev-parse main)" = "$before28" ] || die "main moved in the second project"
holds "another controller's countersignature does not stand in for the worker"

# A signer the file does not name still counts in the second project. The
# contributor's note for the same tree arrives there, and replaces the
# worker's as the evidence.
attest run --repo "$repo" --key "$work/contributor" --name "$contributor" --check prose-format > /dev/null
git push --quiet "$elsewhere" "refs/the-valley/attestations/$c28digest/$(cut -d+ -f2 < "$work/known_signers")"
integrate "${second[@]}"
grep -q "^c28 -> refs/heads/main .*: land$" "$work/last.out" \
  || die "an unrestricted signer's evidence did not count in the second project"
landed_as "$c28" "$before28" "$elsewhere" "$second_integrator"
holds "a signer the file does not name counts in every project, as before"

# ----------------------------------------------------------------------
say "29. a change lands as one commit on the tip, and a tree that lands twice keeps both proofs"

# A change of three commits on the tip. The middle one adds a build output
# and the last removes it, so no diff review shows holds it. The change
# lands as one commit the integrator makes, and the build output is in no
# commit on main.
export VALLEY_KNOWN_SIGNERS="$work/reader_keys" VALLEY_INTEGRATOR_SIGNER="$integrator_name"
git fetch --quiet origin
git checkout --quiet -B c29-a origin/main
echo "the first text" > docs/c29.md
git add -A
git commit --quiet -m "c29: a document"
mkdir -p build
echo "a build output" > build/output.bin
git add -A
git commit --quiet -m "c29: a build output"
git rm --quiet -r build
git commit --quiet -m "c29: no build output"
attest_change c29-a "" "" --check prose-format
ready_change c29-a
id29a="$readied"
request "c-$id29a" main "$(git rev-parse c29-a)"
integrate
grep -q "^c-$id29a -> refs/heads/main .*: land$" "$work/last.out" || die "c-$id29a did not land"
landed_as "$(git rev-parse c29-a)" "$pre"
land29a="$(tip)"
[ -z "$(git -C "$origin" log --format=%H refs/heads/main -- build)" ] || die "the build output is on main"
valley status --json "$id29a" | grep -q '"landing": *"landed-recorded"' || die "c-$id29a's landing was not read as recorded"
holds "three commits on the tip landed as one commit, and the build output one of them held is nowhere on main"

# Two single-commit changes: one changes the document, and the next puts
# back the first text, so the tree c29-a landed lands again. Each landing
# keeps its own transfer statement under that tree's ref, and both read
# landed-recorded.
git fetch --quiet origin
git checkout --quiet -B c29-b origin/main
echo "the second text" > docs/c29.md
git commit --quiet -am "c29: the second text"
attest_change c29-b "" "" --check prose-format
ready_change c29-b
request "c-$readied" main "$(git rev-parse c29-b)"
integrate
grep -q "^c-$readied -> refs/heads/main .*: land$" "$work/last.out" || die "c-$readied did not land"
landed_as "$(git rev-parse c29-b)" "$pre"
git fetch --quiet origin
git checkout --quiet -B c29-c origin/main
echo "the first text" > docs/c29.md
git commit --quiet -am "c29: the first text again"
[ "$(git rev-parse "c29-c^{tree}")" = "$(git -C "$origin" rev-parse "$land29a^{tree}")" ] \
  || die "c29-c does not hold the tree c29-a landed; the scenario proves nothing"
git push --quiet origin c29-c
ready_change c29-c
id29c="$readied"
request "c-$id29c" main "$(git rev-parse c29-c)"
integrate
grep -q "^c-$id29c -> refs/heads/main .*: land$" "$work/last.out" || die "c-$id29c did not land"
landed_as "$(git rev-parse c29-c)" "$pre"
land29c="$(tip)"
landed="refs/the-valley/attestations/$(attest digest --repo "$repo" --rev c29-c | cut -d: -f2)/$ihash"
for commit in "$land29a" "$land29c"; do
  git -C "$origin" cat-file blob "$landed:$(transfer_path "$commit")" > "$work/twice.note" \
    || die "no transfer statement for the landing as $commit"
  grep -qx "subject.digest.git-sha1 $commit" "$work/twice.note" || die "the statement in $commit's slot is about another commit"
done
for id in "$id29a" "$id29c"; do
  valley status --json "$id" | grep -q '"landing": *"landed-recorded"' || die "change $id does not read landed-recorded"
done
unset VALLEY_KNOWN_SIGNERS VALLEY_INTEGRATOR_SIGNER
holds "one tree landed twice, each landing kept its own statement, and both read landed-recorded"

# ----------------------------------------------------------------------
say "30. a change's next version lands under the same name, and the version before stays landed-recorded"

# Version 1 lands under c-<ID>. Version 2, rebased onto the landing, lands
# under the same name, and its outcome record replaces version 1's. Each
# version's landing record still binds its own head to its own landing.
export VALLEY_KNOWN_SIGNERS="$work/reader_keys" VALLEY_INTEGRATOR_SIGNER="$integrator_name"
git fetch --quiet origin
git checkout --quiet -B c30 origin/main
echo "the first version" > docs/c30.md
git add -A
git commit --quiet -m "c30: the first version"
attest_change c30 "" "" --check prose-format
ready_change c30
id30="$readied" v1="$(git rev-parse c30)"
request "c-$id30" main "$v1"
integrate
grep -q "^c-$id30 -> refs/heads/main .*: land$" "$work/last.out" || die "version 1 of c-$id30 did not land"
landed_as "$v1" "$pre"
land30v1="$(tip)"
git fetch --quiet origin
git checkout --quiet -B c30-again origin/main
echo "the second version" > docs/c30-again.md
git add -A
git commit --quiet -m "c30: the second version"
attest_change c30-again "" "" --check prose-format
valley ready --change "$id30" c30-again > /dev/null 2>&1 || die "valley ready --change $id30 c30-again failed"
v2="$(git rev-parse c30-again)"
# v2 is made on the main v1 landed on, so it is the change's next round.
valley status --json "$id30" | jq -e '.version == 2 and .landing == "not-landed" and .landed_version == null' > /dev/null \
  || die "v2, made on v1's landing, does not read as work left to land: $(valley status --json "$id30")"
request "c-$id30" main "$v2"
integrate
grep -q "^c-$id30 -> refs/heads/main .*: land$" "$work/last.out" || die "version 2 of c-$id30 did not land"
landed_as "$v2" "$pre"
land30v2="$(tip)"
[ "$(git -C "$origin" cat-file blob "refs/the-valley/integration-outcomes/main/c-$id30" | cut -d' ' -f1)" = "$v2" ] \
  || die "the outcome record under c-$id30 does not name version 2; the scenario proves nothing"
valley changes --json > "$work/changes30.json"
jq -e --arg id "$id30" --arg l1 "$land30v1" --arg l2 "$land30v2" '
  [.repos[].changes[] | select(.change_id == $id) | .versions[] | [.landing, .landed_commit]]
  == [["landed-recorded", $l1], ["landed-recorded", $l2]]' "$work/changes30.json" > /dev/null \
  || die "both versions do not read landed-recorded as their own landings: $(jq -c --arg id "$id30" '.repos[].changes[] | select(.change_id == $id) | .versions' "$work/changes30.json")"
holds "version 2 replaced the outcome record, and version 1's landing record kept it landed-recorded"

# ----------------------------------------------------------------------
say "30b. approving the version shown lands it under a restacked active version, and the change reads landed as it"

# v1 is what the reviewer read. main moves, and the change is restacked
# onto it as v2 while the review is open. Approving v1, the head shown,
# lands it, and from then on the change reads landed as v1 though v2 is
# active: it is no work left to land, so nothing restacks it, and no
# request for it is judged when main moves again.
git fetch --quiet origin
git checkout --quiet -B c30b origin/main
echo "the version the reviewer read" > docs/c30b.md
git add -A
git commit --quiet -m "c30b: the version the reviewer read"
git push --quiet origin c30b
ready_change c30b
id30b="$readied" v1="$(git rev-parse c30b)"
git checkout --quiet -B c30b-x origin/main
echo "main moves under the review" > notes/c30b-x.txt
git add -A
git commit --quiet -m "c30b: main moves under the review"
git push --quiet origin c30b-x
request c30b-x main "$(git rev-parse c30b-x)"
integrate
grep -q "^c30b-x -> refs/heads/main .*: land$" "$work/last.out" || die "c30b-x did not land"
valley rebase "$id30b" > "$work/rebase30b.out" 2>&1 || die "the restack of c30b failed: $(cat "$work/rebase30b.out")"
valley status --json "$id30b" | jq -e '.version == 2 and .landing == "not-landed"' > /dev/null \
  || die "the restack did not make v2 the active version: $(valley status --json "$id30b")"
VALLEY_ATTEST_KEY="$work/contributor" VALLEY_ATTEST_NAME="$contributor" \
  valley approve --build-here --head "$v1" "$id30b" > "$work/approve30b.out" 2>&1 \
  || die "approving v1 under an active v2 failed: $(cat "$work/approve30b.out")"
integrate
grep -q "^c-$id30b -> refs/heads/main .*: land$" "$work/last.out" || die "v1 of c-$id30b did not land"
[ "$(git -C "$origin" rev-parse "$(tip)^")" = "$pre" ] \
  && [ "$(git -C "$origin" diff "$pre" "$(tip)")" = "$(git -C "$origin" diff "$v1^" "$v1")" ] \
  || die "v1 did not land as its own delta, one commit on the tip"
land30b="$(tip)"
valley status --json "$id30b" | jq -e --arg v1 "$v1" '.version == 2 and .landing == "landed-recorded" and .landed_version == 1 and .head == $v1 and .state == "integrated-recorded"' > /dev/null \
  || die "the change does not read landed as v1: $(valley status --json "$id30b")"
valley pending --json | jq -e --arg id "$id30b" '[.. | objects | select(.change_id? == $id)] == []' > /dev/null \
  || die "valley pending lists c-$id30b, which landed as v1"
valley rebase "$id30b" > "$work/rebase30b-again.out" 2>&1 || true # a landed change is no layer to restack
git fetch --quiet origin
valley status --json "$id30b" | jq -e '.version == 2' > /dev/null \
  || die "c-$id30b was restacked again after it landed as v1: $(cat "$work/rebase30b-again.out")"
git checkout --quiet -B c30b-y origin/main
echo "main moves after the landing" > notes/c30b-y.txt
git add -A
git commit --quiet -m "c30b: main moves after the landing"
git push --quiet origin c30b-y
request c30b-y main "$(git rev-parse c30b-y)"
integrate
grep -q "^c30b-y -> refs/heads/main .*: land$" "$work/last.out" || die "c30b-y did not land"
[ "$(git -C "$origin" rev-parse "$(tip)^")" = "$land30b" ] || die "main did not move past the landing; the scenario proves nothing"
grep -q "c-$id30b\|c-c30b " "$work/last.out" && die "a request for c-$id30b was judged after it landed: $(cat "$work/last.out")"
holds "v1 landed under an active v2, the change reads landed as v1, and nothing restacks or judges it after"

# ----------------------------------------------------------------------
say "31. a branch below a landed change is no layer, and the change above it is measured from the landing"

# main -> p -> a -> b, with p a plain branch, and a and b changes. a lands
# as one new commit, so main reaches neither a's commits nor p, though
# both are on main in content. p is listed nowhere and is no layer of b's
# stack, and b counts its own commit alone.
git fetch --quiet origin
git checkout --quiet -B c31-p origin/main
echo "the plain branch" > docs/c31-p.md
git add -A
git commit --quiet -m "c31: a plain branch"
git push --quiet origin c31-p
git checkout --quiet -B c31-a
echo "change a" > docs/c31-a.md
git add -A
git commit --quiet -m "c31: change a"
attest_change c31-a "" "" --check prose-format
ready_change c31-a
id31a="$readied"
git checkout --quiet -B c31-b
echo "change b" > docs/c31-b.md
git add -A
git commit --quiet -m "c31: change b"
git push --quiet origin c31-b
ready_change c31-b
id31b="$readied"
p31="$(git rev-parse c31-p)" a31="$(git rev-parse c31-a)" b31="$(git rev-parse c31-b)"
request "c-$id31a" main "$a31"
integrate
grep -q "^c-$id31a -> refs/heads/main .*: land$" "$work/last.out" || die "c-$id31a did not land"
landed_as "$a31" "$pre"
main31="$(tip)"
git -C "$origin" merge-base --is-ancestor "$p31" "$main31" && die "main reaches the plain branch; the scenario proves nothing"

valley changes --json > "$work/changes31.json"
jq -e --arg p "$p31" '[.repos[].branches[] | select(.head == $p)] == []' "$work/changes31.json" > /dev/null \
  || die "valley changes lists the plain branch below the landed change"
valley pending --json > "$work/pending31.json"
jq -e --arg p "$p31" '[.repos[].branches[] | select(.head == $p)] == []' "$work/pending31.json" > /dev/null \
  || die "valley pending --json lists the plain branch below the landed change"
h31b="$(jq -r --arg id "$id31b" '.repos[].changes[] | select(.change_id == $id) | .handle' "$work/pending31.json")"
[ -n "$h31b" ] || die "valley pending does not list c-$id31b"
valley pending -v --branches > "$work/pending31.out"
grep -q "c31-p" "$work/pending31.out" && die "valley pending lists the plain branch below the landed change: $(cat "$work/pending31.out")"
grep -E -A2 "^  [0-9]+  $h31b v1( |$)" "$work/pending31.out" | tail -n1 | grep -qx '     1 commit · 1 file +1 −0' \
  || die "valley pending does not measure b from a's landing: $(grep -E -A3 "^  [0-9]+  $h31b v1" "$work/pending31.out")"
holds "valley changes and valley pending leave out the plain branch, and pending counts b's own commit alone"

valley rebase "$id31b" > "$work/rebase31.out" 2>&1 || die "the rebase of b failed: $(cat "$work/rebase31.out")"
git fetch --quiet origin
nb31="$(git rev-parse origin/c31-b)"
[ "$(git rev-parse "$nb31^")" = "$main31" ] || die "b was not rebased as one commit onto main"
[ "$(git diff "$a31" "$b31")" = "$(git diff "$main31" "$nb31")" ] || die "b's diff changed in the rebase"
[ "$(git rev-parse origin/c31-p)" = "$p31" ] || die "the rebase moved the plain branch below the landed change"
holds "b rebased alone onto main: the plain branch and a's commits dropped out from under it"
unset VALLEY_KNOWN_SIGNERS VALLEY_INTEGRATOR_SIGNER

# ----------------------------------------------------------------------
say "32. a standing answer is judged again when the accepted signers or the floor change, and only then"

# A verdict reads more than the request, the target and the attestation
# refs. It reads which signers count, and the floor. Each case below holds
# the head, main and every attestation ref where they are, changes only the
# one input, and expects the request to be judged again.
inputs_of() { git -C "$origin" cat-file blob "refs/the-valley/integration-inputs/main/$1"; }
# quiet32 [flag…] — a pass that may have nothing to say, its output in last.out.
quiet32() { reconcile "$@" > "$work/last.out" 2>&1 || die "the pass failed: $(cat "$work/last.out")"; }
fixed() { printf '%s %s %s' "$(git -C "$origin" rev-parse "refs/the-valley/integration-requests/main/$1")" "$(tip)" \
  "$(git -C "$origin" for-each-ref --format='%(refname) %(objectname)' refs/the-valley/attestations/ | sha256sum)"; }

git checkout --quiet main
git pull --quiet --ff-only origin main

# A signer the known-signers file does not hold yet attests a change.
ssh-keygen -q -t ed25519 -N "" -C late -f "$work/late"
late=late.valley.invalid/attestations
cp "$work/known_signers" "$work/late_signers"
git checkout --quiet -b c32s
echo "a readme a late signer attested" > docs/c32s.md
git add -A
git commit --quiet -m "a change attested by a signer not yet accepted"
attest_change c32s "$work/late" "$late" --check prose-format
request c32s main "$(git rev-parse c32s)"
integrate --known-signers "$work/late_signers"
grep -q "^c32s -> refs/heads/main .*: stale$" "$work/last.out" || die "an unaccepted signer's evidence did not leave c32s stale"
grep -q "check    prose-format .*signed by no signer the integrator holds" "$work/last.out" \
  || die "the late signer's note was not reported as an unknown signer's"
grep -Eqx '[0-9a-f]{40} [0-9a-f]{40} [0-9a-f]{16} stale' <(git -C "$origin" cat-file blob refs/the-valley/integration-outcomes/main/c32s) \
  || die "the outcome record is not in the format its readers parse"
fixed32="$(fixed c32s)"

# Nothing moved, so nothing is judged again.
quiet32 --known-signers "$work/late_signers"
grep -q "^c32s " "$work/last.out" && die "an unchanged c32s was judged again: $(cat "$work/last.out")"

# A record written before inputs were remembered has an outcome and no
# inputs record. It is judged once more, and then stands.
git -C "$origin" update-ref -d refs/the-valley/integration-inputs/main/c32s
integrate --known-signers "$work/late_signers"
grep -q "^c32s -> refs/heads/main .*: stale$" "$work/last.out" || die "an outcome with no inputs record was not judged again"
inputs_of c32s > /dev/null || die "judging c32s again did not record its inputs"
quiet32 --known-signers "$work/late_signers"
grep -q "^c32s " "$work/last.out" && die "c32s was judged again after its inputs were recorded: $(cat "$work/last.out")"

# The registry accepts the late signer. Only the known-signers file moves.
attest key --key "$work/late" --name "$late" >> "$work/late_signers"
[ "$(fixed c32s)" = "$fixed32" ] || die "the head, main or an attestation ref moved; the case proves nothing"
integrate --known-signers "$work/late_signers"
grep -q "^c32s -> refs/heads/main .*: land$" "$work/last.out" \
  || die "accepting the signer did not judge c32s again: $(cat "$work/last.out")"
landed_as "$(git rev-parse c32s)" "$pre"
holds "a stale answer stands while nothing moves, and accepting the signer alone lands the change"

# The signer projects file is read once, when the controller starts, and
# the host restarts the controller when the list changes. So a start under
# a changed list at the same path must judge again what an earlier start
# answered.
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c32p
echo "a readme a restricted signer attested" > docs/c32p.md
git add -A
git commit --quiet -m "a change attested by a signer restricted elsewhere"
attest_change c32p "$work/late" "$late" --check prose-format
request c32p main "$(git rev-parse c32p)"
printf '%s elsewhere\n' "$late" > "$work/late_projects"
integrate --known-signers "$work/late_signers" --signer-projects "$work/late_projects"
grep -q "^c32p -> refs/heads/main .*: stale$" "$work/last.out" || die "a signer restricted elsewhere counted in this project"
fixed32="$(fixed c32p)"
quiet32 --known-signers "$work/late_signers" --signer-projects "$work/late_projects"
grep -q "^c32p " "$work/last.out" && die "an unchanged c32p was judged again: $(cat "$work/last.out")"
printf '%s project\n' "$late" > "$work/late_projects"
[ "$(fixed c32p)" = "$fixed32" ] || die "the head, main or an attestation ref moved; the case proves nothing"
integrate --known-signers "$work/late_signers" --signer-projects "$work/late_projects"
grep -q "^c32p -> refs/heads/main .*: land$" "$work/last.out" \
  || die "a start under a changed signer projects file did not judge c32p again: $(cat "$work/last.out")"
landed_as "$(git rev-parse c32p)" "$pre"
holds "a controller started under a changed signer projects file judges the standing answer again"

# The floor requires a check the change carries no evidence for.
cp "$floor/policy/instance/floor.cue" "$work/floor32.cue"
cat > "$floor/policy/instance/floor.cue" <<'EOF'
package verification

floor: {
	checks: {
		"prose-format": {runner: "nix", attribute: "prose-format"}
		"floor-review": {runner: "command", command: "true"}
	}
	classes: prose: {paths: "docs/**": true, requires: {"prose-format": true, "floor-review": true}}
	unclassified: {}
}
EOF
git -C "$floor" commit --quiet -am "the floor requires floor-review of docs/"
git -C "$floor" push --quiet "$instance_repo" main
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c32f
echo "a readme the floor holds back" > docs/c32f.md
git add -A
git commit --quiet -m "a change the floor holds back"
attest_change c32f "" "" --check prose-format
request c32f main "$(git rev-parse c32f)"
integrate
grep -q "^c32f -> refs/heads/main .*: stale$" "$work/last.out" || die "the floor's own requirement did not hold c32f back: $(cat "$work/last.out")"
grep -q "floor-review" "$work/last.out" || die "the missing floor-review evidence was not named"
fixed32="$(fixed c32f)"
quiet32
grep -q "^c32f " "$work/last.out" && die "an unchanged c32f was judged again: $(cat "$work/last.out")"

# An input that does not read fails the request closed, and the failure
# stands for its window. Restored, the input is judged again at once.
before="$(tip)"
integrate --known-signers "$work/nowhere"
grep -q "^c32f: .*known-signers" "$work/last.out" || die "an unreadable known-signers file was not the reason c32f failed: $(cat "$work/last.out")"
[ "$(outcome_of c32f)" = failed ] || die "the unreadable input did not record a failure"
[ "$(tip)" = "$before" ] || die "main moved while an input did not read"
quiet32 --known-signers "$work/nowhere"
grep -q "^c32f" "$work/last.out" && die "the failure did not stand for its window: $(cat "$work/last.out")"
integrate
grep -q "^c32f -> refs/heads/main .*: stale$" "$work/last.out" || die "c32f was not judged again once the input read: $(cat "$work/last.out")"
holds "an input that does not read fails closed, and the request is judged again once it reads"

# The floor drops the requirement. Only the instance repository moves.
cp "$work/floor32.cue" "$floor/policy/instance/floor.cue"
git -C "$floor" commit --quiet -am "the floor no longer requires floor-review"
git -C "$floor" push --quiet "$instance_repo" main
[ "$(fixed c32f)" = "$fixed32" ] || die "the head, main or an attestation ref moved; the case proves nothing"
integrate
grep -q "^c32f -> refs/heads/main .*: land$" "$work/last.out" \
  || die "changing the floor did not judge c32f again: $(cat "$work/last.out")"
landed_as "$(git rev-parse c32f)" "$pre"
holds "a stale answer the floor gave is judged again when only the floor changes, and the change lands"

# ----------------------------------------------------------------------
say "33. a controller woken by its wake directory decides at once, follows a landing, and recovers what it missed"

# Every controller here passes on a one-hour interval unless a case says
# otherwise, so a pass that comes within seconds came from a mark, a
# landing or a start, never the timer. The served repositories carry a
# post-receive hook in the shape the host installs: each push creates the
# empty file due in the controller's wake directory with O_EXCL, and a due
# that is already there is a mark already made.
wake="$work/wake33"
iwake="$work/wake33-instance"
other="$work/wake33-other" # a controller that reads nothing here
mkdir -m 0770 "$wake" "$iwake" "$other"
hook33() {
  printf '#!%s\nset -C\n{ : > "%s/due"; } 2>/dev/null || true\n' "$(command -v bash)" "$2" > "$1/hooks/post-receive"
  chmod +x "$1/hooks/post-receive"
}
hook33 "$origin" "$wake"
hook33 "$instance_repo" "$iwake"

# Each line a controller prints is stamped with the time it was read, so a
# case measures from a stamp taken before a push to a line.
stamp() { while IFS= read -r line; do printf '%s %s\n' "$EPOCHREALTIME" "$line"; done; }
# watch33 LOG REPO DIR [flag…] — a controller for REPO, woken by DIR, in the
# background, its pid in watch_pid. A case that leaves an executable
# $work/during33 has it run once, from the middle of a pass.
watch33() {
  local log="$1" served="$2" dir="$3"
  shift 3
  VALLEY_E2E_DURING_PASS="f=$work/during33; if [ -x \$f ]; then mv \$f \$f.ran && \$f.ran; fi" \
    VALLEY_TELEMETRY_DIR="$stream33" \
    integrator watch \
    --repo "$served" \
    --key "$work/integrator" --name "$integrator_name" \
    --known-signers "$work/known_signers" \
    --instance-repo "$instance_repo" \
    --schema "$SCHEMA_VERIFICATION" \
    --attest-schema "$SCHEMA_ATTESTATION" \
    --event-schema "$SCHEMA_EVENTS" \
    --woken-by "$dir" --interval 1h "$@" > >(stamp >> "$log") 2>&1 &
  watch_pid=$!
}
# when33 LOG AFTER PATTERN [SECONDS] — the stamp of the first line of LOG
# stamped no earlier than AFTER that matches PATTERN, waiting for it.
when33() {
  local log="$1" after="$2" pattern="$3" limit="${4:-30}" i at
  for ((i = 0; i < limit * 20; i++)); do
    at="$(awk -v a="$after" -v p="$pattern" \
      '$1 >= a { line = $0; sub(/^[^ ]* /, "", line); if (line ~ p) { print $1; exit } }' "$log" 2> /dev/null)"
    if [ -n "$at" ]; then
      echo "$at"
      return 0
    fi
    sleep 0.05
  done
  return 1
}
ms() { awk -v a="$1" -v b="$2" 'BEGIN { printf "%d", (b - a) * 1000 }'; }
# The controllers store their decision records in one telemetry stream,
# which the checks below read as any reader of the stream would.
stream33="$work/telemetry33"
# decision33 CHANGE OUTCOME — the first decision record about CHANGE
# with OUTCOME in the stream, waiting for it.
decision33() {
  local i rec
  for ((i = 0; i < 100; i++)); do
    rec="$(cat "$stream33"/seg-*.jsonl 2> /dev/null \
      | jq -c --arg c "$1" --arg o "$2" 'select(.type == "decision" and .change == $c and .data.outcome == $o)' | head -n 1)"
    if [ -n "$rec" ]; then
      echo "$rec"
      return 0
    fi
    sleep 0.05
  done
  return 1
}
passes33() { awk -v a="$2" '$1 >= a && $2 == "pass:"' "$1" | wc -l; }
# restart33 [flag…] — the project's controller, stopped and started again;
# t is the stamp before the start.
restart33() {
  kill "$watch_pid" 2> /dev/null || true
  wait "$watch_pid" 2> /dev/null || true
  t="$EPOCHREALTIME"
  watch33 "$log" "$origin" "$wake" "$@"
  when33 "$log" "$t" "^pass: startup$" > /dev/null || die "the controller did not start: $(cat "$log")"
}
log="$work/watch33.log"
ilog="$work/inst33.log"
# change33 NAME — a change to docs/, which prose-format reads, off main.
change33() {
  git checkout --quiet main
  git pull --quiet --ff-only origin main
  git checkout --quiet -b "$1"
  echo "$1" > "docs/$1.md"
  git add -A
  git commit --quiet -m "$1"
}
# lands33 NAME AFTER WHAT — NAME lands after AFTER. Says how long it took
# from AFTER to the start of the pass that decided it, and to its landing.
lands33() {
  local decided landed started
  decided="$(when33 "$log" "$2" "^$1 -> refs/heads/main .*: land$")" || die "$1 did not land within 30s: $(cat "$log")"
  landed="$(when33 "$log" "$decided" "^  landed   refs/heads/main ")" || die "$1 decided to land, and did not"
  started="$(awk -v a="$2" -v d="$decided" '$1 >= a && $1 <= d && $2 == "pass:" { s = $1 } END { print s }' "$log")"
  [ -n "$started" ] || die "no pass started between $2 and deciding $1"
  printf '  measured: %s: to decision start %sms, to landing %sms\n' "$3" "$(ms "$2" "$started")" "$(ms "$2" "$landed")"
}

git checkout --quiet main
git pull --quiet --ff-only origin main
watch33 "$log" "$origin" "$wake"
when33 "$log" 0 "^pass: startup$" > /dev/null || die "the controller did not start: $(cat "$log")"
holds "a controller started on a one-hour interval passes once, at its start"

# Request arrival, idle. The evidence is published first, and its push
# starts a pass that finds no request. The request's push starts the pass
# that lands it. The first decision this process makes is the cold case;
# the ones after it are warm.
for c in c33a c33b c33c; do
  change33 "$c"
  t="$EPOCHREALTIME"
  attest_change "$c" "" "" --check prose-format
  when33 "$log" "$t" "^pass: due$" > /dev/null || die "the evidence push did not wake the controller"
  sleep 0.5
  t="$EPOCHREALTIME"
  request "$c" main "$(git rev-parse "$c")"
  pushed="$EPOCHREALTIME"
  [ "$c" = c33a ] && what="request publication, cold" || what="request publication, warm"
  lands33 "$c" "$t" "$what (the push returned at $(ms "$t" "$pushed")ms)"
done
holds "a request pushed to an idle controller lands within seconds on a one-hour interval"

# Evidence arrival. The request is judged stale for want of evidence, and
# the evidence's own push wakes the controller, which lands it.
change33 c33e
git push --quiet origin c33e
request c33e main "$(git rev-parse c33e)"
when33 "$log" 0 "^c33e -> refs/heads/main .*: stale$" > /dev/null || die "c33e was not judged stale without evidence"
sleep 0.5
t="$EPOCHREALTIME"
attest_change c33e "" "" --check prose-format
lands33 c33e "$t" "evidence publication, warm"
holds "evidence pushed for a stale request wakes the controller, which lands it"
# The first two decisions about c33e say when this process first saw the request
# at its head, which was before the stale one began. The landing began
# in a later pass, the one c33e's evidence woke, at least the half second
# the evidence waited after that sighting.
stale="$(decision33 c33e stale)" || die "no stale decision about c33e in the stream"
landed="$(decision33 c33e land)" || die "no landing decision about c33e in the stream"
jq -e -s '.[0].data as $s | .[1].data as $l
  | $s.process == $l.process and $s.first_seen == $l.first_seen and $s.first_seen_pass == $l.first_seen_pass
  and $s.first_seen_at_start == false and $s.first_seen_pass == $s.pass and $s.seen_to_began_ms >= 0
  and $l.pass > $l.first_seen_pass and $l.pass_reason == "due" and $l.seen_to_began_ms >= 500
  and $l.seen_to_began_ms >= $l.listed_to_began_ms' <<< "$stale$landed" > /dev/null \
  || die "c33e's decision records do not say when it was first seen: $stale $landed"
holds "a request's decisions say when this process first saw it, and the landing began in a later pass"

# Busy. A request is pushed while a pass works on another, after that pass
# listed the requests. Its mark survives the pass, and the next pass starts
# as soon as the busy one ends, and lands it. The busy pass judges c33f,
# whose evidence is over a tree c33h has since moved what prose-format
# reads under, so it evaluates the check, and the request it pushes then
# is pushed from the middle of the pass. c33f stays stale, so nothing lands
# and no follow-up comes: only the mark starts the next pass.
change33 c33f
attest_change c33f "" "" --check prose-format
change33 c33h
attest_change c33h "" "" --check prose-format
t="$EPOCHREALTIME"
request c33h main "$(git rev-parse c33h)"
lands33 c33h "$t" "request publication, warm"
change33 c33g
t="$EPOCHREALTIME"
attest_change c33g "" "" --check prose-format
when33 "$log" "$t" "^pass: due$" > /dev/null || die "c33g's evidence did not wake the controller"
cat > "$work/during33" <<EOF
#!$(command -v bash)
echo "\$EPOCHREALTIME" > "$work/during33.at"
git -C "$repo" push --quiet origin "$(git rev-parse c33g):refs/the-valley/integration-requests/main/c33g"
sleep 2
EOF
chmod +x "$work/during33"
# c33i is requested with c33f, without evidence, so the busy pass lists
# both and judges c33i once c33f is done. The 2s c33f's evaluation sleeps
# fall between c33i's sighting and its decision.
change33 c33i
sleep 0.5
git push --quiet origin \
  "$(git rev-parse c33f):refs/the-valley/integration-requests/main/c33f" \
  "$(git rev-parse c33i):refs/the-valley/integration-requests/main/c33i"
busy="$(when33 "$log" 0 "^c33f -> refs/heads/main .*: stale$")" || die "c33f was not judged"
[ -e "$work/during33.ran" ] || die "the busy pass did not push c33g's request"
t="$(cat "$work/during33.at")"
lands33 c33g "$t" "request publication while busy"
reason="$(awk -v a="$busy" '$1 >= a && $2 == "pass:" { print $3; exit }' "$log")"
[ "$reason" = due ] || die "the pass after the busy one started for $reason, not due"
holds "a request pushed while a pass works is landed by the pass right after it"
rec="$(decision33 c33i stale)" || die "no decision about c33i in the stream"
jq -e '.data | .first_seen == .pass_listed and .first_seen_pass == .pass
  and .listed_to_began_ms >= 2000 and .seen_to_began_ms == .listed_to_began_ms' <<< "$rec" > /dev/null \
  || die "c33i's decision does not say it waited behind c33f: $rec"
printf '  measured: c33i: first seen to decision start %sms, behind c33f in the same pass\n' "$(jq -r .data.seen_to_began_ms <<< "$rec")"
holds "a request judged behind a slow one in the same pass says how long it waited after it was first seen"
git -C "$origin" update-ref -d refs/the-valley/integration-requests/main/c33i

# A landing is followed by another pass at once. c33-dep-a sorts first, so
# it is judged against the tip before c33-dep-b lands, and it conflicts
# there: the tip and it change one line differently. c33-dep-b reverts the
# tip's change, and on the tip it lands the two no longer conflict.
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c33s
echo base > checks/dep33.txt
git add -A
git commit --quiet -m "the line both sides change"
t="$EPOCHREALTIME"
request c33s main "$(git rev-parse c33s)"
lands33 c33s "$t" "unclassified request publication, warm"
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c33-dep-a
echo a > checks/dep33.txt
git commit --quiet -am "a changes the line"
git checkout --quiet main
git checkout --quiet -b c33x
echo x > checks/dep33.txt
git commit --quiet -am "the tip changes it otherwise"
t="$EPOCHREALTIME"
request c33x main "$(git rev-parse c33x)"
lands33 c33x "$t" "unclassified request publication, warm"
git checkout --quiet main
git pull --quiet --ff-only origin main
git checkout --quiet -b c33-dep-b
echo base > checks/dep33.txt
git commit --quiet -am "b reverts the tip's change"
t="$EPOCHREALTIME"
git push --quiet origin \
  "$(git rev-parse c33-dep-a):refs/the-valley/integration-requests/main/c33-dep-a" \
  "$(git rev-parse c33-dep-b):refs/the-valley/integration-requests/main/c33-dep-b"
stale="$(when33 "$log" "$t" "^c33-dep-a -> refs/heads/main .*: stale$")" || die "c33-dep-a did not conflict with the tip before c33-dep-b"
when33 "$log" "$stale" "^c33-dep-b -> refs/heads/main .*: land$" > /dev/null || die "c33-dep-b did not land"
follow="$(when33 "$log" "$stale" "^pass: follow-up$")" || die "no follow-up pass after c33-dep-b landed"
lands33 c33-dep-a "$follow" "dependent request, from the follow-up's start"
lands33 c33-dep-a "$t" "dependent request, from the publication of both"
[ "$(git -C "$origin" show refs/heads/main:checks/dep33.txt)" = a ] || die "c33-dep-a's line is not on main"
holds "a request that conflicted with the tip before a landing lands in the pass that follows it at once"

# A burst of evidence. Six requests are stale for want of it, and each
# change's evidence is pushed in turn, as fast as attest publishes. While a
# pass works, the marks wait as one due. Each change adds to docs/, which
# prose-format reads, so once one lands the others' evidence no longer
# transfers to the tip, and they are judged stale again. What is measured
# is the work: every request judged after its evidence, how many passes
# and decisions that took, and that the controller then goes quiet.
burst=(c33b1 c33b2 c33b3 c33b4 c33b5 c33b6)
for c in "${burst[@]}"; do
  change33 "$c"
  git push --quiet origin "$c"
  request "$c" main "$(git rev-parse "$c")"
  when33 "$log" 0 "^$c -> refs/heads/main .*: stale$" > /dev/null || die "$c was not judged stale"
done
sleep 0.5
t="$EPOCHREALTIME"
before="$(grep -c ' -> refs/heads/main ' "$log")"
for c in "${burst[@]}"; do
  git checkout --quiet "$c"
  pushed="$EPOCHREALTIME"
  attest_change "$c" "" "" --check prose-format
  when33 "$log" "$pushed" "^$c -> refs/heads/main " 60 > /dev/null || die "$c was not judged after its evidence: $(cat "$log")"
done
published="$EPOCHREALTIME"
n=-1
while [ "$n" != "$(passes33 "$log" "$t")" ]; do
  n="$(passes33 "$log" "$t")"
  sleep 2
done
last="$(awk -v a="$t" '$1 >= a && / -> refs\/heads\/main / { l = $1 } END { print l }' "$log")"
decisions=$(($(grep -c ' -> refs/heads/main ' "$log") - before))
landings="$(awk -v a="$t" '$1 >= a && $2 == "landed"' "$log" | wc -l)"
printf '  measured: evidence burst of %d pushes over %sms: %d passes, %d decisions, %d landing, last decision %sms after the first push began\n' \
  "${#burst[@]}" "$(ms "$t" "$published")" "$n" "$decisions" "$landings" "$(ms "$t" "$last")"
[ "$landings" -ge 1 ] || die "nothing landed after the burst"
[ "$n" -le $((2 * ${#burst[@]} + 1)) ] || die "a burst of ${#burst[@]} evidence pushes ran $n passes"
for c in "${burst[@]}"; do forget "$c"; done
holds "a burst of evidence is judged in $n passes, and the controller goes quiet"

# Idle, the controller does nothing: no mark, no landing, no pass.
t="$EPOCHREALTIME"
sleep 2
[ "$(passes33 "$log" "$t")" = 0 ] || die "an idle controller passed: $(awk -v a="$t" '$1 >= a' "$log")"
[ -z "$(ls -A "$wake")" ] || die "the idle wake directory holds $(ls -A "$wake")"
holds "an idle controller on a one-hour interval passes no more, and its directory holds nothing"

# A second controller over the same repository stops at its start, before
# it sweeps or passes, whatever happened to the wake directory. Here the
# first controller's wake directory is replaced, as anyone who may write
# its parent could, and a scratch worktree stands where a pass in progress
# would keep one. The second controller is refused the repository, sweeps
# nothing, and judges nothing. The first goes on serving it.
leak33="$TMPDIR/valley-integrator-wt-in-progress"
git -C "$origin" worktree add --quiet --detach "$leak33" refs/heads/main
mv "$wake" "$wake.replaced"
mkdir -m 0770 "$wake"
t="$EPOCHREALTIME"
if out="$(timeout 20 integrator watch --repo "$origin" --key "$work/integrator" --name "$integrator_name" \
  --known-signers "$work/known_signers" --instance-repo "$instance_repo" --schema "$SCHEMA_VERIFICATION" \
  --attest-schema "$SCHEMA_ATTESTATION" --woken-by "$wake" --interval 1h 2>&1)"; then
  die "a second controller over one repository ran: $out"
fi
grep -q "$origin: another controller holds its lock; one controller serves a repository" <<< "$out" \
  || die "the second controller said: $out"
grep -q "^pass: " <<< "$out" && die "the refused controller passed: $out"
[ -e "$leak33" ] || die "the refused controller swept the first one's worktree"
git -C "$origin" worktree remove --force "$leak33"
rm -r "$wake.replaced"
when33 "$log" "$t" "watching $wake again" 20 > /dev/null || die "the first controller did not watch the new directory: $(cat "$log")"
t="$EPOCHREALTIME"
change33 c33r
attest_change c33r "" "" --check prose-format
request c33r main "$(git rev-parse c33r)"
lands33 c33r "$t" "the first controller, after its wake directory was replaced"
reconcile --woken-by "$wake" > "$work/last.out" 2>&1 && die "reconcile took a wake directory"
grep -q "reconcile reads no wake directory" "$work/last.out" || die "reconcile said: $(cat "$work/last.out")"
holds "one controller serves a repository, a refused one sweeps and judges nothing, and reconcile never takes a wake directory"

# A crash. The controller is killed, a request is pushed while nothing
# consumes its mark, and the next start lands it.
kill -9 "$watch_pid"
wait "$watch_pid" 2> /dev/null || true
change33 c33k
attest_change c33k "" "" --check prose-format
request c33k main "$(git rev-parse c33k)"
[ -e "$wake/due" ] || die "the push while the controller was down left no mark"
restart33
lands33 c33k "$t" "restart after a crash, from the start"
holds "a request pushed while the controller was down lands at its next start"

# A dropped mark and a newly accepted signer start no pass on a one-hour
# interval. The interval is what reads them. The request is written with
# update-ref, which runs no hook, as a mark that never came.
grep -v "^$late+" "$work/late_signers" > "$work/signers33"
change33 c33l
attest_change c33l "$work/late" "$late" --check prose-format
request c33l main "$(git rev-parse c33l)"
# c33m touches only checks/, which no class covers, so c33l landing first
# leaves it nothing to be stale for.
git checkout --quiet main
git checkout --quiet -b c33m
echo c33m > checks/c33m.txt
git add -A
git commit --quiet -m c33m
git push --quiet origin c33m
restart33 --known-signers "$work/signers33"
when33 "$log" "$t" "^c33l -> refs/heads/main .*: stale$" > /dev/null || die "c33l counted a signer not yet accepted"
sleep 0.5
t="$EPOCHREALTIME"
cp "$work/late_signers" "$work/signers33"
git -C "$origin" update-ref refs/the-valley/integration-requests/main/c33m "$(git rev-parse c33m)"
sleep 2
[ "$(passes33 "$log" "$t")" = 0 ] || die "a changed signers file or a dropped mark started a pass on a one-hour interval"
holds "on a one-hour interval, a changed signers file and a dropped mark start no pass"

grep -v "^$late+" "$work/late_signers" > "$work/signers33"
git -C "$origin" update-ref -d refs/the-valley/integration-requests/main/c33m
restart33 --known-signers "$work/signers33" --interval 3s
sleep 0.5
t="$EPOCHREALTIME"
cp "$work/late_signers" "$work/signers33"
git -C "$origin" update-ref refs/the-valley/integration-requests/main/c33m "$(git rev-parse c33m)"
lands33 c33l "$t" "accepted signer, on a 3s interval"
lands33 c33m "$t" "dropped mark, on a 3s interval"
reason="$(awk -v a="$t" '$1 >= a && $2 == "pass:" { print $3; exit }' "$log")"
[ "$reason" = timer ] || die "the changed signers file and the dropped mark were read by a $reason pass, not the timer"
holds "the interval's timer pass lands what a changed signers file and a dropped mark left waiting"
# The restarted controller first saw c33l in its first pass, and says so:
# it knows nothing of the stale decision the controller before it made.
# It first saw c33m, filed after it started, in a later pass.
stale="$(decision33 c33l stale)" || die "no stale decision about c33l in the stream"
landed="$(decision33 c33l land)" || die "no landing decision about c33l in the stream"
jq -e -s '.[0].data as $s | .[1].data as $l
  | $s.process != $l.process and $l.first_seen_at_start == true and $l.first_seen_pass == 1
  and $l.pass > 1 and $l.pass_reason == "timer" and $l.first_seen > $s.first_seen' <<< "$stale$landed" > /dev/null \
  || die "c33l's landing does not say a restarted controller first saw it at its start: $stale $landed"
rec="$(decision33 c33m land)" || die "no landing decision about c33m in the stream"
jq -e --argjson p "$(jq .data.process <<< "$landed")" '.data
  | .process == $p and .first_seen_at_start == false and .first_seen_pass > 1' <<< "$rec" > /dev/null \
  || die "c33m's landing does not say the controller saw it after its start: $rec"
holds "after a restart, a decision says its request was first seen at the start, and nothing from before it"

# A due the controller cannot remove starts the pass it is found at, and
# then none until the interval, however often it is marked.
restart33
t="$EPOCHREALTIME"
mkdir -p "$wake/due/held"
when33 "$log" "$t" "could not remove the due in $wake" > /dev/null || die "the stuck due was not said"
change33 c33q
attest_change c33q "" "" --check prose-format
sleep 2
[ "$(passes33 "$log" "$t")" = 1 ] || die "a due that cannot be removed started $(passes33 "$log" "$t") passes"
rm -r "$wake/due"
holds "a due that cannot be removed starts one pass, and the controller does not spin on it"

# The floor's move wakes the controllers that read it, and only those. The
# instance repository's controller lands a floor change, and marks the
# project's directory once it has. Its own push woke only it.
restart33
sleep 1
watch33 "$ilog" "$instance_repo" "$iwake" --wake-floor-reader "$wake"
when33 "$ilog" 0 "^pass: startup$" > /dev/null || die "the instance's controller did not start: $(cat "$ilog")"
cat > "$floor/policy/instance/floor.cue" <<'EOF'
package verification

floor: {
	checks: {
		"prose-format": {runner: "nix", attribute: "prose-format"}
		"floor-review": {runner: "command", command: "true"}
	}
	classes: prose: {paths: "docs/**": true, requires: {"prose-format": true, "floor-review": true}}
	unclassified: {}
}
EOF
git -C "$floor" commit --quiet -am "the floor requires floor-review of docs/"
git -C "$floor" push --quiet "$instance_repo" main
change33 c33o
attest_change c33o "" "" --check prose-format
request c33o main "$(git rev-parse c33o)"
when33 "$log" 0 "^c33o -> refs/heads/main .*: stale$" > /dev/null || die "the floor did not hold c33o back: $(cat "$log")"
cp "$work/floor32.cue" "$floor/policy/instance/floor.cue"
git -C "$floor" commit --quiet -am "the floor no longer requires floor-review"
sleep 0.5
t="$EPOCHREALTIME"
git -C "$floor" push --quiet "$instance_repo" "HEAD:refs/the-valley/integration-requests/main/floor33"
when33 "$ilog" "$t" "^  wake     the floor reader at $wake is due$" > /dev/null || die "the floor landing did not mark the project: $(cat "$ilog")"
lands33 c33o "$t" "floor request publication, to the project's landing"
[ -z "$(ls -A "$other")" ] || die "a controller that reads nothing here was marked"
holds "a floor landed by the instance's controller wakes the project's, which lands what the floor held back"
holds "the directory of a controller the floor does not concern stayed empty"
reconcile --wake-floor-reader "$other" > "$work/last.out" 2>&1 \
  && die "a controller that does not serve the instance repository took --wake-floor-reader"
grep -q "is for the controller that lands the floor" "$work/last.out" || die "the refusal said: $(cat "$work/last.out")"
holds "only the controller serving the instance repository marks the floor's readers"

# ----------------------------------------------------------------------
say "34. evidence for the tree that would land stands where the change's own cannot transfer"

# A note counts for a check only when it ran the computation the policy
# requires. c34u's note is a known signer's passing note for check
# prose-format, built from the code-build attribute, on the tip. The tree
# has not moved, and the note is still no evidence for prose-format.
change33 c34u
attest_change c34u "" "" --check prose-format=code-build
ready_change c34u
before34="$(tip)"
t="$EPOCHREALTIME"
request "c-$readied" main "$(git rev-parse c34u)"
when33 "$log" "$t" "^c-$readied -> refs/heads/main .*: stale$" > /dev/null \
  || die "c34u was not stale on a note for another attribute: $(cat "$log")"
when33 "$log" "$t" "check    prose-format .*attestation is for runner \"nix\", attribute \"code-build\", and the policy requires runner \"nix\", attribute \"prose-format\"" > /dev/null \
  || die "the verdict did not name the attribute it refused: $(cat "$log")"
[ "$(tip)" = "$before34" ] || die "main moved on a note for another attribute"
holds "a passing note for the right check and another attribute is no evidence on an unmoved tree"

# c34 is attested on the tip. Then a change lands that touches what
# prose-format reads, so c34's evidence no longer transfers to the tree its
# delta would land as. The controller of 33 is still watching.
change33 c34
attest_change c34 "" "" --check prose-format
ready_change c34
id34="$readied" x34="$(git rev-parse c34)"
change33 c34-main
attest_change c34-main "" "" --check prose-format
t="$EPOCHREALTIME"
request c34-main main "$(git rev-parse c34-main)"
lands33 c34-main "$t" "the change that moves c34's inputs"
before34="$(tip)"

# c34-rebased is the version the restacker would make: c34's delta on the
# new tip. Its tree is the tree c34 would land as. A stranger attests it
# first, which the integrator does not accept.
git fetch --quiet origin
git checkout --quiet -B c34-rebased origin/main
git cherry-pick "$x34" > /dev/null
attest_change c34-rebased "$work/stranger" "$stranger" --check prose-format
landing34="$(attest digest --repo "$repo" --rev c34-rebased | cut -d: -f2)"
[ "$landing34" != "$(attest digest --repo "$repo" --rev "$x34" | cut -d: -f2)" ] \
  || die "c34's tree is the landing tree; the scenario proves nothing"
sleep 0.5
t="$EPOCHREALTIME"
request "c-$id34" main "$x34"
when33 "$log" "$t" "^c-$id34 -> refs/heads/main .*: stale$" > /dev/null \
  || die "c-$id34 was not stale with a stranger's evidence for its landing tree: $(cat "$log")"
when33 "$log" "$t" "check    prose-format .*input closure changed.*tree that would land.*no signer the integrator holds" > /dev/null \
  || die "the verdict did not report both sources refused: $(cat "$log")"
[ "$(tip)" = "$before34" ] || die "main moved on a stranger's evidence"
# The pass prints its verdict before it writes the outcome record, which
# keeps the four fields every reader parses.
for ((i = 0; i < 100; i++)); do
  record34="$(git -C "$origin" cat-file blob "refs/the-valley/integration-outcomes/main/c-$id34" 2> /dev/null)" && break
  sleep 0.05
done
grep -Eqx "$x34 [0-9a-f]{40} [0-9a-f]{16} stale" <<< "$record34" \
  || die "the outcome record is not in the format its readers parse: $record34"
holds "a stranger's evidence for the landing tree lands nothing"

# A known signer's passing note for the landing tree, built from another
# attribute, is no evidence for prose-format either.
sleep 0.5
t="$EPOCHREALTIME"
attest_change c34-rebased "" "" --check prose-format=code-build
when33 "$log" "$t" "^pass: due$" > /dev/null || die "the landing tree's evidence did not wake the controller"
when33 "$log" "$t" "check    prose-format .*tree that would land.*attestation is for runner \"nix\", attribute \"code-build\"" > /dev/null \
  || die "the verdict did not refuse the landing tree's note for another attribute: $(cat "$log")"
[ "$(tip)" = "$before34" ] || die "main moved on a note for another attribute"
holds "a passing note for the landing tree and another attribute is no evidence on a moved landing"

# The host's worker then attests the landing tree under a known key, for
# the attribute the policy names. Its ref replaces the one before it. The
# push wakes the controller, which lands c34's delta on that evidence and
# countersigns it under the landing tree.
known34="refs/the-valley/attestations/$landing34/$(attest key --key "$work/contributor" --name "$contributor" | cut -d+ -f2)"
git -C "$origin" update-ref -d "$known34"
git update-ref -d "$known34"
sleep 0.5
t="$EPOCHREALTIME"
attest_change c34-rebased "" "" --check prose-format
when33 "$log" "$t" "^pass: due$" > /dev/null || die "the landing tree's evidence did not wake the controller"
lands33 "c-$id34" "$t" "landing-tree evidence publication"
[ "$(passes33 "$log" "$t")" -eq "$(awk -v a="$t" '$1 >= a && $2 == "pass:" && $3 == "due"' "$log" | wc -l)" ] \
  || die "a pass other than a woken one ran: $(cat "$log")"
when33 "$log" "$t" "check    prose-format .*transferred .unmoved, the landing tree's own evidence." > /dev/null \
  || die "prose-format did not stand on the landing tree's evidence: $(cat "$log")"
landed_as c34-rebased "$before34"
note34="$(git -C "$origin" cat-file blob "refs/the-valley/attestations/$landing34/$ihash:prose-format/statement.note")" \
  || die "the landing tree's evidence was not countersigned under the landing tree"
grep -qx "subject.digest.valley-tree-v1 $landing34" <<< "$note34" || die "the countersigned note is not about the landing tree"
grep -qF " $integrator_name " <<< "$note34" || die "the note under the landing tree carries no countersignature"
git -C "$origin" cat-file blob \
  "refs/the-valley/attestations/$landing34/$ihash:$(transfer_path "$(tip)")" > "$work/c34.transfer"
grep -qx "predicate.checks.0.rule unmoved" "$work/c34.transfer" \
  || die "the transfer statement does not record the landing tree's evidence as unmoved"
grep -qx "predicate.checks.0.evidence.sha256 $(sed '/^$/,$d' <<< "$note34" | sha256sum | cut -d' ' -f1)" "$work/c34.transfer" \
  || die "the transfer statement does not cite the landing tree's note"
holds "a stale-for-evidence request lands when its landing tree's evidence arrives, woken by the push"
holds "the landing countersigns that evidence and its transfer statement cites it"

kill $(jobs -p) 2> /dev/null || true
wait 2> /dev/null || true
rm -f "$origin/hooks/post-receive" "$instance_repo/hooks/post-receive"

printf '\nintegrator-e2e: every scenario held\n'
