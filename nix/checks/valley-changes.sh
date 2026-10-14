# shellcheck shell=bash
# valley changes and valley pending, end to end and unmocked, over the
# repositories one test host serves. The host is made by the integrator
# host's rendered valley-init, relocated as init-e2e relocates it, so the
# project list discovery reads is the one a host publishes, and every
# record is pushed through the hook that host installs. What the
# integrator writes on disk when it lands a change — main, the outcome
# record, the transfer statement — is written here the same way, with a
# key generated for the run.
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
set -euo pipefail

w="$TMPDIR/valley-changes"
mkdir -p "$w"
export HOME="$w"
export XDG_CACHE_HOME="$w/cache"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name valley-changes-check
git config --global user.email valley-changes-check@localhost
git config --global init.defaultBranch main
git config --global advice.detachedHead false
unset VALLEY_KNOWN_SIGNERS VALLEY_INTEGRATOR_SIGNER

fail() {
  printf 'valley-changes: %s\n' "$*" >&2
  for f in "$w"/out "$w"/err; do
    if [ -s "$f" ]; then cat "$f" >&2; fi
  done
  exit 1
}

# ok <command…>: the command succeeds, and its output is kept.
ok() {
  "$@" > "$w/out" 2> "$w/err" || fail "$* failed"
}

# holds <jq condition…>: the last JSON printed holds each.
holds() {
  local cond
  for cond in "$@"; do
    jq -e "$cond" "$w/out" > /dev/null || fail "$cond does not hold"
  done
}

# line <ERE>: the last output has a line matching it.
line() {
  grep -qE -- "$1" "$w/out" || fail "no line matches $1"
}

no_line() {
  if grep -qE -- "$1" "$w/out"; then fail "a line matches $1"; fi
}

# after <ERE> <ERE…>: the first line of the last output that matches the
# first is followed, line by line, by lines matching the rest.
after() {
  local n re
  n="$(grep -nE -- "$1" "$w/out" | head -1 | cut -d: -f1)"
  [ -n "$n" ] || fail "no line matches $1"
  for re in "${@:2}"; do
    n=$((n + 1))
    sed -n "${n}p" "$w/out" | grep -qE -- "$re" || fail "line $n, after $1, does not match $re"
  done
}

# The host: guarded, open, released and sealed, with sealed the instance
# repository. valley-init creates them, wires their hooks, and publishes
# the project list in sealed.
data="$w/srv/git"
mkdir -p "$data"
sed "s|/srv/git|$data|g" "$initScriptPath" > "$w/init.sh"
bash "$w/init.sh" 2> "$w/init.err" || fail "valley-init failed: $(cat "$w/init.err")"
instance="$data/sealed.git"
# The integrator's repositories are group-shared, and git then sets the
# setgid bit on every directory it makes, which this sandbox refuses. The
# setting is dropped so the pushes below can write objects; it decides
# nothing this check reads.
for name in guarded open released sealed; do
  git -C "$data/$name.git" config --unset core.sharedRepository || true
done
[ "$(git -C "$instance" cat-file blob refs/the-valley/projects)" = "$(printf '%s\n' guarded open released sealed)" ] ||
  fail "valley-init published another project list"

# A change's ID, by the letter it repeats. Each is 32 letters from k to z.
id() { printf '%s%s' "$1" "$(printf 'k%.0s' $(seq $((32 - ${#1}))))"; }
A="$(id nxvpwlkm)" B="$(id mmmm)" C="$(id pppp)" D="$(id qqqq)" E="$(id rrrr)"
W="$(id ssss)" F="$(id tttt)" G="$(id uuuu)" U="$(id vvvv)" H="$(id wwww)"
X="$(id oooo)" I="$(id zzzz)" J="$(id lmlm)" X2="$(id pqpq)"
R="$(id nxvpwlkz)" D2="$(id xxxx)" E2="$(id yyyy)"

