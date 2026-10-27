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
# --stamp reads the local branch by its exact name. With no local ghost,
# git rev-parse would take the branch named refs/heads/ghost, which is
# refs/heads/refs/heads/ghost, for it, and stamp and push that.
git branch refs/heads/ghost "$(git commit-tree -p main -m ghost 'main^{tree}')"
refused 'there is no such branch' as contributor valley ready --stamp ghost
git -C "$origin" show-ref --exists refs/heads/ghost 2> /dev/null \
  && fail '--stamp pushed the branch named like the missing one'
git branch -D --quiet refs/heads/ghost

# ----------------------------------------------------------------------
# A change named by its handle is approved, reviewed and rebased at the
# head its record names, never at its branch's tip. The record's branch is
# only where that head is expected to be, and whoever can push the branch
# can move it, in the same push as the record if they like.
#
# approve derives what the change requires from the owning valley's floor,
# named at policy/valley on main. This floor requires nothing, so approving
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
no_request() {
  if git -C "$origin" rev-parse --verify --quiet "$request" > /dev/null; then
    fail "$1"
  fi
}

# The record names H on victim, and the same push puts B on victim.
target_id=ppppqqqqrrrrssssttttuuuuvvvvwwww
request="refs/the-valley/integration-requests/main/c-$target_id"
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

# approve binds the head it approves. Off a terminal it needs --head, and
# a head no version records is refused.
refused 'off a terminal, pass it with --head' as requester valley approve pppp
no_request 'approve filed a request with no head bound to it'
refused "$substituted is no version of change pppp" as requester valley approve --head "$substituted" pppp
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

# The dashboard's approve names the head it showed, and approve files
# exactly that head under the change's request name, wherever the branch
# is: here the branch is still at the substituted commit.
ok as requester valley approve pppp --head "$honest"
[ "$(git -C "$origin" rev-parse "$request")" = "$honest" ] || fail 'approve did not file the recorded head'
grep -q "^Approved pppp v1 " "$w/out" || fail 'approve did not say which version it approved'
grep -qx "  request  $request -> $(git rev-parse --short "$honest")" "$w/out" \
  || fail 'approve did not file the request as c-<change ID>'
if git -C "$origin" rev-parse --verify --quiet refs/the-valley/integration-requests/main/victim > /dev/null; then
  fail 'approve filed a change with a record under its branch name'
fi
as contributor git push --quiet --force origin "$honest:refs/heads/victim"

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
# approve takes the head of any version, active or not, since the request
# names the head: a version is a head its owner marked ready. On a
# terminal, approve with no --head shows the active version and asks.
printf 'n\n' | script -qec "VALLEY_PRINCIPAL=requester valley approve pppp" /dev/null > "$w/out" 2>&1 \
  && fail 'approve went ahead when its confirmation was refused'
grep -qF "approve change pppp v2 at $elsewhere, elsewhere? [y/N]" "$w/out" \
  || fail 'approve did not show the active head and title to confirm'
[ "$(git -C "$origin" rev-parse "$request")" = "$honest" ] || fail 'a refused confirmation moved the request'
printf 'y\n' | script -qec "VALLEY_PRINCIPAL=requester valley approve pppp" /dev/null > "$w/out" 2>&1 \
  || fail 'approve on a terminal failed once confirmed'
[ "$(git -C "$origin" rev-parse "$request")" = "$elsewhere" ] || fail 'a confirmed approval did not file the active head'
ok as requester valley approve --head "$honest" pppp
[ "$(git -C "$origin" rev-parse "$request")" = "$honest" ] || fail 'approve did not file an older version head'
ok as requester valley approve --head "$elsewhere" pppp
[ "$(git -C "$origin" rev-parse "$request")" = "$elsewhere" ] || fail 'approve did not file the active head'

# status reads the change at its active head, under c-<change ID>, and
# says what its record says.
ok valley status --json pppp
jq -e --arg id "$target_id" --arg head "$elsewhere" --arg ref "$request" '
  .change == "victim" and .request_name == ("c-" + $id) and .head == $head
  and .request == {ref: $ref, commit: $head} and .state == "awaiting-decision"
  and .change_id == $id and .handle == "pppp" and .version == 2 and .title == "elsewhere"
  and .owner == "contributor" and .landing == "not-landed"' "$w/out" > /dev/null \
  || fail "status --json pppp: $(cat "$w/out")"
ok valley status --json pushed
jq -e '.change_id == null and .handle == null and .version == null and .title == null
  and .owner == null and .landing == null' "$w/out" > /dev/null \
  || fail "status --json of a branch with no record: $(cat "$w/out")"
# With the branch at the recorded head, rejecting it in review deletes it.
as contributor git push --quiet --force origin "$elsewhere:refs/heads/victim"
printf 'r\nyes\n' | as requester valley review pppp > "$w/out" 2> "$w/err" \
  || fail 'rejecting a handle at its recorded head failed'
grep -qF 'rejected victim: deleted from origin' "$w/out" || fail 'reject did not say it deleted the branch'
if git -C "$origin" rev-parse --verify --quiet refs/heads/victim > /dev/null; then
  fail 'reject kept a branch at the recorded head'
fi

# A version may record no branch: the record allows one, though valley
# ready always names a branch. The change's record line then has an empty
# field in its middle, and approve, review and rebase must still read the
# state after it. approve files the head alone. review and rebase act on a
# branch, so they refuse plainly, and a draft's refusal names no branch in
# the ready line it suggests.
bare_id=qqqqkkkkllllmmmmnnnnooooppppqqqq
bare_request="refs/the-valley/integration-requests/main/c-$bare_id"
git checkout --quiet -b branchless main
commit branchless
branchless="$(git rev-parse HEAD)"
b1="$(jq -cn --arg id "$bare_id" --arg head "git-sha1:$branchless" '{schema: "valley-change-record/v1",
  change_id: $id, target: "main", owner: "contributor", state: "ready", seq: 1,
  versions: [{n: 1, head: $head, title: "branchless", branch: "", uploader: "contributor"}], active: 1}')"
