# shellcheck shell=bash
# valley-restacker once --report-only, end to end over a real bare
# repository wired with the fixture host's rendered pre-receive hook.
# Every change is readied with the repository's own valley as its owner.
# The hook grants request and restack to the principal requester, which
# files the requests, and restack alone to the principal restacker, which
# is the principal the restacker counts.
#
# In the planner's scenarios, outcome records are injected on the server
# side with update-ref, because no push may write one: only the
# integrator writes them. Each injection is marked where it is made. The
# fixtures after them run the real integrator.
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
set -euo pipefail

w="$TMPDIR/restack-e2e"
mkdir -p "$w"
export HOME="$w"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name restack-e2e
git config --global user.email restack-e2e@localhost
git config --global init.defaultBranch main
git config --global advice.detachedHead false

fail() {
  printf 'restack-e2e: %s\n' "$*" >&2
  for f in "$w"/out "$w"/err; do
    if [ -s "$f" ]; then cat "$f" >&2; fi
  done
  exit 1
}

origin="$w/project.git"
hook="$(grep -o '/nix/store/[^ ]*-valley-protect-open' "$initScriptPath" | head -n1)"
test -x "$hook"
git init --quiet --bare "$origin"
cp "$hook" "$origin/hooks/pre-receive"
chmod +x "$origin/hooks/pre-receive"

repo="$w/project"
git init --quiet "$repo"
cd "$repo"
git remote add origin "$origin"
# The project layer caps the restacker at two automatic versions a change.
mkdir -p policy/project
printf 'package verification\n\nproject: restack: maxAutomaticVersions: 2\n' > policy/project/policy.cue
echo base > readme
git add readme policy
git commit --quiet -m base
env -u VALLEY_PRINCIPAL git push --quiet origin main

as() {
  local who="$1"
  shift
  git config valley.principal "$who"
  VALLEY_PRINCIPAL="$who" "$@"
}
ok() { "$@" > "$w/out" 2> "$w/err" || fail "$* failed"; }

# change <owner> <branch> <base>: a commit on base, pushed and readied by
# its owner; prints the change ID.
change() {
  local who="$1" branch="$2" base="$3"
  git checkout --quiet -B "$branch" "$base"
  echo "$branch" > "$branch"
  git add "$branch"
  git commit --quiet -m "$branch"
  as "$who" git push --quiet --force origin "$branch"
  ok as "$who" valley ready
  sed -n 's/^The change ID \([k-z]*\) is new\..*/\1/p' "$w/out"
}
# branch <name> <base>: a plain branch, which no record is on.
branch() {
  git checkout --quiet -B "$1" "$2"
  echo "$1" > "$1"
  git add "$1"
  git commit --quiet -m "$1"
  as alice git push --quiet origin "$1"
}

base="$(git rev-parse main)"
a="$(change alice a "$base")"
b1="$(change bob b1 "$base")"
b2="$(change bob b2 b1)"
c1="$(change alice c1 "$base")"
c2="$(change alice c2 c1)"
c3="$(change bob c3 c1)"
d="$(change alice d "$base")"
ok as alice valley unready "$d"
wd="$(change bob wd "$base")"
ok as bob valley withdraw "$wd"
branch plain "$base"
e="$(change alice e "$base")"
branch e-plain e
g1="$(change alice g1 "$base")"
g2="$(change alice g2 g1)"
ok as alice valley unready "$g1"
u="$(change bob u "$base")"
# k gains two versions from the restacker principal. A version the restack
# grant adds keeps the active version's title, so each commit's subject is
# k's title; one under any other title is refused, and its branch is put
# back.
k="$(change alice k "$base")"
git checkout --quiet k
for n in 2 3; do
  echo "$n" >> k
  git commit --quiet -am k
  as restacker git push --quiet origin k
  ok as restacker valley ready --change "$k" k