# A checkout of a project, whose main names sealed as its owning valley.
# Every push carries the principal, as the key sshd authenticated would.
export VALLEY_PRINCIPAL=klaus-env
checkout() {
  local name="$1" dir="$w/$1"
  git clone --quiet "$data/$name.git" "$dir" 2> /dev/null
  git -C "$dir" config valley.principal klaus-env
  mkdir -p "$dir/policy"
  printf '# the owning valley\n%s\n' "$instance" > "$dir/policy/valley"
  git -C "$dir" add policy/valley
  git -C "$dir" commit --quiet -m base
  VALLEY_PRINCIPAL=integrator git -C "$dir" push --quiet origin main
}

# branch <name> <from> <subject…>: a branch from a commit, with a commit
# per subject, each adding a file of its own, pushed to origin.
branch() {
  local name="$1" from="$2" s
  shift 2
  git checkout --quiet -b "$name" "$from"
  for s in "$@"; do
    echo "$s" > "$s.txt"
    git add "$s.txt"
    git commit --quiet -m "$s"
  done
  git push --quiet origin "$name"
}

# ready <id> <branch>: the branch's head readied as the change.
ready() {
  ok valley ready --change "$1" "$2"
}

# ----------------------------------------------------------------------
# open: a stack of three, a draft, a withdrawn change with a ready change
# on top of it, a change landed by fast-forward, one landed as a new
# commit with its transfer statement, one whose request was consumed with
# no evidence stored, one whose transfer statement another key signed,
# and a branch no record is on.
checkout open
cd "$w/open"
base="$(git rev-parse HEAD)"
branch stack-a "$base" stack-a
ready "$A" stack-a
branch stack-b stack-a stack-b
ready "$B" stack-b
branch stack-c stack-b stack-c
ready "$C" stack-c
branch paused "$base" paused
ready "$D" paused
ok valley unready "$D"
branch gone "$base" gone
ready "$E" gone
ok valley withdraw "$E"
branch atop-gone gone atop-gone
ready "$W" atop-gone
branch ff "$base" ff
ready "$F" ff
branch reparent "$base" reparent
ready "$G" reparent
branch lost "$base" lost
ready "$U" lost
branch forged "$base" forged
ready "$H" forged
branch loose "$base" loose

# Before anything lands, ff names its change.
ok valley-go resolve ff
line "^change"$'\t'"$F"$'\t'

# The integrator's keys: the integrator's own, and another a known
# signer holds, which may sign evidence and not a landing.
ssh-keygen -q -t ed25519 -N '' -C integrator -f "$w/integrator.key"
ssh-keygen -q -t ed25519 -N '' -C mallory -f "$w/mallory.key"
{
  attest key --key "$w/integrator.key" --name integrator
  attest key --key "$w/mallory.key" --name mallory
} > "$w/known-signers"

# F lands by fast-forward.
VALLEY_PRINCIPAL=integrator git push --quiet origin ff:main
fhead="$(git rev-parse ff)"

# G lands as a new commit on main, and the integrator records it: main
# moves, the outcome record says land, the request is consumed, and the
# transfer statement is stored under the landed tree.
git checkout --quiet -b landing ff
git cherry-pick reparent > /dev/null
landed="$(git rev-parse HEAD)"
VALLEY_PRINCIPAL=integrator git push --quiet origin landing:main
tree_digest() { attest digest --repo "$w/open" --rev "$1" | cut -d: -f2; }

# memo <name> <head> <decision…>: the integrator's outcome record.
memo() {
  local name="$1" head="$2" blob
  shift 2
  blob="$(printf '%s %s %s %s\n' "$head" "$fhead" 0123456789abcdef "$*" | git -C "$data/open.git" hash-object -w --stdin)"
  git -C "$data/open.git" update-ref "refs/the-valley/integration-outcomes/main/$name" "$blob"
}

