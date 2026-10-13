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

# Withdrawn, the change comes back only by a new version.
ok as contributor valley withdraw "$handle"
grep -qx "project $handle v2 withdrawn" "$w/out" || fail 'withdraw did not say what it withdrew'
record "$id" '.state == "withdrawn"' '.seq == 3' '.active == 2' '.versions | length == 2'
unchanged "$id" refused 'withdrawn already' as contributor valley withdraw "$handle"
unchanged "$id" refused 'a new version needs a new head' as contributor valley ready --change "$id"
commit three
third="$(git rev-parse HEAD)"
as contributor git push --quiet origin topic
ok as contributor valley ready --change "$id"
grep -qx "project $handle v3 ready" "$w/out" || fail 'the revived change was not v3'
record "$id" '.state == "ready"' '.seq == 4' '.active == 3' ".versions[2].head == \"git-sha1:$third\""

# ----------------------------------------------------------------------
# Who may write the record. A principal that neither owns the change nor
# holds the request grant is refused by the hook, whatever the command
# writes, and so is a key that names no principal.
commit four
fourth="$(git rev-parse HEAD)"
as contributor git push --quiet origin topic
unchanged "$id" refused "only the change's owner, contributor" as integrator valley ready --change "$id"
unchanged "$id" refused "only the change's owner, contributor" as integrator valley withdraw "$handle"
unchanged "$id" refused 'a key that names no principal' env -u VALLEY_PRINCIPAL valley withdraw "$handle"
refused 'git config valley.principal names none' as anonymous valley ready --change "$id"
# The operator, who holds the request grant, may add a version and
# withdraw, and the owner stays the owner.
ok as requester valley ready --head "$fourth" --change "$id"
grep -qx "project $handle v4 ready" "$w/out" || fail "the operator's version was not v4"
record "$id" '.owner == "contributor"' '.versions[3].uploader == "requester"' '.seq == 5'
ok as requester valley withdraw "$id"
record "$id" '.state == "withdrawn"' '.seq == 6' '.owner == "contributor"'

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
grep -q '^from-trailer: not approved' "$w/out" || fail 'status did not read the change by its handle'
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

echo 'valley-ready: change records through real pushes passed'
touch "$out"