done
echo forged >> k
git commit --quiet -am 'a title of its own'
as restacker git push --quiet origin k
if as restacker valley ready --change "$k" k > "$w/out" 2> "$w/err"; then
  fail 'the restacker readied a version under a title of its own'
fi
grep -q 'under the title of the active one' "$w/err" || fail 'the refusal does not name the title'
git reset --quiet --hard HEAD~1
as restacker git push --quiet --force origin k
# s lands by fast-forward before main moves, so it is submitted.
s="$(change alice s "$base")"
env -u VALLEY_PRINCIPAL git push --quiet origin s:main
git checkout --quiet main
git reset --quiet --hard origin/main
echo moved > moved
git add moved
git commit --quiet -m moved
env -u VALLEY_PRINCIPAL git push --quiet origin main
tip="$(git rev-parse main)"

# Server-side injection of an outcome record under a request name.
evidence() {
  git -C "$origin" for-each-ref --format='%(refname) %(objectname)' refs/the-valley/attestations/ \
    | LC_ALL=C sort | sha256sum | cut -c1-16
}
inject() {
  local name="$1" line="$2" blob
  blob="$(printf '%s\n' "$line" | git -C "$origin" hash-object -w --stdin)"
  git -C "$origin" update-ref "refs/the-valley/integration-outcomes/main/$name" "$blob"
}
# u's request was judged failed, and no request is left: its landing is
# uncertain.
inject "c-$u" "$(git rev-parse u) $base $(evidence) failed 2026-10-18T00:00:00Z"

# request <name> <head>: requester files an integration request.
request() { as requester git push --quiet origin "$2:refs/the-valley/integration-requests/main/$1"; }

refs() { git -C "$origin" for-each-ref --format='%(refname) %(objectname)' | LC_ALL=C sort; }
plan() {
  local before
  before="$(refs)"
  valley-restacker once --report-only --json --repo "$origin" --state "$w/state" \
    --principal restacker --instance "$smokePolicy/instance" "$@" > "$w/plan.json" 2> "$w/err" \
    || fail "the planner failed"
  [ "$(refs)" = "$before" ] || fail 'a --report-only pass changed a ref on origin'
}
# family <id>: the family plan a change is a layer of.
family() { jq -c --arg id "$1" '.families[] | select(any(.layers[]; .id == $id))' "$w/plan.json"; }
action() {
  local got
  got="$(family "$1" | jq -r .action)"
  [ "$got" = "$2" ] || fail "the family of $1 is $got, not $2: $(family "$1")"
}
says() { family "$1" | jq -e --arg s "$2" 'any(.reasons[], .notes[]; contains($s))' > /dev/null \
  || fail "the family of $1 does not say: $2: $(family "$1")"; }

# ----------------------------------------------------------------------
# Selection and families.
plan
jq -e --arg t "$tip" '.tip == $t and .maxAutomaticVersions == 2' "$w/plan.json" > /dev/null \
  || fail "the plan does not read main's tip or the project's cap"
action "$a" restack
action "$b1" restack
[ "$(family "$b1" | jq -c '[.layers[].id]')" = "[\"$b1\",\"$b2\"]" ] || fail 'b1 and b2 are not one family'
[ "$(family "$c1" | jq '.layers | length')" = 3 ] || fail 'c1, c2 and c3 are not one family'
[ "$(family "$c2" | jq -r '.layers[0].id')" = "$c1" ] || fail 'the shared lower layer is not the bottom'
action "$c3" restack
action "$d" leave
action "$wd" leave
action "$u" leave
action "$e" hold
says "$e" 'e-plain is a plain branch'
action "$g2" hold
says "$g2" 'is a draft'
[ -z "$(family "$s")" ] || fail 'a submitted change was planned'
jq -e '[.families[].layers[] | select(.name == "plain")] | length == 0' "$w/plan.json" > /dev/null \
  || fail 'a lone plain branch was planned'