# transfer <change> <head> <landed> <key> <name>: a transfer statement,
# as the integrator composes, signs and stores one.
transfer() {
  local change="$1" head="$2" at="$3" key="$4" name="$5" ldigest blob tree
  ldigest="$(tree_digest "$at")"
  jq -n --arg change "$change" --arg base "$(tree_digest "$head")" --arg landed "$ldigest" \
    --arg commit "$at" --arg tip "$fhead" '{
      statementType: "the-valley/attestation/v1",
      subject: {primary: "valley-tree-v1", digest: {"valley-tree-v1": $landed, "git-sha1": $commit}},
      predicateType: "the-valley/integration/transfer/v1",
      predicate: {change: $change, target: "refs/heads/main", base: {"valley-tree-v1": $base},
        policy: {sha256: ("ab" * 32), "git-sha1": $tip}, required: "none"}
    }' > "$w/statement.json"
  attest render "$w/statement.json" | attest sign --key "$key" --name "$name" > "$w/transfer.note"
  blob="$(git -C "$data/open.git" hash-object -w "$w/transfer.note")"
  tree="$(printf '100644 blob %s\ttransfer.note\n' "$blob" | git -C "$data/open.git" mktree)"
  git -C "$data/open.git" update-ref "refs/the-valley/attestations/$ldigest/$(printf '%08x' "${#name}")" "$tree"
}
memo reparent "$(git rev-parse reparent)" land
transfer reparent "$(git rev-parse reparent)" "$landed" "$w/integrator.key" integrator

# U's request was consumed, and no evidence was stored.
memo lost "$(git rev-parse lost)" land
# H's request was consumed too, and its transfer statement is signed by a
# known key that is not the integrator's: it is no record of a landing.
memo forged "$(git rev-parse forged)" land
git checkout --quiet -b landing-h landing
git cherry-pick forged > /dev/null
VALLEY_PRINCIPAL=integrator git push --quiet origin landing-h:main
transfer forged "$(git rev-parse forged)" "$(git rev-parse HEAD)" "$w/mallory.key" mallory
# A's request is pending.
git -C "$data/open.git" update-ref refs/the-valley/integration-requests/main/stack-a "$(git rev-parse stack-a)"

# I lands as a new commit, L2, filed as c-<I>, and the integrator records
# it. Then I's next version, H', has H's tree with L2 as its parent. Its
# name and its tree digest match the transfer statement, but it is another
# delta: merged onto L2's parent it does not give L2's tree. I is then made
# a draft.
branch again "$base" again
ready "$I" again
hhead="$(git rev-parse again)"
git checkout --quiet -b landing-i landing-h
git cherry-pick again > /dev/null
landed2="$(git rev-parse HEAD)"
VALLEY_PRINCIPAL=integrator git push --quiet origin landing-i:main
memo "c-$I" "$hhead" land
transfer "c-$I" "$hhead" "$landed2" "$w/integrator.key" integrator
hprime="$(git commit-tree "again^{tree}" -p "$landed2" -m again)"
git push --quiet --force origin "$hprime:refs/heads/again"
ready "$I" again
ok valley unready "$I"

# J lands by fast-forward, and the integrator records it: its outcome
# record says land at J's head, and its transfer statement is about J's
# tree. Then main moves on to Q, which adds a file, and J's next version,
# J', has J's tree with Q as its parent: it deletes Q's file. J' matches
# the statement's name and tree, and merged onto the parent of the commit
# that landed it gives that commit's tree. But the outcome record names
# J's head, not J', so J' has not landed. J is then made a draft.
branch ffrec landing-i ffrec
ready "$J" ffrec
jhead="$(git rev-parse ffrec)"
VALLEY_PRINCIPAL=integrator git push --quiet origin ffrec:main
memo ffrec "$jhead" land
transfer ffrec "$jhead" "$jhead" "$w/integrator.key" integrator
git checkout --quiet -b moved-on ffrec
echo q > q.txt
git add q.txt
git commit --quiet -m q
VALLEY_PRINCIPAL=integrator git push --quiet origin moved-on:main
jprime="$(git commit-tree "ffrec^{tree}" -p moved-on -m ffrec)"
git push --quiet --force origin "$jprime:refs/heads/ffrec"
ready "$J" ffrec
ok valley unready "$J"

