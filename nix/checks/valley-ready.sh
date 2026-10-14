# shellcheck shell=bash
# valley ready and valley withdraw, end to end and unmocked: every record
# they write is pushed for real to a bare repository wired with the real
# rendered pre-receive hook, as each principal, and what that repository
# then holds is read back. What sshd would set from a key's
# authorized_keys entry, these pushes set in the environment directly, as
# protect-e2e's do.
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
set -euo pipefail

w="$TMPDIR/valley-ready"
mkdir -p "$w"
export HOME="$w"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name valley-ready-check
git config --global user.email valley-ready-check@localhost
git config --global init.defaultBranch main
git config --global advice.detachedHead false

fail() {
  printf 'valley-ready: %s\n' "$*" >&2
  for f in "$w"/out "$w"/err; do
    if [ -s "$f" ]; then cat "$f" >&2; fi
  done
  exit 1
}

# The project "open" of the protected host: it protects nothing, and the
# host grants request to the principal named requester. Its hook is the
# one valley-init installs, followed from the rendered init script.
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
echo base > readme
git add readme
git commit --quiet -m base
env -u VALLEY_PRINCIPAL git push --quiet origin main

# as <principal> <command…>: run a command as a principal. The record names
# the principal git config valley.principal does, and the hook reads the
# one the push carries; an honest client has the two agree. anonymous is a
# key that names no principal.
as() {
  local who="$1"
  shift
  if [ "$who" = anonymous ]; then
    git config --unset valley.principal || true
    env -u VALLEY_PRINCIPAL "$@"
  else
    git config valley.principal "$who"
    VALLEY_PRINCIPAL="$who" "$@"
  fi
}

# ok <command…>: the command succeeds, and its output is kept.
ok() {
  "$@" > "$w/out" 2> "$w/err" || fail "$* failed"
}

# refused <phrase> <command…>: the command fails, saying the phrase.
refused() {
  local says="$1"
  shift
  if "$@" > "$w/out" 2> "$w/err"; then
    fail "$* succeeded where it had to be refused"
  fi
  grep -qF -- "$says" "$w/err" || fail "$* was refused without saying: $says"
}

# record <id> <jq condition…>: origin's record of the change holds each.
record() {
  local id="$1" cond
  shift
  git -C "$origin" cat-file blob "refs/the-valley/changes/main/$id:record.json" > "$w/record.json" \
    || fail "origin holds no record of $id"
  for cond in "$@"; do
    jq -e "$cond" "$w/record.json" > /dev/null || fail "the record of $id: $cond does not hold"
  done
}

# unchanged <id> <command…>: the command leaves origin's record as it was.
unchanged() {
  local id="$1" before
  shift
  before="$(git -C "$origin" rev-parse "refs/the-valley/changes/main/$id")"
  "$@"
  [ "$(git -C "$origin" rev-parse "refs/the-valley/changes/main/$id")" = "$before" ] \
    || fail "the record of $id moved"
}

# one_parent <id>: origin's record commit of the change has one parent,
# the record before it, so it added no version.
one_parent() {
  [ "$(git -C "$origin" rev-list --parents -n 1 "refs/the-valley/changes/main/$1" | wc -w)" = 2 ] \
    || fail "the record of $1 added a version"
}

no_records() {
  [ -z "$(git -C "$origin" for-each-ref refs/the-valley/changes)" ] || fail "$1"
}

# commit <subject> [<trailer>]: a new commit on the current branch, adding
# a file of its own, so that any two branches merge cleanly.
commit() {
  echo "$1" > "$1.txt"
  git add "$1.txt"
  if [ $# -gt 1 ]; then
    git commit --quiet -m "$1" -m "$2"
  else
    git commit --quiet -m "$1"
  fi
}

# with_header <id>: HEAD rewritten to carry a change-id header, the way jj
# writes one, after the committer.
with_header() {
  local amended
  amended="$(git cat-file commit HEAD | sed "/^committer /a change-id $1" |
    git hash-object -t commit -w --stdin)"
  git update-ref refs/heads/"$(git symbolic-ref --short HEAD)" "$amended"
}