as contributor git push --quiet origin "$(write_record "$b1" "$branchless"):refs/the-valley/changes/main/$bare_id"
ok as requester valley approve --head "$branchless" qqqq
[ "$(git -C "$origin" rev-parse "$bare_request")" = "$branchless" ] || fail 'approve did not file a change with no branch'
grep -q '^Approved qqqq v1 ' "$w/out" || fail 'approve did not say which version of a change with no branch it approved'
for verb in review rebase; do
  refused "change qqqq v1 records no branch, and $verb acts on a change's branch" valley "$verb" qqqq
done
ok as contributor valley unready qqqq
refused "change qqqq v1 is draft, not ready; nothing published. Ready it first: valley ready --change $bare_id" \
  as requester valley approve --head "$branchless" qqqq
if grep -qF "''" "$w/err"; then fail "approve suggested readying a change with no branch on the branch ''"; fi

# A name no record is on is acted on as a branch only when it is a branch
# name. resolve prints the name in a line of fields that bin/valley
# splits, so a name holding a separator could otherwise read as main.
before="$(git -C "$origin" for-each-ref refs/the-valley)"
for verb in approve review; do
  refused 'is not a branch name in UTF-8' as requester valley "$verb" $'main\x1fjunk'
done
[ "$(git -C "$origin" for-each-ref refs/the-valley)" = "$before" ] \
  || fail 'a name holding a separator published something'

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
grep -A1 "^$ph v1 on drafts/paused  .* ago · " "$w/out" | grep -qx '  paused' || fail 'pending did not list the draft'
if grep -qE '(^|└ |├ )drafts/paused  ' "$w/out"; then fail 'pending listed a draft among the branches'; fi
grep -qE '(^|└ |├ )[k-z]{4,32} v[0-9]+  ' "$w/out" || fail 'pending no longer lists the ready changes'

# A draft is not approved or rebased. review says so on the first line it
# shows, above the diff, and again in place of its prompt, which offers
# only [v]iew full diff and [s]kip: no [a]pprove, no [b]ase, and no
# [r]eject, which would delete the branch its owner may be working on. An
# answer it does not offer is answered with what it does, and acts on
# nothing.
request_ref="refs/the-valley/integration-requests/main/c-$pid"
unchanged "$pid" refused "change $ph v1 is draft, not ready; nothing published" as requester valley approve "$ph"
if git -C "$origin" rev-parse --verify --quiet "$request_ref" > /dev/null; then
  fail 'approve filed a request for a draft'
fi
unchanged "$pid" refused "change $ph v1 is draft, not ready; nothing rebased" as requester valley rebase "$ph"
drafted="$ph v1 is a draft: its owner (contributor) hasn't marked it ready, so it can't be approved yet."
# review_draft <name>: review the draft by that name, answering a, b and
# r before s. A prompt ends with no newline, so what follows it is on its
# line.
review_draft() {
  printf 'a\nb\nr\ns\n' | as requester valley review "$1" > "$w/out" 2> "$w/err" || fail "review of $1 failed"
  [ "$(head -n 1 "$w/out")" = "$drafted" ] || fail "review of $1 did not open with the draft's line"
  grep -q "^change $ph v1 at .*, on branch drafts/paused, log against origin/main:$" "$w/out" \
    || fail "review of $1 did not show the draft"
  [ "$(grep -oF "$drafted [v]iew full diff, [s]kip? " "$w/out" | wc -l)" = 4 ] \
    || fail "review of $1 did not say the draft's line in place of each prompt"
  [ "$(grep -oF '[s]kip? answer v or s' "$w/out" | wc -l)" = 3 ] || fail "review of $1 took an answer it does not offer"
  grep -qF '[s]kip? skipped drafts/paused; nothing done' "$w/out" || fail "review of $1 did not skip the draft"
  if grep -qF -e '[a]pprove' -e '[b]ase' -e '[r]eject' -e 'really delete' "$w/out"; then
    fail "review of $1 offered what a draft can't take"
  fi
  if grep -q 'not ready' "$w/err"; then fail "review of $1 ran approve or rebase on the draft"; fi
  [ "$(git -C "$origin" rev-parse refs/heads/drafts/paused)" = "$paused" ] || fail "review of $1 moved the draft's branch"
}
unchanged "$pid" review_draft "$ph"
# Named by its branch, written either way, the draft is the same change,
# since its active version is the one record on that branch. It is
# refused, and reviewed, the same way.
for name in drafts/paused origin/drafts/paused; do
  unchanged "$pid" refused "change $ph v1 is draft, not ready; nothing published" as requester valley approve "$name"
  unchanged "$pid" refused "change $ph v1 is draft, not ready; nothing rebased" as requester valley rebase "$name"
  unchanged "$pid" review_draft "$name"
done
if git -C "$origin" rev-parse --verify --quiet "$request_ref" > /dev/null; then
  fail 'approve filed a request for a draft named by its branch'
fi

# ready at the same head is v1 again, and a draft once more after it.
ok as contributor valley ready --change "$pid"
grep -qx "project $ph v1 ready" "$w/out" || fail 'a draft readied at its head was not v1 again'
record "$pid" '.state == "ready"' '.seq == 3' '.active == 1' '.versions | length == 1'
one_parent "$pid"

# Approved now, the change has a request under its own name, c-<change
# ID>. Ready and at its recorded head, it is approved by its branch,
# written either way, and in review. unready voids nothing, so the request
# stays, and the integrator holds it back until the change is ready again.
ok as requester valley approve --head "$paused" drafts/paused
[ "$(git -C "$origin" rev-parse "$request_ref")" = "$paused" ] || fail 'approve did not file the ready change'
ok as requester valley approve --head "$paused" origin/drafts/paused
grep -q "^Approved $ph v1 " "$w/out" || fail 'approve of origin/drafts/paused did not go ahead'
printf 'a\n' | as requester valley review drafts/paused > "$w/out" 2> "$w/err" || fail 'review of drafts/paused failed'
grep -q "^change $ph v1 at " "$w/out" || fail 'review of a branch did not read its change'
grep -q "^Approved $ph v1 " "$w/out" || fail "review's [a]pprove of drafts/paused did not go ahead"
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

# A request named for the change's branch, as approve named one before it
# named a change by its ID, is warned of too. withdraw takes a draft.
as requester git push --quiet origin --delete "$request_ref"
as requester git push --quiet origin "$paused_again:refs/the-valley/integration-requests/main/drafts-paused"
ok as contributor valley unready "$ph"
grep -qF "the integration request refs/the-valley/integration-requests/main/drafts-paused for $ph is pending" "$w/err" \
  || fail 'unready did not say a request named for the branch is pending'