# Capacity: k has two versions uploaded by the restacker principal, and a
# third is over the project's cap of 2.
action "$k" hold
says "$k" 'over the cap of 2'
family "$k" | jq -e '.layers[0].capacity.uploads == 3' > /dev/null || fail 'k does not count three uploads'

# ----------------------------------------------------------------------
# Request holds. A request by name with no outcome holds.
request a a
plan
action "$a" hold
says "$a" 'no outcome yet'
family "$a" | jq -e '.requests[0].by == ["name", "head"]' > /dev/null || fail 'request a was not related by name'
# A current stale releases.
inject a "$(git rev-parse a) $tip $(evidence) stale"
plan
action "$a" restack
# A mismatched head, tip or fingerprint holds.
inject a "$base $tip $(evidence) stale"
plan
action "$a" hold
says "$a" 'is about head'
inject a "$(git rev-parse a) $base $(evidence) stale"
plan
says "$a" 'judged on main at'
inject a "$(git rev-parse a) $tip 0000000000000000 stale"
plan
says "$a" 'judged on evidence'
# A malformed outcome holds.
inject a "not an outcome"
plan
says "$a" 'does not read'
# A current reject releases, with a note.
inject a "$(git rev-parse a) $tip $(evidence) reject"
plan
action "$a" restack
says "$a" 'was rejected'

# By an exact head, under a name nothing else relates.
request zz-head "$(git rev-parse b2)"
# By range: a commit on c2 no branch holds, so only the request names it
# and the planner has to fetch it before reading its ancestry.
git checkout --quiet -B scratch c2
echo over > over
git add over
git commit --quiet -m over
request zz-range "$(git rev-parse HEAD)"
git checkout --quiet main
git branch --quiet -D scratch
plan
action "$b1" hold
family "$b1" | jq -e '.requests[] | select(.name == "zz-head") | .by == ["head"]' > /dev/null \
  || fail 'zz-head was not related by head'
action "$c1" hold
family "$c1" | jq -e '.requests[] | select(.name == "zz-range") | .by == ["range"]' > /dev/null \
  || fail 'zz-range was not related by range'
action "$c3" hold

# A request whose head is no commit cannot be related or not, so every
# family it might be about holds. Injected on the server side, since the
# hook takes no such request.
git -C "$origin" update-ref refs/the-valley/integration-requests/main/zz-blob \
  "$(echo blob | git -C "$origin" hash-object -w --stdin)"
plan
action "$a" hold
says "$a" 'cannot be determined'

# The human summary reads the same plan.
valley-restacker once --report-only --repo "$origin" --state "$w/state" --principal restacker \
  --instance "$smokePolicy/instance" > "$w/out" 2> "$w/err" || fail 'the summary failed'
grep -qE '^[k-z]+: hold$' "$w/out" || fail 'the summary holds no family'
# And without --report-only, nothing runs.
if valley-restacker once --repo "$origin" --state "$w/state" --principal restacker \
  --instance "$smokePolicy/instance" > "$w/out" 2> "$w/err"; then
  fail 'once ran without --report-only'
fi

# ----------------------------------------------------------------------
# A cap larger than any int. The schema admits it, so it composes, and the
# planner counts it as the largest int: no practical cap. k, held above by
# the cap of 2, is not held by this one. The request whose head is no
# commit would hold k too, so it is removed first, on the server side
# where it was injected.
git -C "$origin" update-ref -d refs/the-valley/integration-requests/main/zz-blob
git checkout --quiet main
printf 'package verification\n\nproject: restack: maxAutomaticVersions: 9223372036854775808\n' \
  > policy/project/policy.cue
git commit --quiet -am 'a cap larger than any int'
env -u VALLEY_PRINCIPAL git push --quiet origin main
plan
grep -q '"maxAutomaticVersions": 9223372036854775807,' "$w/plan.json" \
  || fail "the plan does not count the cap as the largest int: $(cat "$w/plan.json")"