# ----------------------------------------------------------------------
# A head marked ready for the first time is v1 of a new change. Its head
# carries no change ID, so ready makes one and prints it.
git checkout --quiet -b topic
commit one
first="$(git rev-parse HEAD)"
refused 'push it there first' as contributor valley ready
no_records 'a head not on origin was recorded'
as contributor git push --quiet origin topic
ok as contributor valley ready
grep -qE '^project [k-z]{4} v1 ready$' "$w/out" || fail 'ready did not print the handle and v1'
id="$(sed -n 's/^The change ID \([k-z]*\) is new\..*/\1/p' "$w/out")"
[ "${#id}" = 32 ] || fail 'ready did not print the change ID it made'
handle="${id:0:4}"
grep -qx "project $handle v1 ready" "$w/out" || fail 'the handle is not the ID'"'"'s first four letters'
record "$id" '.schema == "valley-change-record/v1"' ".change_id == \"$id\"" '.target == "main"' \
  '.owner == "contributor"' '.state == "ready"' '.seq == 1' '.active == 1' \
  ".versions == [{n: 1, head: \"git-sha1:$first\", title: \"one\", branch: \"topic\", uploader: \"contributor\"}]"
# The record commit holds on to the head it records.
[ "$(git -C "$origin" rev-parse "refs/the-valley/changes/main/$id^1")" = "$first" ] \
  || fail 'the record commit does not have the head as its parent'

# A second ready at a new head is v2, named by the ID it printed.
commit two
second="$(git rev-parse HEAD)"
as contributor git push --quiet origin topic
ok as contributor valley ready --change "$id"
grep -qx "project $handle v2 ready" "$w/out" || fail 'the second ready was not v2'
record "$id" '.seq == 2' '.active == 2' '.state == "ready"' '.versions | length == 2' \
  ".versions[0].head == \"git-sha1:$first\"" ".versions[1] == {n: 2, head: \"git-sha1:$second\", title: \"two\", branch: \"topic\", uploader: \"contributor\"}"

# The same head again is no new version.
unchanged "$id" refused 'a new version needs a new head' as contributor valley ready --change "$id"

# Withdrawn, the change comes back by ready. At the head it was withdrawn
# at, it is ready again at the version it had: the record commit's one
# parent is the record before it. It is never made a draft.
ok as contributor valley withdraw "$handle"
grep -qx "project $handle v2 withdrawn" "$w/out" || fail 'withdraw did not say what it withdrew'
record "$id" '.state == "withdrawn"' '.seq == 3' '.active == 2' '.versions | length == 2'
unchanged "$id" refused 'withdrawn already' as contributor valley withdraw "$handle"
unchanged "$id" refused 'never made a draft' as contributor valley unready "$handle"
ok as contributor valley ready --change "$id"
grep -qx "project $handle v2 ready" "$w/out" || fail 'the change readied at the head it was withdrawn at was not v2'
record "$id" '.state == "ready"' '.seq == 4' '.active == 2' '.versions | length == 2'
one_parent "$id"
# Withdrawn again, it comes back by a new version at a new head too.
ok as contributor valley withdraw "$handle"
commit three
third="$(git rev-parse HEAD)"
as contributor git push --quiet origin topic
ok as contributor valley ready --change "$id"
grep -qx "project $handle v3 ready" "$w/out" || fail 'the revived change was not v3'
record "$id" '.state == "ready"' '.seq == 6' '.active == 3' ".versions[2].head == \"git-sha1:$third\""

# ----------------------------------------------------------------------
# Who may write the record. A principal that neither owns the change nor
# holds the request grant is refused by the hook, whatever the command
# writes, and so is a key that names no principal.
commit four
fourth="$(git rev-parse HEAD)"
as contributor git push --quiet origin topic
unchanged "$id" refused "only the change's owner, contributor" as integrator valley ready --change "$id"
unchanged "$id" refused "only the change's owner, contributor" as integrator valley withdraw "$handle"
unchanged "$id" refused "only the change's owner, contributor" as integrator valley unready "$handle"
unchanged "$id" refused 'a key that names no principal' env -u VALLEY_PRINCIPAL valley withdraw "$handle"
refused 'git config valley.principal names none' as anonymous valley ready --change "$id"
# The operator, who holds the request grant, may add a version and
# withdraw, and the owner stays the owner.
ok as requester valley ready --head "$fourth" --change "$id"
grep -qx "project $handle v4 ready" "$w/out" || fail "the operator's version was not v4"
record "$id" '.owner == "contributor"' '.versions[3].uploader == "requester"' '.seq == 7'
ok as requester valley withdraw "$id"
record "$id" '.state == "withdrawn"' '.seq == 8' '.owner == "contributor"'