# ----------------------------------------------------------------------
# guarded: a ready change whose ID shares seven letters with A's, a draft
# and a withdrawn change.
checkout guarded
cd "$w/guarded"
gbase="$(git rev-parse HEAD)"
branch g-ready "$gbase" g-ready
ready "$R" g-ready
branch g-draft "$gbase" g-draft
ready "$D2" g-draft
ok valley unready "$D2"
branch g-gone "$gbase" g-gone
ready "$E2" g-gone
ok valley withdraw "$E2"
# B's change ID, recorded here too.
branch g-dup "$gbase" g-dup
ready "$B" g-dup

# A merge on main can drop the content of either parent, so no head below
# one has landed by ancestry. X is ready, and a merge of it whose tree
# drops its changes lands on main by fast-forward: M = (main, X), with
# main's tree. Then X2, branched from that main, is dropped by a merge with
# its parents reversed: M2 = (X2, main), with main's tree. X2 is on main's
# first-parent history, below the merge that dropped it.
branch dropped "$gbase" dropped
ready "$X" dropped
merge="$(git commit-tree "$gbase^{tree}" -p "$gbase" -p dropped -m 'merge, dropping X')"
VALLEY_PRINCIPAL=integrator git push --quiet origin "$merge:refs/heads/main"
branch reversed "$merge" reversed
ready "$X2" reversed
merge2="$(git commit-tree "$merge^{tree}" -p reversed -p "$merge" -m 'merge, dropping X2')"
VALLEY_PRINCIPAL=integrator git push --quiet origin "$merge2:refs/heads/main"

# origin: a project named origin, recording B's change ID too. A host that
# declared it would list it, so it is added to the list on disk, as
# valley-init writes the list.
git init --quiet --bare "$data/origin.git"
checkout origin
cd "$w/origin"
branch o-dup "$(git rev-parse HEAD)" o-dup
ready "$B" o-dup
projects="$(printf '%s\n' guarded open origin released sealed | git -C "$instance" hash-object -w --stdin)"
git -C "$instance" update-ref refs/the-valley/projects "$projects"

# released is listed and cannot be read.
mv "$data/released.git" "$data/released.moved"

export VALLEY_KNOWN_SIGNERS="$w/known-signers" VALLEY_INTEGRATOR_SIGNER=integrator

# ----------------------------------------------------------------------
# One repository: every record in every state, as valley-changes/v1.
cd "$w/open"
ok valley changes --json
[ "$(wc -l < "$w/out")" = 1 ] || fail "changes printed more than one line"
holds '.schema == "valley-changes/v1"' '.host == "localhost"' '(.repos | length) == 1' \
  '.repos[0].repo == "open"' ".repos[0].url == \"$data/open.git\"" '.repos[0].error == null' \
  '(.repos[0].changes | length) == 12' '.repos[0] | has("drafts") | not'
c() { printf '.repos[0].changes[] | select(.change_id == "%s")' "$1"; }
holds "$(c "$A") | .handle == \"nxvp\" and .state == \"ready\" and .owner == \"klaus-env\" and .target == \"main\" and .active == 1 and .seq == 1" \
  "$(c "$A") | .record == \"$(git -C "$data/open.git" rev-parse "refs/the-valley/changes/main/$A")\"" \
  "$(c "$A") | .versions[0] | .n == 1 and .title == \"stack-a\" and .branch == \"stack-a\" and .uploader == \"klaus-env\"" \
  "$(c "$A") | .versions[0] | .head == \"$(git rev-parse stack-a)\" and .landing == \"not-landed\" and .landed_commit == null" \
  "$(c "$A") | .versions[0].request == {ref: \"refs/the-valley/integration-requests/main/stack-a\", commit: \"$(git rev-parse stack-a)\"}" \
  "$(c "$A") | .versions[0].outcome == null" \
  "$(c "$D") | .state == \"draft\" and .seq == 2" \
  "$(c "$E") | .state == \"withdrawn\""