if grep -qF "$request_ref " "$w/err"; then fail 'unready warned of a request that was dropped'; fi
ok as contributor valley withdraw "$ph"
record "$pid" '.state == "withdrawn"' '.seq == 9' '.active == 2'
grep -qF "main/drafts-paused for $ph is pending" "$w/err" || fail 'withdraw did not warn of a request named for the branch'

# review shows a withdrawn change as it shows a draft, with a line of its
# own.
withdrawn="$ph is withdrawn: there is nothing to approve."
printf 'a\ns\n' | as requester valley review "$ph" > "$w/out" 2> "$w/err" || fail 'review of a withdrawn change failed'
[ "$(head -n 1 "$w/out")" = "$withdrawn" ] || fail 'review did not open with the withdrawn line'
grep -qF "$withdrawn [v]iew full diff, [s]kip? answer v or s" "$w/out" \
  || fail 'review did not say the withdrawn line in place of its prompt'
if grep -qF -e '[a]pprove' -e '[b]ase' -e '[r]eject' "$w/out"; then fail "review offered what a withdrawn change can't take"; fi

# ----------------------------------------------------------------------
# review, approve and rebase read the record again just before they act.
# A review opened while the change is ready refuses to approve or rebase
# it once another principal has unreadied it, and refuses to approve it
# once it has a new version. Its branch starts behind main, so the review
# offers [b]ase as well.
#
# review_while <change> <answers> <command…>: review the change as the
# operator, run the command once the review waits at its prompt, then
# give the answers. Every prompt that follows a change shown offers
# [v]iew full diff, so that is the marker waited on, whatever else the
# prompt offers.
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
    if grep -qF '[v]iew full diff' "$w/review.out" 2> /dev/null; then break; fi
    sleep 0.1
  done
  grep -qF '[v]iew full diff' "$w/review.out" || fail "review of $name never reached its prompt (after $i tries)"
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
raced_request="refs/the-valley/integration-requests/main/c-$rid"

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
# Shown as a draft from the start, the change is offered neither
# [a]pprove nor [b]ase, though its branch does not fast-forward from main.
printf 'b\ns\n' | as requester valley review "$rh" > "$w/out" 2> "$w/err" || fail "review of the draft $rh failed"
grep -qF "$rh v1 is a draft: its owner (contributor) hasn't marked it ready, so it can't be approved yet. [v]iew full diff, [s]kip? answer v or s" "$w/out" \
  || fail 'review of a stale draft did not say it is a draft in place of its prompt'
if grep -qF -e '[a]pprove' -e '[b]ase' "$w/out"; then fail 'review offered a stale draft [a]pprove or [b]ase'; fi
[ "$(git -C "$origin" rev-parse refs/heads/drafts/raced)" = "$raced" ] || fail 'review rebased a draft'

ok as contributor valley ready --change "$rid"
commit raced-again
raced_again="$(git rev-parse HEAD)"
new_version() {
  as contributor git push --quiet origin drafts/raced && as contributor valley ready --change "$rid"
}
# A version added while the review is open changes nothing [a]pprove
# files: it approves the head the review showed, which is still a version.
review_while "$rh" 'a\ns\n' new_version
grep -q "^Approved $rh v1 " "$w/review.out" \
  || fail "review's [a]pprove did not approve the head it showed: $(cat "$w/review.err")"
[ "$(git -C "$origin" rev-parse "$raced_request")" = "$raced" ] \
  || fail 'review filed another head than the one it showed'

# rebase rebases the active version's head, pushes the branch, and adds
# the rebased head as a new version, as the operator. ready --if-active,
# which it does that with, adds nothing unless the change is ready at that
# active head.
unchanged "$rid" refused "$rh v2 is at" as contributor valley ready --if-active "$raced" --change "$rid"
ok as requester valley rebase "$rh"
rebased="$(git -C "$origin" rev-parse refs/heads/drafts/raced)"
[ "$rebased" != "$raced_again" ] || fail 'rebase did not move the branch'
grep -qx "project $rh v3 ready" "$w/out" || fail 'rebase did not add the rebased head as v3'
record "$rid" '.state == "ready"' '.active == 3' '.versions | length == 3' \
  ".versions[2].head == \"git-sha1:$rebased\"" '.versions[2].uploader == "requester"' \
  '.versions[2].branch == "drafts/raced"' '.owner == "contributor"'

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
old_request=refs/the-valley/integration-requests/main/moved-old
as requester git push --quiet origin "$(git rev-parse HEAD):$old_request"
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

# c-<change ID> is the request name of the change with that ID, so a
# branch whose request name would be one is neither approved nor read by
# that name.
reserved=c-kkkkllllmmmmnnnnooooppppqqqqrrrr
git checkout --quiet -b "$reserved" main
commit reserved
as contributor git push --quiet origin "$reserved"
refused 'that name is reserved for the change with the ID' as requester valley approve "$reserved"
if git -C "$origin" rev-parse --verify --quiet "refs/the-valley/integration-requests/main/$reserved" > /dev/null; then
  fail "approve filed a branch under a change's request name"
fi
refused 'that name is reserved for the change with the ID' valley status "$reserved"