# --head marks exactly the head given, which must be on origin. An older
# head of the branch is on origin, and a commit nobody pushed is not.
commit five
unpushed="$(git rev-parse HEAD)"
unchanged "$id" refused 'push it there first' as contributor valley ready --head "$unpushed" --change "$id"
git reset --quiet --hard "$fourth"

# ----------------------------------------------------------------------
# Change identity. A jj change-id header in the head commit names the
# change; so does a Change-Id trailer in its message. The two disagreeing
# is refused, and so is a value that is no change ID.
header_id=zyxwvutsrqponmlkzyxwvutsrqponmlk
trailer_id=kkkkllllmmmmnnnnooooppppqqqqrrrr
git checkout --quiet -b from-header main
commit header
with_header "$header_id"
as contributor git push --quiet origin from-header
ok as contributor valley ready
grep -qx "project zyxw v1 ready" "$w/out" || fail 'the header did not name the change'
grep -q 'is new' "$w/out" && fail 'a change ID read from the header was reported as made'
record "$header_id" '.versions[0].branch == "from-header"'

git checkout --quiet -b from-trailer main
commit trailer "Change-Id: $trailer_id"
as contributor git push --quiet origin from-trailer
ok as contributor valley ready
grep -qx "project kkkk v1 ready" "$w/out" || fail 'the trailer did not name the change'
record "$trailer_id" '.versions[0].branch == "from-trailer"'

git checkout --quiet -b disagreeing main
commit disagreeing "Change-Id: $trailer_id"
with_header "$header_id"
as contributor git push --quiet origin disagreeing
unchanged "$trailer_id" unchanged "$header_id" \
  refused 'they must agree' as contributor valley ready
refused 'they must agree' as contributor valley ready --change "$id"

git checkout --quiet -b malformed main
commit malformed "Change-Id: I0123456789abcdef0123456789abcdef01234567"
as contributor git push --quiet origin malformed
refused 'which is not a change ID' as contributor valley ready

# A change is a line of commits from its merge base with main. A merge
# in that range is refused.
git checkout --quiet -b merged main
commit merged
git merge --quiet --no-ff -m merge from-trailer
as contributor git push --quiet origin merged
refused 'include the merge' as contributor valley ready

# ----------------------------------------------------------------------
# A change is named by its handle wherever a change is taken: status
# reads the branch its active version names, and withdraw takes a branch
# name too.
ok valley status kkkk
grep -q '^kkkk v1: not approved' "$w/out" || fail 'status did not read the change by its handle'
grep -q '^Change kkkk v1 on from-trailer, owned by contributor: trailer$' "$w/out" \
  || fail 'status did not say what the record says'
ok as contributor valley withdraw from-trailer
grep -qx 'project kkkk v1 withdrawn' "$w/out" || fail 'withdraw did not take a branch name'

# --stamp carries a new change's ID in an unpushed head as a trailer,
# pushes the branch, and marks it ready. It will not rewrite a head that
# is on origin in a change that is not the user's.
git checkout --quiet -b stamped main
commit stamped
ok as contributor valley ready --stamp
stamped_id="$(sed -n 's/^The change ID \([k-z]*\) is new\..*/\1/p' "$w/out")"
[ "${#stamped_id}" = 32 ] || fail '--stamp made no change ID'
[ "$(git log -1 --format='%(trailers:key=Change-Id,valueonly)' | head -n1)" = "$stamped_id" ] \
  || fail '--stamp did not carry the ID in the head'
[ "$(git rev-parse HEAD)" = "$(git -C "$origin" rev-parse refs/heads/stamped)" ] \
  || fail '--stamp did not push the stamped head'
record "$stamped_id" ".versions[0].head == \"git-sha1:$(git rev-parse HEAD)\""
git checkout --quiet -b pushed main
commit pushed
as contributor git push --quiet origin pushed
refused 'will not rewrite it' as integrator valley ready --stamp
# A change that has a record already is never stamped, whoever
# valley.principal says is asking: neither the local branch nor origin's
# moves.
git checkout --quiet stamped
commit restamped
restamped="$(git rev-parse HEAD)"
unchanged "$stamped_id" refused 'has a record on origin already' \
  as contributor valley ready --stamp --change "$stamped_id"