# Landing evidence, one version each way.
holds "$(c "$F") | .versions[0] | .landing == \"landed\" and .landed_commit == \"$fhead\"" \
  "$(c "$G") | .versions[0] | .landing == \"landed-recorded\" and .landed_commit == \"$landed\"" \
  "$(c "$G") | .versions[0].outcome == {decision: \"land\", head: \"$(git rev-parse reparent)\", tip: \"$fhead\"}" \
  "$(c "$G") | .versions[0].request == null" \
  "$(c "$U") | .versions[0] | .landing == \"uncertain\" and .landed_commit == null" \
  "$(c "$H") | .versions[0].landing == \"uncertain\"" \
  "$(c "$J") | .versions[0] | .landing == \"landed\" and .landed_commit == \"$jhead\"" \
  "$(c "$J") | .versions[1] | .head == \"$jprime\" and .landing == \"not-landed\" and .landed_commit == null" \
  "$(c "$J") | .state == \"draft\" and .active == 2" \
  "$(c "$I") | .versions[0] | .landing == \"landed-recorded\" and .landed_commit == \"$landed2\"" \
  "$(c "$I") | .versions[1] | .head == \"$hprime\" and .landing == \"not-landed\" and .landed_commit == null" \
  "$(c "$I") | .state == \"draft\" and .active == 2"
# includes is ancestry, nearest only.
holds "$(c "$A") | .includes == []" "$(c "$B") | .includes == [\"$A\"]" \
  "$(c "$C") | .includes == [\"$B\"]" "$(c "$W") | .includes == [\"$E\"]"
# The one branch no record is on, and no branch main reaches.
holds '.repos[0].branches == [{branch: "loose", head: "'"$(git rev-parse loose)"'"}]'

# Without the registry's keys, the transfer statement is not believed, and
# that is said.
env -u VALLEY_KNOWN_SIGNERS valley changes --json > "$w/out" 2> "$w/err" || fail "changes failed without the keys"
holds "$(c "$G") | .versions[0].landing == \"uncertain\""
grep -q 'could not be verified' "$w/err" || fail "an unverified transfer statement was not reported"

# ----------------------------------------------------------------------
# Every project the host publishes. Handles are unique across both
# repositories, the repository that cannot be read carries its error, and
# the others are listed all the same.
ok valley changes --json --all
holds '[.repos[].repo] == ["guarded", "open", "origin", "released", "sealed"]' \
  '.repos[] | select(.repo == "released") | .error != null and .changes == [] and .url == "'"$data/released.git"'"' \
  '.repos[] | select(.repo != "released") | .error == null' \
  '.repos[] | select(.repo == "sealed") | .changes == []' \
  '[.repos[].changes[].handle] | length == (unique | length)' \
  "[.repos[].changes[] | select(.change_id == \"$A\" or .change_id == \"$R\") | .handle] == [\"nxvpwlkz\", \"nxvpwlkm\"]" \
  "[.repos[].changes[] | select(.change_id == \"$B\") | .handle] == [\"guarded:mmmm\", \"open:mmmm\", \"origin:mmmm\"]" \
  '.repos[] | select(.repo == "guarded") | [.changes[].state] == ["ready", "ready", "ready", "ready", "draft", "withdrawn"]'
# ls-remote matches a ref's tail, so refs whose names end like the
# refs discovery reads must not stand in for them: one in open whose
# policy/valley names another instance, and one in the instance listing
# another project.
git checkout --quiet -b decoy "$base"
printf '%s\n' "$w/elsewhere.git" > policy/valley
git commit --quiet -am decoy
git push --quiet origin decoy:refs/heads/a/refs/heads/main
decoy_list="$(printf 'decoy\n' | git -C "$instance" hash-object -w --stdin)"
git -C "$instance" update-ref refs/tags/a/refs/the-valley/projects "$decoy_list"
git checkout --quiet landing
ok valley changes --json --all
holds '[.repos[].repo] == ["guarded", "open", "origin", "released", "sealed"]'
git push --quiet origin :refs/heads/a/refs/heads/main
git -C "$instance" update-ref -d refs/tags/a/refs/the-valley/projects
# --repo limits the listing to one repository, read through its clone.
ok valley changes --json --repo guarded
holds '[.repos[].repo] == ["guarded"]' "[.repos[0].changes[].change_id] == [\"$B\", \"$R\", \"$X\", \"$X2\", \"$D2\", \"$E2\"]" \
  '[.repos[0].changes[].handle] == ["mmmm", "nxvp", "oooo", "pqpq", "xxxx", "yyyy"]' \
  "[.repos[0].changes[] | select(.change_id == \"$X\" or .change_id == \"$X2\") | .versions[0] | [.landing, .landed_commit]] == [[\"not-landed\", null], [\"not-landed\", null]]"