# ----------------------------------------------------------------------
# A stack is rebased whole: every layer onto the rebased layer below it,
# and the bottom onto main, with one atomic push of every branch, and then
# each change's rebased head added to its record as a new version.
#
# new_change <branch>: origin's head of the branch readied by the
# contributor as a new change, whose ID and handle are left in nid and nh.
new_change() {
  ok as contributor valley ready "$1"
  nid="$(sed -n 's/^The change ID \([k-z]*\) is new\..*/\1/p' "$w/out")"
  nh="$(sed -n 's/^project \([k-z]*\) v1 ready$/\1/p' "$w/out")"
  if [ "${#nid}" != 32 ] || [ -z "$nh" ]; then fail "ready $1 did not name a new change"; fi
}
# build_stack <name>: three layers on main, each adding files of its own:
# <name>/one, a plain branch, then <name>/two and <name>/three, changes the
# contributor readies. The top has two commits. The heads are left in
# one, two and three, and the changes' IDs and handles in id2, h2, id3
# and h3.
build_stack() {
  local from=main layer
  for layer in one two three; do
    git checkout --quiet -b "$1/$layer" "$from"
    commit "$1-$layer"
    as contributor git push --quiet origin "$1/$layer"
    from="$1/$layer"
  done
  commit "$1-three-more"
  as contributor git push --quiet origin "$1/three"
  git checkout --quiet main
  one="$(git rev-parse "$1/one")" two="$(git rev-parse "$1/two")" three="$(git rev-parse "$1/three")"
  new_change "$1/two"
  id2="$nid" h2="$nh"
  new_change "$1/three"
  id3="$nid" h3="$nh"
}
# move_main <subject>: main moves on, adding a file of its own.
move_main() {
  git checkout --quiet main
  commit "$1"
  env -u VALLEY_PRINCIPAL git push --quiet origin main
}
at() { git -C "$origin" rev-parse "$1"; }
# moved_nothing <name>: no branch of the stack and no record has moved
# since $before was read.
stack_refs() {
  git -C "$origin" for-each-ref --format='%(objectname) %(refname)' "refs/heads/$1/" refs/the-valley/changes/
}
moved_nothing() {
  [ "$(stack_refs "$1")" = "$before" ] || fail "the rebase of $1 moved something"
}
# same_delta <old base> <old head> <new base> <new head>: a layer's diff
# from its base is what it was.
same_delta() {
  [ "$(git -C "$origin" diff "$1" "$2")" = "$(git -C "$origin" diff "$3" "$4")" ] \
    || fail "the diff of $2 from $1 changed in the rebase"
}

# A three-layer stack with main moved under it, without conflict. Every
# branch moves, each change gains a ready version at its new head, the
# operator's, and every layer keeps its diff, on the rebased layer below.
build_stack s1
oldmain="$(at refs/heads/main)"
move_main s1-main
main="$(at refs/heads/main)"
ok as requester valley rebase "$h3"
new1="$(at refs/heads/s1/one)" new2="$(at refs/heads/s1/two)" new3="$(at refs/heads/s1/three)"
for b in "$one:$new1" "$two:$new2" "$three:$new3"; do
  [ "${b%%:*}" != "${b#*:}" ] || fail "the rebase left a branch of s1 where it was"
done
record "$id2" '.state == "ready"' '.active == 2' ".versions[1].head == \"git-sha1:$new2\"" \
  '.versions[1].uploader == "requester"' '.versions[1].branch == "s1/two"'
record "$id3" '.state == "ready"' '.active == 2' ".versions[1].head == \"git-sha1:$new3\"" \
  '.versions[1].uploader == "requester"' '.versions[1].branch == "s1/three"'
git -C "$origin" merge-base --is-ancestor "$main" "$new1" || fail 'the bottom is not on main'
git -C "$origin" merge-base --is-ancestor "$new1" "$new2" || fail 'the second layer is not on the first'
git -C "$origin" merge-base --is-ancestor "$new2" "$new3" || fail 'the top is not on the second layer'
same_delta "$oldmain" "$one" "$main" "$new1"
same_delta "$one" "$two" "$new1" "$new2"
same_delta "$two" "$three" "$new2" "$new3"
grep -qx "the stack under $h3, bottom first:" "$w/out" || fail 'the rebase printed no manifest'
grep -qE "^  s1/one +${one:0:7} -> ${new1:0:7}  origin/s1/one pushed$" "$w/out" || fail 'the manifest misread s1/one'
grep -qE "^  $h2 +${two:0:7} -> ${new2:0:7}  origin/s1/two pushed, v2 added$" "$w/out" || fail "the manifest misread $h2"
grep -qE "^  $h3 +${three:0:7} -> ${new3:0:7}  origin/s1/three pushed, v2 added$" "$w/out" || fail "the manifest misread $h3"
if grep -q '^to finish' "$w/out"; then fail 'a finished rebase printed a command to finish it'; fi
# Rebased again, a stack that fast-forwards from main is left as it is.
before="$(stack_refs s1)"
ok as requester valley rebase "$h3"
grep -qx 's1/three already fast-forwards from origin/main; nothing rebased' "$w/out" \
  || fail 'a stack that fast-forwards was not left alone'
moved_nothing s1

# A conflict in the middle layer pushes nothing, records nothing, and
# names the layer.
build_stack s3
git checkout --quiet main
echo 'main has its own' > s3-two.txt
git add s3-two.txt
git commit --quiet -m 'main has its own s3-two'
env -u VALLEY_PRINCIPAL git push --quiet origin main
before="$(stack_refs s3)"
refused "$h2, layer 2 of 3 under $h3, conflicts with s3/one as rebased — resolve by hand; nothing changed" \
  as requester valley rebase "$h3"
moved_nothing s3

# Each refusal before the rebase changes nothing.
build_stack s4
move_main s4-main
before="$(stack_refs s4)"
# A lower change that is a draft.
ok as contributor valley unready "$h2"
before="$(stack_refs s4)"
refused "change $h2 v1, layer 2 of 3 under $h3, is draft, not ready; nothing rebased. Ready it first: valley ready --change $id2 s4/two" \
  as requester valley rebase "$h3"
moved_nothing s4
ok as contributor valley ready --change "$id2" s4/two
before="$(stack_refs s4)"
# A layer's branch missing on origin.
git -C "$origin" update-ref -d refs/heads/s4/two
refused "change $h2 v1, layer 2 of 3 under $h3, is on the branch s4/two, and origin has no such branch; nothing rebased" \
  as requester valley rebase "$h3"
git -C "$origin" update-ref refs/heads/s4/two "$two"
moved_nothing s4
# A layer's branch at another commit than its recorded head.
git checkout --quiet -b s4-elsewhere "$two"
commit s4-elsewhere
elsewhere="$(git rev-parse HEAD)"
git checkout --quiet main
as contributor git push --quiet --force origin "$elsewhere:refs/heads/s4/two"
refused "change $h2 v1, layer 2 of 3 under $h3, records the head $two, and origin/s4/two is at $elsewhere; nothing rebased. Ready the branch's head as a new version of $h2 to act on it: valley ready --head $elsewhere --change $id2 s4/two" \
  as requester valley rebase "$h3"