[ "$(git rev-parse HEAD)" = "$restamped" ] || fail '--stamp rewrote the head of a change with a record'
[ "$(git -C "$origin" rev-parse refs/heads/stamped)" != "$restamped" ] \
  || fail '--stamp pushed for a change with a record'

# ----------------------------------------------------------------------
# A change named by its handle is approved, reviewed and rebased at the
# head its record names, never at its branch's tip. The record's branch is
# only where that head is expected to be, and whoever can push the branch
# can move it, in the same push as the record if they like.
#
# approve derives what the change owes from the owning valley's floor,
# named at policy/valley on main. This floor owes nothing, so approving
# files a request with no evidence, and no signing key is needed.
valley="$w/valley.git"
git init --quiet --bare "$valley"
git init --quiet "$w/valley-work"
mkdir -p "$w/valley-work/policy/instance"
cat > "$w/valley-work/policy/instance/floor.cue" << 'CUE'
package verification

floor: {
	checks: {}
	classes: {}
	unclassified: {}
}
CUE
git -C "$w/valley-work" add -A
git -C "$w/valley-work" commit --quiet -m floor
git -C "$w/valley-work" push --quiet "$valley" HEAD:main
git checkout --quiet main
mkdir -p policy
echo "$valley" > policy/valley
git add policy/valley
git commit --quiet -m 'name the owning valley'
env -u VALLEY_PRINCIPAL git push --quiet origin main

# write_record <json> <parent…>: a record commit holding the JSON, written
# by hand, the way anyone holding a key could write one.
write_record() {
  local json="$1" blob tree parent
  local -a parents=()
  shift
  for parent in "$@"; do parents+=(-p "$parent"); done
  blob="$(printf '%s\n' "$json" | git hash-object -w --stdin)"
  tree="$(printf '100644 blob %s\trecord.json\n' "$blob" | git mktree)"
  git commit-tree "$tree" "${parents[@]}" -m 'a change record'
}
request=refs/the-valley/integration-requests/main/victim
no_request() {
  if git -C "$origin" rev-parse --verify --quiet "$request" > /dev/null; then
    fail "$1"
  fi
}

# The record names H on victim, and the same push puts B on victim.
target_id=ppppqqqqrrrrssssttttuuuuvvvvwwww
git checkout --quiet -b victim main
commit honest
honest="$(git rev-parse HEAD)"
commit substituted
substituted="$(git rev-parse HEAD)"
v1="$(jq -cn --arg id "$target_id" --arg head "git-sha1:$honest" '{schema: "valley-change-record/v1",
  change_id: $id, target: "main", owner: "contributor", state: "ready", seq: 1,
  versions: [{n: 1, head: $head, title: "honest", branch: "victim", uploader: "contributor"}], active: 1}')"
r1="$(write_record "$v1" "$honest")"
as contributor git push --quiet origin "$r1:refs/the-valley/changes/main/$target_id" \
  "$substituted:refs/heads/victim"
[ "$(git -C "$origin" rev-parse refs/heads/victim)" = "$substituted" ] || fail 'the branch did not move with the record'

refused "records the head $honest, and origin/victim is at $substituted" as requester valley approve pppp
no_request 'approve filed a request for a branch tip no version records'
refused "origin/victim is at $substituted; nothing rebased" as requester valley rebase pppp
[ "$(git -C "$origin" rev-parse refs/heads/victim)" = "$substituted" ] || fail 'rebase moved the branch'
# review shows the recorded head, and none of what the branch added.
printf 's\n' | valley review pppp > "$w/out" 2> "$w/err" || fail 'review of a handle failed'
grep -q '^change pppp v1 at' "$w/out" || fail 'review did not say which version it shows'
grep -q ' honest$' "$w/out" || fail 'review did not show the recorded head'
if grep -q 'substituted' "$w/out"; then fail 'review showed the branch tip, not the recorded head'; fi
# Rejecting it there deletes nothing while the branch is elsewhere: the
# refusal names both commits, and the next answer goes to the prompt.
printf 'r\nyes\ns\n' | as requester valley review pppp > "$w/out" 2> "$w/err" \
  || fail 'review of a handle failed'