cd "$w/guarded"
ok valley changes --json
holds '[.repos[].repo] == ["guarded"]' '[.repos[0].changes[].handle] == ["mmmm", "nxvp", "oooo", "pqpq", "xxxx", "yyyy"]'
# A qualified handle names the change in the repository it qualifies, and
# a qualifier naming another repository is refused.
ok valley-go resolve guarded:mmmm
line "^change"$'\t'"$B"$'\t'"mmmm"$'\t'"1"$'\t'"$(git rev-parse g-dup)"$'\t'
cd "$w/open"
ok valley-go resolve open:mmmm
line "^change"$'\t'"$B"$'\t'"mmmm"$'\t'"1"$'\t'"$(git rev-parse stack-b)"$'\t'
if valley-go resolve origin:mmmm > "$w/out" 2> "$w/err"; then fail "open resolved a handle qualified by origin"; fi
grep -q 'origin:mmmm names a change in origin, and this repository is open' "$w/err" || fail "origin:mmmm was refused without saying why"
cd "$w/origin"
ok valley-go resolve origin:mmmm
line "^change"$'\t'"$B"$'\t'"mmmm"$'\t'"1"$'\t'"$(git rev-parse o-dup)"$'\t'
ok valley status origin:mmmm
cd "$w/guarded"
if valley changes --json --repo nowhere > "$w/out" 2> "$w/err"; then fail "--repo took a project the host does not list"; fi
grep -q 'lists no project nowhere' "$w/err" || fail "--repo nowhere was refused without saying why"
cd "$w/open"

# A repository whose main names no owning valley lists itself alone, and
# says discovery is unavailable.
git init --quiet --bare "$w/solo.git"
git clone --quiet "$w/solo.git" "$w/solo" 2> /dev/null
git -C "$w/solo" commit --quiet --allow-empty -m solo
git -C "$w/solo" push --quiet origin main
(cd "$w/solo" && valley changes --json --all) > "$w/out" 2> "$w/err" || fail "changes --all failed without discovery"
holds '[.repos[].repo] == ["solo"]'
grep -q 'discovery is unavailable' "$w/err" || fail "changes --all did not say discovery is unavailable"

# A policy/valley that git would read as an option, or that names a remote
# helper, is refused before git sees it, and nothing it names runs, even
# where the caller's own configuration allows ext::.
for url in "--upload-pack=touch $w/pwned; git-upload-pack" "ext::sh -c touch% $w/pwned"; do
  rm -rf "$w/evil.git" "$w/evil"
  git init --quiet --bare "$w/evil.git"
  git clone --quiet "$w/evil.git" "$w/evil" 2> /dev/null
  mkdir -p "$w/evil/policy"
  printf '%s\n' "$url" > "$w/evil/policy/valley"
  git -C "$w/evil" add policy
  git -C "$w/evil" commit --quiet -m evil
  git -C "$w/evil" push --quiet origin main
  export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=protocol.ext.allow GIT_CONFIG_VALUE_0=always
  (cd "$w/evil" && valley changes --json --all) > "$w/out" 2> "$w/err" || fail "changes --all failed over policy/valley $url"
  holds '[.repos[].repo] == ["evil"]'
  grep -q 'discovery is unavailable' "$w/err" || fail "policy/valley $url was not refused"
  if (cd "$w/evil" && valley changes --json --repo other) > "$w/out" 2> "$w/err"; then
    fail "--repo read through policy/valley $url"
  fi
  unset GIT_CONFIG_COUNT GIT_CONFIG_KEY_0 GIT_CONFIG_VALUE_0
  [ ! -e "$w/pwned" ] || fail "policy/valley $url ran a command"