action "$k" restack


# ======================================================================
# The fixtures the publishing slice's tests stand on, each proved here.

# One verifier file, which the rendered hook checks pushed evidence
# against, and the integrator and the restacker read too.
signers="$keys/known_signers"
grep -qF -- "--known-signers $signers" "$hook" || fail 'the rendered hook does not read the shared verifier keys'
# ssh-keygen signs only with a key nobody else can read.
mkdir -p "$w/keys"
for who in contributor integrator; do install -m 600 "$keys/$who" "$w/keys/$who"; done
contributor=contributor.valley.invalid/attestations
integrator_name=integrator.valley.invalid/attestations

# The transport (restack/e2e/transport): git reaches origin through it as
# ssh, as the principal the URL names, and the real receive-pack runs the
# rendered hook. A barrier is armed by writing it to $VALLEY_BARRIER/hold.
export GIT_SSH_COMMAND=valley-restack-transport GIT_SSH_VARIANT=simple
export VALLEY_BARRIER="$w/barrier"
mkdir -p "$VALLEY_BARRIER"
url() { printf 'ssh://%s@valley%s' "$1" "$2"; }
disarm() { rm -f "${VALLEY_BARRIER:?}"/*; }
arm() {
  disarm
  echo "$*" > "$VALLEY_BARRIER/hold"
}
arrived() {
  local i=0
  until [ -e "$VALLEY_BARRIER/arrived" ]; do
    i=$((i + 1))
    [ "$i" -lt 1200 ] || fail 'nothing reached the barrier'
    sleep 0.05
  done
}
release() { touch "$VALLEY_BARRIER/release"; }
at() { git -C "$1" rev-parse --verify --quiet "$2" || true; }

# ----------------------------------------------------------------------
# The restacker principal holds restack and not request, so it cannot file
# a request, which requester can.
cd "$repo"
git checkout --quiet -B fixture main
if git push --quiet "$(url restacker "$origin")" fixture:refs/the-valley/integration-requests/main/zz-by-restacker \
  2> "$w/err"; then
  fail 'the restacker principal filed a request'
fi
git push --quiet "$(url requester "$origin")" fixture:refs/the-valley/integration-requests/main/zz-by-requester \
  2> "$w/err" || fail 'requester could not file a request through the transport'
git -C "$origin" update-ref -d refs/the-valley/integration-requests/main/zz-by-requester

# A barrier holds a push before origin sees it, and releases it.
echo held > held
git add held
git commit --quiet -m held
arm receive connect
git push --quiet "$(url alice "$origin")" fixture:refs/heads/held 2> "$w/err" &
pid=$!
arrived
[ -z "$(at "$origin" refs/heads/held)" ] || fail 'a push the barrier held reached origin'
kill -0 "$pid" 2> /dev/null || fail 'a push the barrier held ended'
release
wait "$pid" || fail 'a released push failed'
[ "$(at "$origin" refs/heads/held)" = "$(git rev-parse fixture)" ] || fail 'a released push did not reach origin'

# A push held there loses its lease to one made while it waits.
was="$(git rev-parse fixture)"
git commit --quiet --allow-empty -m mine
arm receive connect
git push --quiet --force-with-lease="refs/heads/held:$was" "$(url restacker "$origin")" fixture:refs/heads/held \
  2> "$w/err" &
pid=$!
arrived
git checkout --quiet -B author "$was"
git commit --quiet --allow-empty -m "the author's"
as alice git push --quiet origin author:held
release
if wait "$pid"; then fail 'a push held across another kept its lease'; fi
grep -q 'stale info' "$w/err" || fail "the held push was not refused for its lease: $(cat "$w/err")"
[ "$(at "$origin" refs/heads/held)" = "$(git rev-parse author)" ] || fail "the author's push did not stand"

# A barrier holds a push that origin has taken, before its client hears.
git checkout --quiet fixture
arm receive taken refs/heads/taken
git push --quiet "$(url alice "$origin")" fixture:refs/heads/taken 2> "$w/err" &
pid=$!
arrived
[ "$(at "$origin" refs/heads/taken)" = "$(git rev-parse fixture)" ] || fail 'origin had not taken the held push'
kill -0 "$pid" 2> /dev/null || fail 'the client heard before the release'
release
wait "$pid" || fail 'a push held after origin took it failed'

# A dropped answer: origin takes the push, and its client reports that
# origin never said so.
arm receive drop refs/heads/dropped
if git push --porcelain "$(url alice "$origin")" fixture:refs/heads/dropped > "$w/out" 2> "$w/err"; then
  fail 'a push whose answer was lost succeeded'
fi
cat "$w/out" "$w/err" | grep -q 'remote failed to report status' || fail 'the client did not lose the answer'
[ "$(at "$origin" refs/heads/dropped)" = "$(git rev-parse fixture)" ] || fail 'origin did not take the push whose answer was lost'
disarm

# ----------------------------------------------------------------------
# A repository served with the integrator's evidence fixtures: the nix
# stand-in (integrator/e2e), and a policy whose checks are pure, under a
# floor that requires nothing.
cp -r "$integratorE2e" "$w/stand-ins"
chmod -R +w "$w/stand-ins"
patchShebangs "$w/stand-ins" > /dev/null
export PATH="$w/stand-ins:$PATH" VALLEY_FAKE_STORE="$w/store"
mkdir -p "$w/floor"
printf 'package verification\n\nfloor: {\n\tchecks: {}\n\tclasses: {}\n\tunclassified: {}\n}\n' > "$w/floor/floor.cue"

served="$w/served.git"
git init --quiet --bare "$served"
cp "$hook" "$served/hooks/pre-receive"
chmod +x "$served/hooks/pre-receive"
git init --quiet "$w/fixture"
cd "$w/fixture"
git remote add origin "$served"
mkdir -p docs src checks policy/project
echo "the readme" > docs/readme.md
echo "the source" > src/main.txt
echo docs > checks/prose-format.inputs
echo src > checks/code-build.inputs
cat > policy/project/policy.cue << 'CUE'
package verification

project: {
	checks: {
		"prose-format": {runner: "nix", attribute: "prose-format"}
		"code-build": {runner: "nix", attribute: "code-build"}
	}
	classes: {
		prose: {paths: "docs/**": true, requires: "prose-format": true}
		code: {paths: "src/**": true, requires: "code-build": true}
	}
	unclassified: {}
}
CUE
git add -A
git commit --quiet -m 'seed the project'
env -u VALLEY_PRINCIPAL git push --quiet origin main

# attested <branch> <attest run flags…>: alice's change, its evidence signed
# by the contributor's key and pushed with it through the hook, readied;
# prints the change ID.
attested() {
  local branch="$1"
  shift
  as alice attest run --repo . --key "$w/keys/contributor" --name "$contributor" \
    --push origin --branch "$branch" "$@" > "$w/out" 2> "$w/err" || fail "attest run on $branch failed"
  ok as alice valley ready
  sed -n 's/^The change ID \([k-z]*\) is new\..*/\1/p' "$w/out"
}
git checkout --quiet -b lands main
echo "a second source line" >> src/main.txt
git commit --quiet -am 'a change the integrator lands'
lands="$(attested lands --check code-build)"
# Its evidence claims an effectful check where the policy runs a pure one.
git checkout --quiet -b lies main
echo "a readme whose evidence lies" > docs/readme.md
git commit --quiet -am 'a change whose evidence lies about its kind'
lies="$(attested lies --command prose-format=true)"
git checkout --quiet main
echo moved > moved.txt
git add moved.txt
git commit --quiet -m 'main moves on'
env -u VALLEY_PRINCIPAL git push --quiet origin main