grep -qF "records the head $honest, and origin/victim is at $substituted; nothing deleted" "$w/err" \
  || fail 'reject did not refuse a branch whose tip is not the recorded head'
[ "$(git -C "$origin" rev-parse refs/heads/victim)" = "$substituted" ] \
  || fail 'reject deleted a branch whose tip is not the recorded head'

# With the branch at the recorded head, approve files exactly that head.
as contributor git push --quiet --force origin "$honest:refs/heads/victim"
ok as requester valley approve pppp
[ "$(git -C "$origin" rev-parse "$request")" = "$honest" ] || fail 'approve did not file the recorded head'

# A second agent acting as the same principal adds v2 at a head that is
# not on victim, while its record says victim. approve refuses, and the
# request stays where it was.
git checkout --quiet -b elsewhere main
commit elsewhere
elsewhere="$(git rev-parse HEAD)"
as contributor git push --quiet origin elsewhere
v2="$(jq -c --arg head "git-sha1:$elsewhere" '.versions += [{n: 2, head: $head, title: "elsewhere",
  branch: "victim", uploader: "contributor"}] | .active = 2 | .seq = 2' <<< "$v1")"
r2="$(write_record "$v2" "$r1" "$elsewhere")"
as contributor git push --quiet origin "$r2:refs/the-valley/changes/main/$target_id"
refused "records the head $elsewhere, and origin/victim is at $honest" as requester valley approve pppp
[ "$(git -C "$origin" rev-parse "$request")" = "$honest" ] || fail 'a redirected version moved the request'
# With the branch at the recorded head, rejecting it in review deletes it.
as contributor git push --quiet --force origin "$elsewhere:refs/heads/victim"
printf 'r\nyes\n' | as requester valley review pppp > "$w/out" 2> "$w/err" \
  || fail 'rejecting a handle at its recorded head failed'
grep -qF 'rejected victim: deleted from origin' "$w/out" || fail 'reject did not say it deleted the branch'
if git -C "$origin" rev-parse --verify --quiet refs/heads/victim > /dev/null; then
  fail 'reject kept a branch at the recorded head'
fi

# ----------------------------------------------------------------------
# A draft is a change paused. unready makes a ready change a draft, and
# ready brings it back: at the head of its active version it is ready
# again at that version, and at a new head it gains the next one. pending
# lists a draft apart from the branches, and approve and rebase refuse it,
# naming its state. Until the integrator reads records, a request filed
# before a change was paused or withdrawn can still land it, so unready and
# withdraw warn when one is pending, and leave it for the operator.
git checkout --quiet -b drafts/paused main
commit paused
paused="$(git rev-parse HEAD)"
as contributor git push --quiet origin drafts/paused
ok as contributor valley ready
pid="$(sed -n 's/^The change ID \([k-z]*\) is new\..*/\1/p' "$w/out")"
ph="$(sed -n 's/^project \([k-z]*\) v1 ready$/\1/p' "$w/out")"
if [ "${#pid}" != 32 ] || [ -z "$ph" ]; then fail 'ready did not name the new change'; fi

ok as contributor valley unready "$ph"
grep -qx "project $ph v1 draft" "$w/out" || fail 'unready did not say it made a draft'
record "$pid" '.state == "draft"' '.seq == 2' '.active == 1' '.versions | length == 1'
one_parent "$pid"
if grep -q 'warning' "$w/err"; then fail 'unready warned with no request pending'; fi
unchanged "$pid" refused 'is a draft already' as contributor valley unready "$ph"

# pending lists the draft under drafts, and not among the branches, since
# its branch is at the draft's head.
ok valley pending
grep -qx 'drafts' "$w/out" || fail 'pending listed no drafts'
grep -q "^$ph v1 on drafts/paused | paused | " "$w/out" || fail 'pending did not list the draft'
if grep -q '^drafts/paused | ' "$w/out"; then fail 'pending listed a draft among the branches'; fi
grep -qE '^[k-z]{4,32} v[0-9]+ \| ' "$w/out" || fail 'pending no longer lists the ready changes'