git -C "$origin" update-ref refs/heads/s4/two "$two"
moved_nothing s4
# Two layers on one branch: s4/two at the top's first commit is a plain
# branch in the stack, and the change under it is on s4/two as well.
git -C "$origin" update-ref refs/heads/s4/two "$three~1"
refused "$h2 and s4/two, layers 2 and 3 of 4 under $h3, are both on the branch s4/two; nothing rebased" \
  as requester valley rebase "$h3"
git -C "$origin" update-ref refs/heads/s4/two "$two"
moved_nothing s4
# No valley.principal: the refusal gives the command that sets it.
refused "git config valley.principal names none, so nothing is rebased; set it with git config valley.principal <name>" \
  as anonymous valley rebase "$h3"
moved_nothing s4
# A draft's earlier version head among the commits to be rebased. The
# draft readied s4/one's head as its v1, then readied a head elsewhere as
# v2, and was unreadied. s4/one is a plain branch again, and its head is
# a version head of a change that is not ready.
ok as contributor valley ready --head "$one" s4/one
did="$(sed -n 's/^The change ID \([k-z]*\) is new\..*/\1/p' "$w/out")"
dh="$(sed -n 's/^project \([k-z]*\) v1 ready$/\1/p' "$w/out")"
git checkout --quiet -b s4/draft main
commit s4-draft
as contributor git push --quiet origin s4/draft
git checkout --quiet main
ok as contributor valley ready --change "$did" s4/draft
ok as contributor valley unready "$dh"
before="$(stack_refs s4)"
refused "the commit ${one:0:7} under $h3 is v1 of change $dh, which is draft, not ready; nothing rebased" \
  as requester valley rebase "$h3"
moved_nothing s4

# race_hook <command>: the command runs once, from the next rebase's
# post-rewrite hook: after the stack is read and a layer is rebased, and
# before anything is pushed.
race_hook() {
  cat > "$repo/.git/hooks/post-rewrite" << EOF
#!/bin/sh
[ -e "$w/race-armed" ] || exit 0
rm -f "$w/race-armed"
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_PREFIX
cd "$repo" && $1
exit 0
EOF
  chmod +x "$repo/.git/hooks/post-rewrite"
  touch "$w/race-armed"
}
disarm_hook() {
  rm -f "$repo/.git/hooks/post-rewrite"
  [ ! -e "$w/race-armed" ] || fail 'the race hook never ran'
}

# A layer's branch moves after the rebase read it: the atomic push is
# refused, and no branch moves.
build_stack s5
move_main s5-main
git checkout --quiet -b s5-moved "$two"
commit s5-moved
moved="$(git rev-parse HEAD)"
as contributor git push --quiet origin "$moved:refs/heads/s5-moved"
git checkout --quiet main
race_hook "git -C '$origin' update-ref refs/heads/s5/two $moved"
refused 'origin refused the push of the stack'"'"'s branches, so none moved' as requester valley rebase "$h3"
disarm_hook
[ "$(at refs/heads/s5/one)" = "$one" ] || fail 'a refused push moved s5/one'
[ "$(at refs/heads/s5/two)" = "$moved" ] || fail 'a refused push moved s5/two'
[ "$(at refs/heads/s5/three)" = "$three" ] || fail 'a refused push moved s5/three'
record "$id2" '.active == 1'
record "$id3" '.active == 1'
grep -qE "^  $h3 +.* origin/s5/three not pushed, no version added$" "$w/out" || fail 'the manifest did not say nothing was pushed'

# A plain branch in the stack becomes a change after the rebase read it:
# the last read before the push refuses it, and nothing moves.
build_stack s6
move_main s6-main
before="$(stack_refs s6)"
race_hook "valley ready s6/one > '$w/raced.out'"
refused "origin/s6/one, layer 1 of 3 under $h3, is now change" as requester valley rebase "$h3"
disarm_hook
grep -qF 'nothing pushed. Rebase it again by its handle' "$w/err" || fail 'the refusal did not say what to do'
[ "$(at refs/heads/s6/one)" = "$one" ] || fail 's6/one moved'
[ "$(at refs/heads/s6/two)" = "$two" ] || fail 's6/two moved'
[ "$(at refs/heads/s6/three)" = "$three" ] || fail 's6/three moved'
record "$id2" '.active == 1'
record "$id3" '.active == 1'

# The middle change is unreadied after the branches are pushed and before
# its record is written. The other layers are finished, the manifest gives
# the exact command that finishes the middle one, the exit is non-zero,
# and that command finishes it. A stand-in for origin's receive-pack
# unreadies it in front of the second push: the first moves the branches,
# and the second writes the middle change's record.
build_stack s7
move_main s7-main
cat > "$w/racing-receive-pack" << EOF
#!/bin/sh
n=\$((\$(cat "$w/pushes" 2> /dev/null || echo 0) + 1))
echo "\$n" > "$w/pushes"
if [ "\$n" = 2 ]; then
  (unset GIT_DIR; cd "$repo" && valley unready $id2 > /dev/null 2>&1)
fi
exec git-receive-pack "\$@"
EOF
chmod +x "$w/racing-receive-pack"
rm -f "$w/pushes"
git config remote.origin.receivepack "$w/racing-receive-pack"
refused '1 change of the stack under' as requester valley rebase "$h3"
git config --unset remote.origin.receivepack
[ "$(cat "$w/pushes")" -ge 3 ] || fail 'the record writes did not follow the branch push'
new2="$(at refs/heads/s7/two)" new3="$(at refs/heads/s7/three)"
[ "$new2" != "$two" ] || fail 's7/two did not move'
record "$id2" '.state == "draft"' '.active == 1'
record "$id3" '.state == "ready"' '.active == 2' ".versions[1].head == \"git-sha1:$new3\""
grep -qE "^  $h2 +.* origin/s7/two pushed, no version added$" "$w/out" || fail "the manifest did not say $h2 is unfinished"
grep -qE "^  $h3 +.* origin/s7/three pushed, v2 added$" "$w/out" || fail "the manifest did not say $h3 is finished"
finish="$(sed -n "s/^to finish $h2: //p" "$w/out")"
[ "$finish" = "valley ready --head $new2 --change $id2 s7/two" ] || fail "the manifest gave the command: $finish"
# Run again, the rebase refuses the stack, since a branch in it holds a
# head no version of its change records, and gives the same command.
refused "Ready the branch's head as a new version of $h2 to act on it: $finish" as requester valley rebase "$h3"
eval "ok as contributor $finish"
record "$id2" '.state == "ready"' '.active == 2' ".versions[1].head == \"git-sha1:$new2\""

