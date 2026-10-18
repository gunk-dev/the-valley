# shellcheck shell=bash
# valley-restacker once --report-only, end to end over a real bare
# repository wired with the protected host's rendered pre-receive hook.
# Every change is readied with the repository's own valley as its owner;
# the hook grants request to the principal requester, which files the
# requests and is the principal the restacker counts.
#
# Outcome records are injected on the server side with update-ref,
# because no push may write one: only the integrator writes them. Each
# injection is marked where it is made. No scenario here runs the real
# integrator.
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
k="$(change requester k "$base")"
git checkout --quiet k
echo again >> k
git commit --quiet -am again
as requester git push --quiet origin k
ok as requester valley ready --change "$k" k
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
    --principal requester --instance "$smokePolicy/instance" "$@" > "$w/plan.json" 2> "$w/err" \
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

# Capacity: k has two versions uploaded by requester, and a third is over
# the project's cap of 2.
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
valley-restacker once --report-only --repo "$origin" --state "$w/state" --principal requester \
  --instance "$smokePolicy/instance" > "$w/out" 2> "$w/err" || fail 'the summary failed'
grep -qE '^[k-z]+: hold$' "$w/out" || fail 'the summary holds no family'
# And without --report-only, nothing runs.
if valley-restacker once --repo "$origin" --state "$w/state" --principal requester \
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

touch "$out"