# A draft is not approved or rebased. Refused in review, approving is no
# check failure, so the prompt still offers [a]pprove.
request_ref=refs/the-valley/integration-requests/main/drafts-paused
unchanged "$pid" refused "change $ph v1 is draft, not ready; nothing published" as requester valley approve "$ph"
if git -C "$origin" rev-parse --verify --quiet "$request_ref" > /dev/null; then
  fail 'approve filed a request for a draft'
fi
unchanged "$pid" refused "change $ph v1 is draft, not ready; nothing rebased" as requester valley rebase "$ph"
printf 'a\ns\n' | as requester valley review "$ph" > "$w/out" 2> "$w/err" || fail 'review of a draft failed'
grep -qF "change $ph v1 is draft, not ready; nothing published" "$w/err" \
  || fail "review's [a]pprove did not refuse a draft"
[ "$(grep -oF 'drafts/paused: [a]pprove, [r]eject, [s]kip? ' "$w/out" | wc -l)" = 2 ] \
  || fail "review's prompt changed after a refusal that was no check failure"
# Named by its branch, written either way, the draft is the same change,
# since its active version is the one record on that branch. It is
# refused the same way.
for name in drafts/paused origin/drafts/paused; do
  unchanged "$pid" refused "change $ph v1 is draft, not ready; nothing published" as requester valley approve "$name"
  unchanged "$pid" refused "change $ph v1 is draft, not ready; nothing rebased" as requester valley rebase "$name"
  printf 'a\ns\n' | as requester valley review "$name" > "$w/out" 2> "$w/err" || fail "review of $name failed"
  grep -qF "change $ph v1 is draft, not ready; nothing published" "$w/err" \
    || fail "review's [a]pprove did not refuse a draft named $name"
done
if git -C "$origin" rev-parse --verify --quiet "$request_ref" > /dev/null; then
  fail 'approve filed a request for a draft named by its branch'
fi

# ready at the same head is v1 again, and a draft once more after it.
ok as contributor valley ready --change "$pid"
grep -qx "project $ph v1 ready" "$w/out" || fail 'a draft readied at its head was not v1 again'
record "$pid" '.state == "ready"' '.seq == 3' '.active == 1' '.versions | length == 1'
one_parent "$pid"

# Approved now, the change has a request under its branch-derived name.
# Ready and at its recorded head, it is approved by its branch, written
# either way, and in review. unready voids nothing, so the request stays.
# It warns, because the integrator does not read records yet.
ok as requester valley approve drafts/paused
[ "$(git -C "$origin" rev-parse "$request_ref")" = "$paused" ] || fail 'approve did not file the ready change'
ok as requester valley approve origin/drafts/paused
grep -q '^Approved drafts/paused' "$w/out" || fail 'approve of origin/drafts/paused did not go ahead'
printf 'a\n' | as requester valley review drafts/paused > "$w/out" 2> "$w/err" || fail 'review of drafts/paused failed'
grep -q "^change $ph v1 at " "$w/out" || fail 'review of a branch did not read its change'
grep -q '^Approved drafts/paused' "$w/out" || fail "review's [a]pprove of drafts/paused did not go ahead"
ok as contributor valley unready "$ph"
grep -qF "valley: note: the integration request $request_ref for $ph is pending on origin" "$w/err" \
  || fail 'unready did not say a request is pending'
grep -qF 'lands once the change is ready again' "$w/err" || fail 'the note did not say when the request lands'
[ "$(git -C "$origin" rev-parse "$request_ref")" = "$paused" ] || fail 'unready touched the request'

# unready, then ready at a new head, is v2.
commit paused-again
paused_again="$(git rev-parse HEAD)"
as contributor git push --quiet origin drafts/paused
ok as contributor valley ready --change "$pid"
grep -qx "project $ph v2 ready" "$w/out" || fail 'a draft readied at a new head was not v2'
record "$pid" '.state == "ready"' '.seq == 5' '.active == 2' '.versions | length == 2' \
  ".versions[1].head == \"git-sha1:$paused_again\""

# withdraw warns of the same request, and the change, withdrawn, is never
# made a draft. ready at the same head brings it back at v2.
ok as contributor valley withdraw "$ph"
grep -qx "project $ph v2 withdrawn" "$w/out" || fail 'withdraw did not say what it withdrew'
grep -qF "valley: warning: the integration request $request_ref for $ph is pending on origin" "$w/err" \
  || fail 'withdraw did not warn of the pending request'