# A second stack shares the bottom layer. The rebase names it as on an
# old head of that layer, and does not move it.
build_stack s9
git checkout --quiet -b s9/side "$one"
commit s9-side
side="$(git rev-parse HEAD)"
as contributor git push --quiet origin s9/side
git checkout --quiet main
move_main s9-main
ok as requester valley rebase "$h3"
grep -qx 's9/side is now on an old head of s9/one; rebase it with valley rebase s9/side' "$w/out" \
  || fail 'the rebase did not name the stack left on an old head'
[ "$(at refs/heads/s9/side)" = "$side" ] || fail 'the rebase moved another stack'

# A stack whose middle layer is a draft. review says so on that layer's
# page and in its prompt, and offers [a]pprove neither there nor above it,
# since approving either would land the draft. The bottom, under it, is
# offered [a]pprove as before. A withdrawn middle layer is shown the same
# way.
build_stack s10
ok as contributor valley unready "$h2"
s10_request="refs/the-valley/integration-requests/main/c-$id3"
printf 'n\na\nn\na\ns\n' | as requester valley review "$h3" > "$w/out" 2> "$w/err" || fail "review of the stack under $h3 failed"
drafted="$h2 v1 is a draft: its owner (contributor) hasn't marked it ready, so it can't be approved yet."
grep -qF "s10/one, 1 of 3: [n]ext layer, [a]pprove s10/one alone, [v]iew full diff, [s]top? " "$w/out" \
  || fail 'the layer under the draft was not offered [a]pprove'
grep -A1 "change 2 of 3: $h2 on s10/one\$" "$w/out" | grep -qxF "$drafted" \
  || fail "the draft layer's page did not say it is a draft under its heading"
[ "$(grep -oF "$h2, 2 of 3: $drafted [n]ext layer, [v]iew full diff, [s]top? " "$w/out" | wc -l)" = 2 ] \
  || fail "the draft layer's prompt did not say it is a draft in place of [a]pprove"
grep -qF '[s]top? answer n, v, or s' "$w/out" || fail 'the draft layer took [a]pprove'
held="it would bring $h2, change 2 of 3, which is a draft, so it can't be approved yet."
[ "$(grep -oF "$h3, 3 of 3: $held [v]iew full diff, [r]eject $h3, [s]top? " "$w/out" | wc -l)" = 2 ] \
  || fail 'the top above the draft did not say why it cannot be approved'
grep -qF '[s]top? answer v, r, or s' "$w/out" || fail 'the top above the draft took [a]pprove'
grep -qF "[s]top? stopped reviewing $h3 at change 3 of 3; nothing done" "$w/out" || fail 'the review of the stack did not stop'
if grep -qF -e "[a]pprove all 3" -e "[a]pprove s10/one and $h2" "$w/out"; then
  fail 'review offered to approve at or above the draft'
fi
if git -C "$origin" rev-parse --verify --quiet "$s10_request" > /dev/null; then
  fail 'review filed a request above the draft'
fi
ok as contributor valley withdraw "$h2"
printf 'n\nn\ns\n' | as requester valley review "$h3" > "$w/out" 2> "$w/err" || fail "review of the stack under $h3 failed"
grep -A1 "change 2 of 3: $h2 on s10/one\$" "$w/out" | grep -qxF "$h2 is withdrawn: there is nothing to approve." \
  || fail "the withdrawn layer's page did not say it is withdrawn under its heading"
grep -qF "$h3, 3 of 3: it would bring $h2, change 2 of 3, which is withdrawn, so it can't be approved yet. [v]iew full diff, [r]eject $h3, [s]top? " "$w/out" \
  || fail 'the top above the withdrawn layer did not say why it cannot be approved'

# ----------------------------------------------------------------------
# review sets the version it shows against the change's approved version:
# the greatest version below it whose head a request or an outcome names.
# main moves here by direct pushes, as a landing moves it.
#
# after <log> <line>: what <log> holds from its first line that is exactly
# <line> on.
after() {
  awk -v l="$2" '$0 == l { on = 1 } on' "$1"
}

# v1 is approved, another change lands, and review's [b]ase makes v2. The
# rebased version has v1's file delta, so review prints a clean restack
# of v1, its two bases, and the landing between them, and pages no diff.
git checkout --quiet -b cmp/topic main
commit cmp-one
as contributor git push --quiet origin cmp/topic
new_change cmp/topic
cid="$nid" ch="$nh"
cv1="$(git rev-parse HEAD)"
git checkout --quiet main
ok as requester valley approve --head "$cv1" "$ch"
grep -q "^Approved $ch v1 " "$w/out" || fail "approve did not approve $ch v1"
base1="$(at refs/heads/main)"
move_main cmp-landing
landing="$(at refs/heads/main)"
printf 'b\ns\n' | as requester valley review "$ch" > "$w/out" 2> "$w/err" || fail "review of $ch failed"
cv2="$(at refs/heads/cmp/topic)"
clean="$ch v2 at ${cv2:0:7}: clean restack of approved v1 at ${cv1:0:7}."
grep -qx "$clean" "$w/out" || fail "review after [b]ase did not say v2 is a clean restack of v1"
after "$w/out" "$clean" > "$w/clean"
[ "$(sed -n 2,4p "$w/clean")" = "$(printf '%s\n' "File delta unchanged. Base ${base1:0:7} -> ${landing:0:7}." \
  'Landings between bases:' "  ${landing:0:7} cmp-landing")" ] || fail "the clean restack did not list the landing: $(cat "$w/clean")"
grep -qx "cmp/topic: \[a\]pprove, \[v\]iew full diff, \[r\]eject, \[s\]kip? skipped cmp/topic; nothing done" "$w/clean" \
  || fail 'the prompt after a clean restack did not offer [v]iew full diff'
if grep -q '^diff --git' "$w/clean"; then fail 'review paged the diff of a clean restack'; fi
# [v]iew full diff pages it, and the prompt comes back.
printf 'v\ns\n' | as requester valley review "$ch" > "$w/out" 2> "$w/err" || fail "review of $ch failed"
after "$w/out" "$clean" > "$w/clean"
grep -qF "[s]kip? change $ch v2 at ${cv2:0:7}, on branch cmp/topic, log against origin/main:" "$w/clean" \
  || fail '[v]iew full diff did not page the change'