done

# ----------------------------------------------------------------------
# pending: a block per entry, in three sections. Each entry is nested
# under the entry that contains it, and counted from the entry it
# contains. atop-lost is a branch no record is on, holding U, a ready
# change. a-parked is a branch last committed to eleven days ago.
branch atop-lost lost atop-lost
parked="@$(($(date +%s) - 11 * 86400))"
GIT_AUTHOR_DATE="$parked" GIT_COMMITTER_DATE="$parked" branch a-parked "$base" a-parked
ok valley pending
no_line $'\x1b'
line '^ready$'
# The stack of three, outermost first, each layer counted on its own.
after '^pppp v1  +[0-9]+ [a-z]+ ago · valley-changes-check$' '^  stack-c$' '^  1 commit · 1 file \+1 −0$' \
  '^  └ mmmm v1  +.* ago · valley-changes-check$' '^      stack-b$' '^      1 commit · 1 file \+1 −0$' \
  '^      └ nxvp v1  +.* ago · valley-changes-check$' '^          stack-a$' '^          1 commit · 1 file \+1 −0$' '^$'
# W includes E, which is withdrawn, so it is counted from main.
after '^ssss v1  ' '^  atop-gone$' '^  2 commits · 2 files \+2 −0$' '^  includes rrrr \(withdrawn\)$' '^$'
after '^wwww v1  ' '^  forged$' '^  1 commit · 1 file \+1 −0$' '^  uncertain: it may have landed without its evidence being stored$'
no_line '(tttt|uuuu|rrrr) v1'
no_line 'qqqq v1  '
# The branches, latest first, and atop-lost with U nested in it, marked
# as ready.
after '^branches$' '^atop-lost  +.* ago · valley-changes-check$' '^  atop-lost$' '^  1 commit · 1 file \+1 −0$' \
  '^  └ vvvv v1 \(ready\)  +.* ago · valley-changes-check$' '^      lost$' '^      1 commit · 1 file \+1 −0$' \
  '^      uncertain: ' '^$' '^loose  +.* ago · valley-changes-check$' '^  loose$' '^  1 commit · 1 file \+1 −0$' '^$' \
  '^a-parked  +11 days ago · valley-changes-check$' '^  a-parked$' '^  1 commit · 1 file \+1 −0$' '^$' '^drafts$'
after '^qqqq v1 on paused  +.* ago · valley-changes-check$' '^  paused$' '^  1 commit · 1 file \+1 −0$'
after '^zzzz v2 on again  ' '^  again$' '^  1 commit · '
after '^lmlm v2 on ffrec  ' '^  ffrec$' '^  1 commit · 1 file \+0 −1$'
[ "$(grep -n '^ready$' "$w/out" | cut -d: -f1)" -lt "$(grep -n '^branches$' "$w/out" | cut -d: -f1)" ] ||
  fail "pending listed the branches before the ready changes"
# --json is the same listing in valley-changes/v1's shape: the pending
# changes alone in changes, and the drafts in drafts.
ok valley pending --json
holds '.schema == "valley-pending/v1"' \
  '[.repos[0].changes[].handle] == ["mmmm", "nxvp", "pppp", "ssss", "vvvv", "wwww"]' \
  '[.repos[0].changes[].state] | unique == ["ready"]' \
  '[.repos[0].drafts[] | [.handle, .state]] == [["lmlm", "draft"], ["qqqq", "draft"], ["zzzz", "draft"]]' \
  '[.repos[0].branches[].branch] == ["a-parked", "atop-lost", "loose"]'