grep -qF 'so the operator must drop it' "$w/err" || fail 'the warning did not say who must drop the request'
record "$pid" '.state == "withdrawn"' '.seq == 6' '.active == 2'
unchanged "$pid" refused 'never made a draft' as contributor valley unready "$ph"
ok as contributor valley ready --change "$pid"
grep -qx "project $ph v2 ready" "$w/out" || fail 'a withdrawn change readied at its head was not v2 again'
record "$pid" '.state == "ready"' '.seq == 7' '.active == 2' '.versions | length == 2'
one_parent "$pid"

# A request named c-<change ID> is warned of too. withdraw takes a draft.
as requester git push --quiet origin --delete "$request_ref"
as requester git push --quiet origin "$paused_again:refs/the-valley/integration-requests/main/c-$pid"
ok as contributor valley unready "$ph"
grep -qF "the integration request refs/the-valley/integration-requests/main/c-$pid for $ph is pending" "$w/err" \
  || fail 'unready did not warn of a request named by the change ID'
if grep -qF "$request_ref " "$w/err"; then fail 'unready warned of a request that was dropped'; fi
ok as contributor valley withdraw "$ph"
record "$pid" '.state == "withdrawn"' '.seq == 9' '.active == 2'
grep -qF "main/c-$pid for $ph is pending" "$w/err" || fail 'withdraw did not warn of a request named by the change ID'

# ----------------------------------------------------------------------
# review, approve and rebase read the record again just before they act.
# A review opened while the change is ready refuses to approve or rebase
# it once another principal has unreadied it, and refuses to approve it
# once it has a new version. Its branch starts behind main, so the review
# offers [b]ase as well.
#
# review_while <change> <answers> <command…>: review the change as the
# operator, run the command once the review waits at its prompt, then
# give the answers.
review_while() {
  local name="$1" answers="$2" reviewer i
  shift 2
  # A review.out left from an earlier review would show a prompt this one
  # has not reached.
  rm -f "$w/fifo" "$w/review.out"
  mkfifo "$w/fifo"
  VALLEY_PRINCIPAL=requester valley review "$name" < "$w/fifo" > "$w/review.out" 2> "$w/review.err" &
  reviewer=$!
  exec 3> "$w/fifo"
  for i in $(seq 600); do
    if grep -qF '[s]kip? ' "$w/review.out" 2> /dev/null; then break; fi
    sleep 0.1
  done
  grep -qF '[s]kip? ' "$w/review.out" || fail "review of $name never reached its prompt (after $i tries)"
  "$@" > "$w/out" 2> "$w/err" || fail "$* failed while the review was open"
  printf '%b' "$answers" >&3
  exec 3>&-
  wait "$reviewer" || fail "review of $name failed"
}
git checkout --quiet -b drafts/raced main~1
commit raced
raced="$(git rev-parse HEAD)"
as contributor git push --quiet origin drafts/raced
ok as contributor valley ready
rid="$(sed -n 's/^The change ID \([k-z]*\) is new\..*/\1/p' "$w/out")"
rh="$(sed -n 's/^project \([k-z]*\) v1 ready$/\1/p' "$w/out")"
if [ "${#rid}" != 32 ] || [ -z "$rh" ]; then fail 'ready did not name the new change'; fi
raced_request=refs/the-valley/integration-requests/main/drafts-raced

review_while "$rh" 'a\nb\ns\n' as contributor valley unready "$rh"
grep -qF "change $rh v1 is draft, not ready; nothing published" "$w/review.err" \
  || fail "review's [a]pprove went ahead for a change unreadied while the review was open"
grep -qF "change $rh v1 is draft, not ready; nothing rebased" "$w/review.err" \
  || fail "review's [b]ase went ahead for a change unreadied while the review was open"
if git -C "$origin" rev-parse --verify --quiet "$raced_request" > /dev/null; then
  fail 'review filed a request for a change unreadied while it was open'
fi
[ "$(git -C "$origin" rev-parse refs/heads/drafts/raced)" = "$raced" ] \
  || fail 'review rebased a change unreadied while it was open'

ok as contributor valley ready --change "$rid"
commit raced-again
raced_again="$(git rev-parse HEAD)"
new_version() {
  as contributor git push --quiet origin drafts/raced && as contributor valley ready --change "$rid"
}
review_while "$rh" 'a\ns\n' new_version
grep -qF "change $rh is at v2, $raced_again, and this run began at v1, $raced; nothing published" "$w/review.err" \
  || fail "review's [a]pprove went ahead for a change that gained a version while the review was open"