integrate() {
  integrator reconcile --repo "$served" --key "$w/keys/integrator" --name "$integrator_name" \
    --known-signers "$signers" --instance "$w/floor" --schema "$SCHEMA_VERIFICATION" \
    --attest-schema "$SCHEMA_ATTESTATION" --event-schema "$SCHEMA_EVENTS" > "$w/integrator.out" 2>&1 \
    || fail "the integrator failed: $(cat "$w/integrator.out")"
}
judged() {
  grep -q "^c-$1 -> refs/heads/main .*: $2\$" "$w/integrator.out" \
    || fail "the integrator did not $2 c-$1: $(cat "$w/integrator.out")"
}
# requester files each request through the transport, one pass each, so
# the reject is judged on the tip the landing left.
git push --quiet "$(url requester "$served")" "lands:refs/the-valley/integration-requests/main/c-$lands"
integrate
judged "$lands" land
git push --quiet "$(url requester "$served")" "lies:refs/the-valley/integration-requests/main/c-$lies"
integrate
judged "$lies" reject
grep -q 'requires a pure check and the attestation claims an effectful check' "$w/integrator.out" \
  || fail "the reject is not for the evidence's kind: $(cat "$w/integrator.out")"

# The planner, reading the shared verifier keys, sees the landed change
# submitted: it is no layer. Without the keys, the same landing is only
# uncertain. The rejected change's family is released by the real
# outcome, current at its head, tip and evidence, and the reject is noted.
served_plan() {
  valley-restacker once --report-only --json --repo "$(url restacker "$served")" --state "$w/served-state" \
    --principal restacker --instance "$w/floor" "$@" > "$w/plan.json" 2> "$w/err" || fail 'the planner failed'
}
served_plan --known-signers "$signers" --integrator-name "$integrator_name"
[ -z "$(family "$lands")" ] || fail "the change the integrator landed was planned: $(family "$lands")"
action "$lies" restack
says "$lies" 'was rejected'
family "$lies" | jq -e --arg n "c-$lies" '.requests[] | select(.name == $n) | .decision == "reject" and .current' \
  > /dev/null || fail "the reject was not read as current: $(family "$lies")"