grep -qx 'diff --git a/cmp-one.txt b/cmp-one.txt' "$w/clean" || fail '[v]iew full diff did not show the diff'
[ "$(grep -c 'cmp/topic: \[a\]pprove, \[v\]iew full diff' "$w/clean")" = 2 ] || fail 'the prompt did not come back after [v]'

# v3 edits a file. The greatest approved version below it is still v1,
# so review pages that v3 changed since v1, the file whose change
# differs, and the range-diff.
git checkout --quiet -B cmp/topic "$cv2"
echo edited > cmp-one.txt
git commit --quiet -am 'cmp-one edited'
as contributor git push --quiet origin cmp/topic
ok as contributor valley ready --change "$cid"
cv3="$(git rev-parse HEAD)"
git checkout --quiet main
printf 's\n' | as requester valley review "$ch" > "$w/out" 2> "$w/err" || fail "review of $ch v3 failed"
changed="$ch v3 at ${cv3:0:7}: changed since approved v1 at ${cv1:0:7}."
grep -qx "$changed" "$w/out" || fail "review did not say v3 changed since v1: $(cat "$w/out")"
after "$w/out" "$changed" > "$w/changed"
grep -qx 'Review the range-diff before approving.' "$w/changed" || fail 'the changed version did not point at the range-diff'
grep -A2 -x '  cmp-one.txt' "$w/changed" | grep -q '^    v3: absent -> 100644 ' || fail 'the file whose change differs was not listed'
grep -qx "git range-diff ${base1:0:7}..${cv1:0:7} ${landing:0:7}..${cv3:0:7}:" "$w/changed" || fail 'no range-diff heading'
grep -qx "1:  ${cv1:0:7} = 1:  ${cv2:0:7} cmp-one" "$w/changed" || fail 'the range-diff did not pair the rebased commit'
grep -qx -- "-:  ------- > 2:  ${cv3:0:7} cmp-one edited" "$w/changed" || fail 'the range-diff did not show the new commit'
# The page ends with v3's own diff of that file, where a note keeps its
# file and line.
after "$w/changed" "v3's diff of the files whose change differs, from its base ${landing:0:7}. A note written beside a line of it keeps its file and line:" > "$w/own"
[ "$(sed -n 2p "$w/own")" = 'diff --git a/cmp-one.txt b/cmp-one.txt' ] || fail "the changed version did not end with its own diff: $(cat "$w/changed")"
grep -qx '+edited' "$w/own" || fail "v3's own diff did not show the edited line"

# v3 approved, v4 is v3 with another message, and v5 is v4 with another
# author, once v4 is approved. Each has its approved version's file delta
# on the same base, and each is flagged.
ok as requester valley approve --head "$cv3" "$ch"
git checkout --quiet cmp/topic
git commit --quiet --amend -m 'cmp-one edited, and reworded'
as contributor git push --quiet --force origin cmp/topic
ok as contributor valley ready --change "$cid"
cv4="$(git rev-parse HEAD)"
printf 's\n' | as requester valley review "$ch" > "$w/out" 2> "$w/err" || fail "review of $ch v4 failed"
after "$w/out" "$ch v4 at ${cv4:0:7}: clean restack of approved v3 at ${cv3:0:7}." > "$w/meta"
[ -s "$w/meta" ] || fail "a version with another message alone was no clean restack: $(cat "$w/out")"
grep -qx 'Commit messages changed since approved v3. Read them before approving:' "$w/meta" \
  || fail 'a changed message was not flagged'
grep -A1 -x "  v4, commit 2 of 2, ${cv4:0:7}:" "$w/meta" | grep -qx '    cmp-one edited, and reworded' \
  || fail 'the changed message was not shown'
if grep -q 'Commit authors changed' "$w/meta"; then fail 'an unchanged author was flagged'; fi
ok as requester valley approve --head "$cv4" "$ch"
git commit --quiet --amend --no-edit --author 'Someone Else <else@localhost>'
as contributor git push --quiet --force origin cmp/topic
ok as contributor valley ready --change "$cid"
cv5="$(git rev-parse HEAD)"
git checkout --quiet main
printf 's\n' | as requester valley review "$ch" > "$w/out" 2> "$w/err" || fail "review of $ch v5 failed"
after "$w/out" "$ch v5 at ${cv5:0:7}: clean restack of approved v4 at ${cv4:0:7}." > "$w/meta"
grep -qx 'Commit authors changed since approved v4:' "$w/meta" || fail "a changed author was not flagged: $(cat "$w/out")"
grep -q "^  v5, commit 2 of 2, ${cv5:0:7}: Someone Else <else@localhost> " "$w/meta" || fail 'the new author was not shown'
if grep -q 'Commit messages changed' "$w/meta"; then fail 'an unchanged message was flagged'; fi

# A change with no approved version is reviewed as any change is: its
# whole diff, with no comparison.
git checkout --quiet -b cmp/fresh main
commit cmp-fresh
as contributor git push --quiet origin cmp/fresh
new_change cmp/fresh
commit cmp-fresh-more
as contributor git push --quiet origin cmp/fresh
ok as contributor valley ready --change "$nid"
git checkout --quiet main
printf 's\n' | as requester valley review "$nh" > "$w/out" 2> "$w/err" || fail "review of $nh failed"
grep -q "^change $nh v2 at .*, on branch cmp/fresh, log against origin/main:$" "$w/out" || fail 'a change with no approval was not paged'
grep -qx 'diff --git a/cmp-fresh.txt b/cmp-fresh.txt' "$w/out" || fail 'a change with no approval did not show its diff'
if grep -q 'clean restack\|changed since approved' "$w/out"; then fail 'a change with no approval was compared'; fi