# Across the host, each repository is headed by its name, and handles
# are qualified where a change ID is recorded in more than one.
ok valley pending --all
after '^guarded$' '^(─)+$' '^ready$'
after '^nxvpwlkz v1  ' '^  g-ready$' '^  1 commit · 1 file \+1 −0$'
after '^guarded:mmmm v1  ' '^  g-dup$'
after '^pqpq v1  ' '^  reversed$' '^  no commits · ' '^  └ oooo v1  ' '^      dropped$' '^      no commits · no file changes$'
line '^xxxx v1 on g-draft  '
line '^open$'
after '^pppp v1  ' '^  stack-c$' '^  1 commit · ' '^  └ open:mmmm v1  ' '^      stack-b$' '^      1 commit · ' \
  '^      └ nxvpwlkm v1  ' '^          stack-a$'
line '^origin$'
line '^origin:mmmm v1  '
line '^released: could not read '
after '^sealed$' '^(─)+$' '^nothing is ready$'

# --owes adds a line to each change with the checks it owes, by cost, as
# valley checks derives them, here and in a repository read through its
# clone. The instance gets a floor for that, written to its main on disk
# as the integrator would.
git init --quiet "$w/floor"
mkdir -p "$w/floor/policy/instance"
cp "$floor"/*.cue "$w/floor/policy/instance/"
git -C "$w/floor" add policy
git -C "$w/floor" commit --quiet -m floor
git -C "$w/floor" push --quiet "$instance" HEAD:refs/heads/floor
git -C "$instance" update-ref refs/heads/main "$(git -C "$w/floor" rev-parse HEAD)"
ok valley pending --owes --all
after '^pppp v1  ' '^  stack-c$' '^  1 commit · ' '^  owes 1 quick, 0 slow$' '^  └ open:mmmm v1  ' '^      stack-b$' \
  '^      1 commit · ' '^      owes 1 quick, 0 slow$' '^      └ nxvpwlkm v1  ' '^          stack-a$' '^          1 commit · ' \
  '^          owes 1 quick, 0 slow$'
after '^nxvpwlkz v1  ' '^  g-ready$' '^  1 commit · ' '^  owes 1 quick, 0 slow$'

# ----------------------------------------------------------------------
# A branch name is used again once its change has landed: ff now names no
# change, and reparent none either, once its transfer statement verifies.
ok valley-go resolve ff
line "^branch"$'\t'"ff$"
ok valley-go resolve reparent
line "^branch"$'\t'"reparent$"
env -u VALLEY_KNOWN_SIGNERS valley-go resolve reparent > "$w/out" 2> "$w/err" || fail "resolve failed"
line "^change"$'\t'"$G"$'\t'
ok valley-go resolve stack-a
line "^change"$'\t'"$A"$'\t'

# H' has not landed, so its branch still names I, which is a draft, and
# approving the branch is refused for I's state rather than taken as a
# plain branch.
ok valley-go resolve again
line "^change"$'\t'"$I"$'\t'"zzzz"$'\t'"2"$'\t'"$hprime"$'\t'"again"$'\t'"draft$"
if valley approve again > "$w/out" 2> "$w/err"; then fail "approve took the branch of a draft"; fi
grep -q "change zzzz v2 is draft, not ready" "$w/err" || fail "approve did not refuse the draft for its state"

# J' is not J's landing replayed, so its branch names J, a draft, too.
ok valley-go resolve ffrec
line "^change"$'\t'"$J"$'\t'"lmlm"$'\t'"2"$'\t'"$jprime"$'\t'"ffrec"$'\t'"draft$"
if valley approve ffrec > "$w/out" 2> "$w/err"; then fail "approve took the branch of a replayed landing"; fi
grep -q "change lmlm v2 is draft, not ready" "$w/err" || fail "approve did not refuse J' for its state"

# A landed change names its branch no more once the branch moves on to new
# work: ff is a branch, free for that work.
git checkout --quiet -b ff-again ff
echo more > more.txt
git add more.txt
git commit --quiet -m more
git push --quiet origin ff-again:ff
ok valley-go resolve ff
line "^branch"$'\t'"ff$"

# valley help names the verbs klaus looks for.
valley help > "$w/out"
line '^  ready '
line '^  changes '

touch "$out"