served_plan
action "$lands" leave
family "$lands" | jq -e '.layers[0].state == "uncertain"' > /dev/null \
  || fail "without the keys, the landing is not uncertain: $(family "$lands")"

# ----------------------------------------------------------------------
# A pass locks its state directory: a second pass fails while a barrier
# holds the first one's read of origin, and the first goes on once
# released. The directory belongs to the restacker principal, so a pass
# for another is refused it.
pass() {
  valley-restacker once --report-only --repo "$(url restacker "$served")" --state "$w/served-state" \
    --principal restacker --instance "$w/floor" "$@"
}
arm upload connect
pass > "$w/first.out" 2> "$w/first.err" &
pid=$!
arrived
if pass > "$w/out" 2> "$w/err"; then fail 'a second pass ran while the first held the state'; fi
grep -q 'another pass is using the state directory' "$w/err" || fail "the second pass did not say why: $(cat "$w/err")"
release
wait "$pid" || fail "the first pass failed once released: $(cat "$w/first.err")"
if valley-restacker once --report-only --repo "$(url restacker "$served")" --state "$w/served-state" \
  --principal requester --instance "$w/floor" > "$w/out" 2> "$w/err"; then
  fail 'a pass for another principal used the state directory'
fi
grep -q 'give each its own --state' "$w/err" || fail "the refusal does not say why: $(cat "$w/err")"

# A pass whose read of origin never returns ends at its --timeout.
arm upload connect
if pass --timeout 2s > "$w/out" 2> "$w/err"; then fail 'a pass held at the barrier finished'; fi
grep -q 'ran past its --timeout of 2s' "$w/err" || fail "the pass did not end at its timeout: $(cat "$w/err")"
disarm

touch "$out"