if git -C "$origin" rev-parse --verify --quiet "$raced_request" > /dev/null; then
  fail 'review filed a request for a change that gained a version while it was open'
fi

# A name that read as a plain branch is read again before approve files.
# A review of a branch no record is on is left open while a second
# principal readies the branch and then unreadies it. [a]pprove then
# refuses, naming the change the branch has become and its state.
git checkout --quiet -b drafts/plain main
commit plain
as contributor git push --quiet origin drafts/plain
ready_then_unready() {
  as contributor valley ready drafts/plain > "$w/ready.out" || return 1
  plain_id="$(sed -n 's/^The change ID \([k-z]*\) is new\..*/\1/p' "$w/ready.out")"
  plain_handle="$(sed -n 's/^project \([k-z]*\) v1 ready$/\1/p' "$w/ready.out")"
  [ "${#plain_id}" = 32 ] && [ -n "$plain_handle" ] || return 1
  as contributor valley unready "$plain_id"
}
review_while drafts/plain 'a\ns\n' ready_then_unready
grep -q '^branch drafts/plain, log against origin/main' "$w/review.out" \
  || fail 'the review did not begin on a branch with no record'
record "$plain_id" '.state == "draft"'
grep -qF "drafts/plain is now change $plain_handle v1, which is draft, not ready; nothing published" "$w/review.err" \
  || fail "review's [a]pprove went ahead for a branch readied and unreadied while the review was open"
if git -C "$origin" rev-parse --verify --quiet refs/the-valley/integration-requests/main/drafts-plain > /dev/null; then
  fail 'review filed a request for a branch that became a draft while it was open'
fi

# ----------------------------------------------------------------------
# A request filed for an earlier version is named by that version's
# branch. withdraw warns of it, though the active version is on another.
git checkout --quiet -b moved/old main
commit old
as contributor git push --quiet origin moved/old
ok as contributor valley ready
old_id="$(sed -n 's/^The change ID \([k-z]*\) is new\..*/\1/p' "$w/out")"
old_handle="$(sed -n 's/^project \([k-z]*\) v1 ready$/\1/p' "$w/out")"
ok as requester valley approve "$old_handle"
old_request=refs/the-valley/integration-requests/main/moved-old
git -C "$origin" rev-parse --verify --quiet "$old_request" > /dev/null || fail 'approve filed no request for moved/old'
git checkout --quiet -b moved/new
commit new
as contributor git push --quiet origin moved/new
ok as contributor valley ready --change "$old_id"
grep -qx "project $old_handle v2 ready" "$w/out" || fail 'the version on moved/new was not v2'
ok as contributor valley withdraw "$old_handle"
grep -qF "valley: warning: the integration request $old_request for $old_handle is pending on origin" "$w/err" \
  || fail "withdraw did not warn of the request filed for v1's branch"

# ----------------------------------------------------------------------
# A branch no record's active version is on is a branch, as before
# records: review shows its tip, and approve files it under its own name.
printf 's\n' | valley review pushed > "$w/out" 2> "$w/err" || fail 'review of a branch with no record failed'
grep -q '^branch pushed, log against origin/main' "$w/out" || fail 'review read a change for a branch with no record'
ok as requester valley approve pushed
[ "$(git -C "$origin" rev-parse refs/the-valley/integration-requests/main/pushed)" \
  = "$(git -C "$origin" rev-parse refs/heads/pushed)" ] || fail 'approve did not file a branch with no record at its tip'

# A branch two records' active versions are on names neither: the verbs
# that act on a change refuse it, and list both IDs.
twin=llllmmmmnnnnoooollllmmmmnnnnoooo
ok as contributor valley ready --change "$twin" topic
for verb in approve rebase; do
  refused 'name one by its change ID' as requester valley "$verb" topic
  grep -qF "$id" "$w/err" || fail "$verb of topic did not list $id"
  grep -qF "$twin" "$w/err" || fail "$verb of topic did not list $twin"
done
refused 'name one by its change ID' as requester valley approve origin/topic

echo 'valley-ready: change records through real pushes passed'
touch "$out"