# A stack of two changes, of which only the lower has an approved
# version. Once rebased, review sets each layer against its own change's
# approval: the lower is a clean restack of its v1, and the top, which
# has none, is paged in full.
git checkout --quiet -b cmp/low main
commit cmp-low
as contributor git push --quiet origin cmp/low
new_change cmp/low
lid="$nid" lh="$nh" lv1="$(git rev-parse HEAD)"
git checkout --quiet -b cmp/high
commit cmp-high
as contributor git push --quiet origin cmp/high
new_change cmp/high
hh="$nh"
git checkout --quiet main
ok as requester valley approve --head "$lv1" "$lh"
move_main cmp-stack-landing
ok as requester valley rebase "$hh"
record "$lid" '.active == 2'
lv2="$(at refs/heads/cmp/low)" hv2="$(at refs/heads/cmp/high)"
printf 'n\ns\n' | as requester valley review "$hh" > "$w/out" 2> "$w/err" || fail "review of the stack under $hh failed"
# A prompt ends with no newline, so the next page's heading follows it on
# its line: each page runs from its heading to the line holding the next.
awk '/^change 1 of 2: /{ on = 1 } on { print } /change 2 of 2: /{ on = 0 }' "$w/out" > "$w/page1"
awk '/change 2 of 2: /{ on = 1 } on' "$w/out" > "$w/page2"
grep -qx "$lh v2 at ${lv2:0:7}: clean restack of approved v1 at ${lv1:0:7}." "$w/page1" \
  || fail "the lower layer was not set against its own approved version: $(cat "$w/out")"
grep -qF "$lh, 1 of 2: [n]ext layer, [a]pprove $lh alone, [v]iew full diff, [s]top? " "$w/page1" \
  || fail 'the lower layer did not offer [v]iew full diff'
if grep -q '^diff --git' "$w/page1"; then fail 'the clean lower layer was paged'; fi
grep -q "^change $hh v2 at ${hv2:0:7}, on branch cmp/high, log against $lh at " "$w/page2" || fail 'the top was not paged'
grep -qx 'diff --git a/cmp-high.txt b/cmp-high.txt' "$w/page2" || fail "the top's page did not hold its own diff"
if grep -q 'clean restack\|changed since approved' "$w/page2"; then fail 'the top, which has no approval, was compared'; fi

# ----------------------------------------------------------------------
# review names the knots each change it shows does: every knot in the
# project's plan graph whose changes lists valley:project/<change ID>, one
# line each, after the line that names the change and before its diff,
# active knots first. The graph is written through the hook. cmp/fresh is
# linked from one knot, cmp/low from two, cmp/high from one, and cmp/topic
# from none.
change_id() { valley status --json "$1" | jq -r .change_id; }
fid="$(change_id cmp/fresh)" hid="$(change_id "$hh")"
ok as contributor valley knot new outcome "Ready changes stay landable as main moves" --json
goal="$(jq -r .knot.id "$w/out")"
ok as contributor valley knot new bug "The stack reads the wrong base" --json
bug="$(jq -r .knot.id "$w/out")"
ok as contributor valley knot link "$goal" --change "valley:project/$fid" --change "valley:project/$lid" \
  --change "valley:project/$hid"
ok as contributor valley knot link "$bug" --change "valley:project/$lid"
goal_line="does $goal (open): Ready changes stay landable as main moves"
bug_line="does $bug (open): The stack reads the wrong base"
printf 's\n' | as requester valley review cmp/fresh > "$w/out" 2> "$w/err" || fail 'review of cmp/fresh failed'
grep -A1 "^change [a-z]* v2 at .*, on branch cmp/fresh, log against origin/main:$" "$w/out" | sed -n 2p \
  | grep -qxF "$goal_line" || fail "review of a change one knot links did not name it under the change: $(cat "$w/out")"
[ "$(grep -c '^does ' "$w/out")" = 1 ] || fail 'review of cmp/fresh named another knot than its own'
printf 'n\ns\n' | as requester valley review "$hh" > "$w/out" 2> "$w/err" || fail "review of the stack under $hh failed"
awk '/^change 1 of 2: /{ on = 1 } on { print } /change 2 of 2: /{ on = 0 }' "$w/out" > "$w/page1"
awk '/change 2 of 2: /{ on = 1 } on' "$w/out" > "$w/page2"
[ "$(sed -n 2,3p "$w/page1")" = "$(printf '%s\n' "$bug_line" "$goal_line")" ] \
  || fail "the lower layer's page did not name both its knots under its heading: $(cat "$w/page1")"
[ "$(grep -c '^does ' "$w/page1")" = 2 ] || fail "the lower layer's page named another layer's knots"
[ "$(sed -n 2p "$w/page2")" = "$goal_line" ] || fail "the top's page did not name its knot under its heading: $(cat "$w/page2")"
[ "$(grep -c '^does ' "$w/page2")" = 1 ] || fail "the top's page named another layer's knots"
printf 's\n' | as requester valley review "$ch" > "$w/out" 2> "$w/err" || fail "review of $ch failed"
if grep -q '^does ' "$w/out"; then fail 'review named a knot for a change no knot links'; fi
# A graph that does not read shows no knot, and review goes on as before.
# It says why only with -v.
graph_tip="$(at refs/the-valley/knots)"
junk="$(printf 'junk\n' | git -C "$origin" hash-object -w --stdin)"
junk_tree="$(printf '100644 blob %s\tjunk.txt\n' "$junk" | git -C "$origin" mktree)"
git -C "$origin" update-ref refs/the-valley/knots "$(git -C "$origin" commit-tree "$junk_tree" -m junk)"
printf 's\n' | as requester valley review cmp/fresh > "$w/out" 2> "$w/err" || fail 'review of cmp/fresh failed over a broken graph'
grep -q 'on branch cmp/fresh, log against origin/main:$' "$w/out" || fail 'review over a broken graph did not show the change'
grep -qF 'skipped cmp/fresh; nothing done' "$w/out" || fail 'review over a broken graph did not reach its prompt'
if grep -q '^does ' "$w/out"; then fail 'review named a knot from a graph that does not read'; fi
if grep -q 'graph' "$w/err"; then fail 'review spoke of a broken graph without -v'; fi
printf 's\n' | as requester valley review -v cmp/fresh > "$w/out" 2> "$w/err" || fail 'review -v of cmp/fresh failed'
grep -qF "project's plan graph could not be read, so no change shows a knot" "$w/err" \
  || fail 'review -v did not say why it shows no knot'
git -C "$origin" update-ref refs/the-valley/knots "$graph_tip"

echo 'valley-ready: change records through real pushes passed'
touch "$out"
