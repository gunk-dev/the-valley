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
  n="$(grep -nE -m 1 -- "$1" "$w/out" | cut -d: -f1 || true)"
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
K="$(id kkkk)" X="$(id oooo)" I="$(id zzzz)" J="$(id lmlm)" X2="$(id pqpq)"
R="$(id nxvpwlkz)" D2="$(id xxxx)" E2="$(id yyyy)"

# A checkout of a project, whose main names sealed as its owning valley.
# Every push carries the principal, as the key sshd authenticated would.
export VALLEY_PRINCIPAL=alice
checkout() {
  local name="$1" dir="$w/$1"
  git clone --quiet "$data/$name.git" "$dir" 2> /dev/null
  git -C "$dir" config valley.principal alice
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
ok valley resolve ff
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

# transfer <change> <head> <landed> <key> <name> [single]: a transfer
# statement, as the integrator composes, signs and stores one under the
# landed tree and its signer's key hash, at
# transfer.d/<2 hex>/<2 hex>/<rest>.note, split from the landed commit's
# ID. With single, it is stored as the one transfer.note at the top of the
# tree, the layout of a ref the integrator wrote before it kept one
# statement per landed commit, which the reader still reads.
transfer() {
  local change="$1" head="$2" at="$3" key="$4" name="$5" layout="${6:-}" ldigest blob tree
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
  if [ "$layout" = single ]; then
    tree="$(printf '100644 blob %s\ttransfer.note\n' "$blob" | git -C "$data/open.git" mktree)"
  else
    tree="$(printf '100644 blob %s\t%s.note\n' "$blob" "${at:4}" | git -C "$data/open.git" mktree)"
    tree="$(printf '040000 tree %s\t%s\n' "$tree" "${at:2:2}" | git -C "$data/open.git" mktree)"
    tree="$(printf '040000 tree %s\t%s\n' "$tree" "${at:0:2}" | git -C "$data/open.git" mktree)"
    tree="$(printf '040000 tree %s\ttransfer.d\n' "$tree" | git -C "$data/open.git" mktree)"
  fi
  git -C "$data/open.git" update-ref "refs/the-valley/attestations/$ldigest/$(attest key --key "$key" --name "$name" | cut -d+ -f2)" "$tree"
}
memo reparent "$(git rev-parse reparent)" land
transfer reparent "$(git rev-parse reparent)" "$landed" "$w/integrator.key" integrator single

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

# K is built on main after F, whose active version landed by ancestry
# while its record is still ready. K's branch starts at main's tip, so F's
# head is below it, and F has landed: K includes nothing.
branch after-landing moved-on after-landing
ready "$K" after-landing
# K's request is pending, so pending lists K as approved, apart from the
# ready changes that wait on an approval.
git -C "$data/open.git" update-ref refs/the-valley/integration-requests/main/after-landing "$(git rev-parse after-landing)"

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
  '(.repos[0].changes | length) == 13' '.repos[0] | has("drafts") | not'
c() { printf '.repos[0].changes[] | select(.change_id == "%s")' "$1"; }
holds "$(c "$A") | .handle == \"nxvp\" and .state == \"ready\" and .owner == \"alice\" and .target == \"main\" and .active == 1 and .seq == 1" \
  "$(c "$A") | .record == \"$(git -C "$data/open.git" rev-parse "refs/the-valley/changes/main/$A")\"" \
  "$(c "$A") | .versions[0] | .n == 1 and .title == \"stack-a\" and .branch == \"stack-a\" and .uploader == \"alice\"" \
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
# Each change says which version it landed as, and as which commit: J and
# I's next rounds, made on the main that holds their landings, have not.
holds "$(c "$F") | .landed_version == 1 and .landed_commit == \"$fhead\"" \
  "$(c "$G") | .landed_version == 1 and .landed_commit == \"$landed\"" \
  "$(c "$U") | .landed_version == null and .landed_commit == null" \
  "$(c "$J") | .landed_version == null and .landed_commit == null" \
  "$(c "$I") | .landed_version == null and .landed_commit == null"
# includes is ancestry, nearest only.
holds "$(c "$A") | .includes == []" "$(c "$B") | .includes == [\"$A\"]" \
  "$(c "$C") | .includes == [\"$B\"]" "$(c "$W") | .includes == [\"$E\"]" \
  "$(c "$F") | .state == \"ready\" and .versions[0].landing == \"landed\"" "$(c "$K") | .includes == []"
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
ok valley resolve guarded:mmmm
line "^change"$'\t'"$B"$'\t'"mmmm"$'\t'"1"$'\t'"$(git rev-parse g-dup)"$'\t'
cd "$w/open"
ok valley resolve open:mmmm
line "^change"$'\t'"$B"$'\t'"mmmm"$'\t'"1"$'\t'"$(git rev-parse stack-b)"$'\t'
if valley resolve origin:mmmm > "$w/out" 2> "$w/err"; then fail "open resolved a handle qualified by origin"; fi
grep -q 'origin:mmmm names a change in origin, and this repository is open' "$w/err" || fail "origin:mmmm was refused without saying why"
cd "$w/origin"
ok valley resolve origin:mmmm
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
# pending: the ready changes that need the operator, one row each in
# landing order, then those moving without them, then a count of the
# drafts and the branches no change is on. The changes that wait on an
# approval are stacked among themselves, a stack folded into its top's
# row. atop-lost is a branch no record is on, holding U, a ready change.
# a-parked is a branch last committed to eleven days ago. deep-4 tops a
# chain of four branches. fork-left and fork-right are two branches on one
# base, fork-base. The chain and the fork were last committed to twenty
# days ago. W is readied again as v2, dated an hour on, so it became ready
# last: its row is the last, though its change ID sorts before U's and
# H's, and its v1 is listed nowhere.
branch atop-lost lost atop-lost
parked="@$(($(date +%s) - 11 * 86400))"
GIT_AUTHOR_DATE="$parked" GIT_COMMITTER_DATE="$parked" branch a-parked "$base" a-parked
old="@$(($(date +%s) - 20 * 86400))"
export GIT_AUTHOR_DATE="$old" GIT_COMMITTER_DATE="$old"
branch deep-1 "$base" deep-1
branch deep-2 deep-1 deep-2
branch deep-3 deep-2 deep-3
branch deep-4 deep-3 deep-4
branch fork-base "$base" fork-base
branch fork-left fork-base fork-left
branch fork-right fork-base fork-right
unset GIT_AUTHOR_DATE GIT_COMMITTER_DATE
cd "$w/open"
git checkout --quiet atop-gone
echo atop-gone-v2 > atop-gone-v2.txt
git add atop-gone-v2.txt
git commit --quiet -m atop-gone-v2
git push --quiet origin atop-gone
GIT_COMMITTER_DATE="@$(($(date +%s) + 3600))" ok valley ready --change "$W" atop-gone
ok valley pending
no_line $'\x1b'
# pending also writes the handle cache completion reads: one line for each
# change not landed or withdrawn, ready changes first.
cache="$(git rev-parse --git-common-dir)/valley/handles"
[ -f "$cache" ] || fail "pending wrote no handle cache at $cache"
cut -f1,2 "$cache" > "$w/out"
[ "$(cat "$w/out")" = "$(printf '%s\t%s\n' kkkk ready mmmm ready nxvp ready pppp ready ssss ready vvvv ready wwww ready lmlm draft qqqq draft zzzz draft)" ] ||
  fail "the handle cache lists other changes than the ready and the drafts"
grep -qE "^pppp"$'\t'"ready"$'\t'"[^"$'\t'"]*"$'\t'"stack-c$" "$cache" || fail "the handle cache has no line for pppp with its title"
grep -qE "^qqqq"$'\t'"draft"$'\t'"paused"$'\t' "$cache" || fail "the handle cache has no line for qqqq with its branch"
ok valley pending
# pppp folds mmmm, which it is on, into its row. Its approved layer nxvp
# moves on its own, as K does. The rows are in the order their active
# versions became ready.
after '^needs you \(4, in landing order\)$' \
  '^  1  pppp v1  stack of 2 +[0-9]+[smhd]$' '^     stack-c$' \
  '^  2  vvvv v1  +[0-9]+[smhd]$' '^     lost$' \
  '^  3  wwww v1  +[0-9]+[smhd]$' '^     forged$' \
  '^  4  ssss v2  +0s$' '^     atop-gone-v2$' '^$' '^moving \(2\)$'
line '^  ◐ kkkk v1  approved$'
line '^  ◐ nxvp v1  approved$'
no_line 'mmmm v1|ssss v1|(tttt|uuuu|rrrr) v1|qqqq v1|lmlm v|zzzz v'
no_line ' ago · '
[ "$(tail -1 "$w/out")" = '3 drafts · 10 branches no change is on · valley pending --drafts --branches' ] ||
  fail "pending did not end with the counts of the drafts and the branches"
# -v follows each row with its block: the stack in full, each layer
# counted from the one it is on, and what the change includes.
ok valley pending -v
after '^  1  pppp v1  stack of 2 ' '^     stack-c$' '^     1 commit · 1 file \+1 −0$' \
  '^     approving it lands the one below too$' '^     └ mmmm v1  +.* ago · valley-changes-check$' \
  '^         stack-b$' '^         2 commits · 2 files \+2 −0$' '^  2  vvvv v1  '
after '^  3  wwww v1  ' '^     forged$' '^     1 commit · 1 file \+1 −0$' \
  '^     uncertain: it may have landed without its evidence being stored$' '^  4  ssss v2  '
after '^  4  ssss v2  ' '^     atop-gone-v2$' '^     3 commits · 3 files \+3 −0$' '^     includes rrrr \(withdrawn\)$' '^$'
after '^  ◐ kkkk v1  approved$' '^    after-landing$' '^    1 commit · 1 file \+1 −0$'
# --drafts and --branches list them in full, as blocks, in place of their
# counts. Each listing stacks its own entries.
ok valley pending --drafts --branches
no_line ' no change is on'
after '^drafts$' '^lmlm v2 on ffrec  +.* ago · valley-changes-check$' '^  ffrec$' '^  1 commit · 1 file \+0 −1$'
after '^qqqq v1 on paused  +.* ago · valley-changes-check$' '^  paused$' '^  1 commit · 1 file \+1 −0$'
after '^zzzz v2 on again  ' '^  again$' '^  1 commit · '
# The branches, latest first. atop-lost holds U's commit as well as its
# own, since U is no branch, and so is titled by U's commit.
after '^branches$' '^atop-lost  +.* ago · valley-changes-check$' '^  lost$' '^  2 commits · 2 files \+2 −0$' \
  '^$' '^loose  +.* ago · valley-changes-check$' '^  loose$' '^  1 commit · 1 file \+1 −0$' '^$' \
  '^a-parked  +11 days ago · valley-changes-check$' '^  a-parked$' '^  1 commit · 1 file \+1 −0$' '^$' '^deep-4  '
[ "$(grep -n '^drafts$' "$w/out" | cut -d: -f1)" -lt "$(grep -n '^branches$' "$w/out" | cut -d: -f1)" ] ||
  fail "pending listed the branches before the drafts"
# A chain four deep is one flat list, with no on line: each entry is on
# the one listed after it.
after '^deep-4  +3 weeks ago · valley-changes-check$' '^  deep-4$' '^  1 commit · 1 file \+1 −0$' \
  '^  approving it lands the 3 below too$' \
  '^  ├ deep-3  +3 weeks ago · valley-changes-check$' '^  │   deep-3$' '^  │   1 commit · 1 file \+1 −0$' \
  '^  ├ deep-2  +3 weeks ago · valley-changes-check$' '^  │   deep-2$' '^  │   1 commit · 1 file \+1 −0$' \
  '^  └ deep-1  +3 weeks ago · valley-changes-check$' '^      deep-1$' '^      1 commit · 1 file \+1 −0$' '^$'
no_line '^ *(│ *)?on deep-'
# Of two branches on one base, one lists the base, and the other says it
# is on it. The base is listed once.
[ "$(grep -cE '(^|├ |└ )fork-base  ' "$w/out")" = 1 ] || fail "pending did not list fork-base exactly once"
[ "$(grep -cx '  on fork-base' "$w/out")" = 1 ] || fail "not exactly one of the fork says it is on fork-base"
after '^fork-left  ' '^  fork-left$' '^  1 commit · 1 file \+1 −0$' '^  approving it lands the one below too$' \
  '^  └ fork-base  ' '^      fork-base$' '^      1 commit · 1 file \+1 −0$' '^$'
after '^fork-right  ' '^  fork-right$' '^  1 commit · 1 file \+1 −0$' '^  on fork-base$'
# --json is the same listing in valley-changes/v1's shape: the pending
# changes alone in changes, and the drafts in drafts.
ok valley pending --json
holds '.schema == "valley-pending/v1"' \
  '[.repos[0].changes[].handle] == ["kkkk", "mmmm", "nxvp", "pppp", "ssss", "vvvv", "wwww"]' \
  '[.repos[0].changes[] | select(.approved) | .handle] == ["kkkk", "nxvp"]' \
  '[.repos[0].changes[] | select(.approved | not) | .handle] == ["mmmm", "pppp", "ssss", "vvvv", "wwww"]' \
  '[.repos[0].changes[].approved | type] | unique == ["boolean"]' \
  '[.repos[0].changes[].state] | unique == ["ready"]' \
  '[.repos[0].changes[] | .landed_version, .landed_commit] | unique == [null]' \
  '[.repos[0].drafts[] | [.handle, .state]] == [["lmlm", "draft"], ["qqqq", "draft"], ["zzzz", "draft"]]' \
  '[.repos[0].branches[].branch] == ["a-parked", "atop-lost", "deep-1", "deep-2", "deep-3", "deep-4", "fork-base", "fork-left", "fork-right", "loose"]'
# Across the host, one listing: each row names its project, and the rows
# of every project are numbered in one landing order. Handles are
# qualified where a change ID is recorded in more than one project. A
# project that cannot be read says so first.
(cd "$w/origin" && branch atop-dup o-dup atop-dup)
ok valley pending --all
[ "$(head -1 "$w/out" | cut -c1-25)" = 'released: could not read ' ] || fail "pending --all did not open with the project it could not read"
line '^needs you \(8, in landing order\)$'
line '^  [1-8]  guarded  nxvpwlkz v1  +[0-9]+[smhd]$'
line '^  [1-8]  guarded  guarded:mmmm v1  +[0-9]+[smhd]$'
line '^  [1-8]  guarded  pqpq v1  +stack of 2 +[0-9]+[smhd]$'
line '^  [1-8]  open     pppp v1  +stack of 2 +[0-9]+[smhd]$'
line '^  [1-8]  origin   origin:mmmm v1  +[0-9]+[smhd]$'
line '^  8  open     ssss v2  +0s$'
line '^  ◐ open     nxvpwlkm v1  +approved$'
no_line '^(guarded|open|origin|sealed)$'
no_line 'oooo v1|open:mmmm v1'
[ "$(tail -1 "$w/out")" = '4 drafts · 11 branches no change is on · valley pending --drafts --branches' ] ||
  fail "pending --all did not count the drafts and the branches of every project"
ok valley pending --all --branches
after '^branches · origin$' '^atop-dup  ' '^  o-dup$' '^  2 commits · '

# --checks adds a line to each change with the checks it requires, by
# cost, as valley checks derives them, here and in a repository read
# through its clone. The instance gets a floor for that, written to its
# main on disk as the integrator would.
git init --quiet "$w/floor"
mkdir -p "$w/floor/policy/instance"
cp "$floor"/*.cue "$w/floor/policy/instance/"
git -C "$w/floor" add policy
git -C "$w/floor" commit --quiet -m floor
git -C "$w/floor" push --quiet "$instance" HEAD:refs/heads/floor
git -C "$instance" update-ref refs/heads/main "$(git -C "$w/floor" rev-parse HEAD)"
ok valley pending --checks --all
after '^  [1-8]  open     pppp v1  +1 check pending · stack of 2 ' '^     stack-c$' \
  '^     requires 1 quick check, 0 slow$' '^     checks: 1 pending$'
after '^  [1-8]  guarded  nxvpwlkz v1  +1 check pending ' '^     g-ready$' '^     requires 1 quick check, 0 slow$' '^     checks: 1 pending$'
# An approved change's row carries its check state.
after '^  ◐ open     (open:)?kkkk v1  +approved · 1 check pending$' '^    requires 1 quick check, 0 slow$' '^    checks: 1 pending$'
# Without --checks, no change lists its checks.
ok valley changes --json --all
holds '[.repos[].changes[].versions[] | select(has("checks"))] == []'

# ----------------------------------------------------------------------
# A branch name is used again once its change has landed: ff now names no
# change, and reparent none either, once its transfer statement verifies.
ok valley resolve ff
line "^branch"$'\t'"ff$"
ok valley resolve reparent
line "^branch"$'\t'"reparent$"
env -u VALLEY_KNOWN_SIGNERS valley resolve reparent > "$w/out" 2> "$w/err" || fail "resolve failed"
line "^change"$'\t'"$G"$'\t'
ok valley resolve stack-a
line "^change"$'\t'"$A"$'\t'

# H' has not landed, so its branch still names I, which is a draft, and
# approving the branch is refused for I's state rather than taken as a
# plain branch.
ok valley resolve again
line "^change"$'\t'"$I"$'\t'"zzzz"$'\t'"2"$'\t'"$hprime"$'\t'"again"$'\t'"draft"$'\t'"alice$"
if valley approve again > "$w/out" 2> "$w/err"; then fail "approve took the branch of a draft"; fi
grep -qx "⊘ zzzz v2  not approved · change is a draft" "$w/err" || fail "approve did not refuse the draft for its state"

# J' is not J's landing replayed, so its branch names J, a draft, too.
ok valley resolve ffrec
line "^change"$'\t'"$J"$'\t'"lmlm"$'\t'"2"$'\t'"$jprime"$'\t'"ffrec"$'\t'"draft"$'\t'"alice$"
if valley approve ffrec > "$w/out" 2> "$w/err"; then fail "approve took the branch of a replayed landing"; fi
grep -qx "⊘ lmlm v2  not approved · change is a draft" "$w/err" || fail "approve did not refuse J' for its state"

# A landed change names its branch no more once the branch moves on to new
# work: ff is a branch, free for that work.
git checkout --quiet -b ff-again ff
echo more > more.txt
git add more.txt
git commit --quiet -m more
git push --quiet origin ff-again:ff
ok valley resolve ff
line "^branch"$'\t'"ff$"

# ----------------------------------------------------------------------
# The listings name the knots that link each change: every knot in its
# project's plan graph whose changes lists valley:<project>/<change ID>,
# active knots first. The graph is written through the hook. nxvp is
# linked from two knots, one of them closed, mmmm from one, pppp from
# none, and the draft qqqq from the closed one. mmmm's change ID is linked
# in guarded's name too, and guarded has no graph, so its mmmm has none.
ok valley knot new outcome "The stack lands" --json
goal="$(jq -r .knot.id "$w/out")"
ok valley knot new bug "The stack reads the wrong base" --json
bug="$(jq -r .knot.id "$w/out")"
ok valley knot link "$goal" --change "valley:open/$A" --change "valley:open/$B"
ok valley knot link "$bug" --change "valley:open/$A" --change "valley:open/$D" --change "valley:guarded/$B"
ok valley knot set "$bug" status=closed
goal_ref="[\"$goal\", \"outcome\", \"open\", \"The stack lands\"]"
bug_ref="[\"$bug\", \"bug\", \"closed\", \"The stack reads the wrong base\"]"
k() { printf '[.repos[0].%s[] | select(.change_id == "%s") | .knots[] | [.id, .type, .status, .title]]' "$1" "$2"; }
ok valley changes --json
holds "$(k changes "$A") == [$goal_ref, $bug_ref]" "$(k changes "$B") == [$goal_ref]" "$(k changes "$C") == []" \
  "$(k changes "$D") == [$bug_ref]" '[.repos[0].changes[] | .knots | type] | unique == ["array"]'
if grep -q 'plan graph' "$w/err"; then fail 'changes spoke of the plan graph without -v'; fi
ok valley pending --json
holds "$(k changes "$A") == [$goal_ref, $bug_ref]" "$(k changes "$C") == []" "$(k drafts "$D") == [$bug_ref]"
# pending shows the first knot's ID after the title.
ok valley pending -v --drafts
after '^  1  pppp v1  ' '^     stack-c$' '^     1 commit · ' '^     checks: 1 pending$' \
  '^     approving it lands the one below too$' '^     └ mmmm v1  ' "^         stack-b · $goal\$"
after '^  ◐ nxvp v1  approved · 1 check pending$' "^    stack-a · $goal\$"
after '^qqqq v1 on paused  ' "^  paused · $bug\$"
ok valley status --json nxvp
holds "[.knots[] | [.id, .type, .status, .title]] == [$goal_ref, $bug_ref]"
ok valley status --json pppp
holds '.knots == []'
ok valley status --json loose
holds '.change_id == null and .knots == []'
ok valley changes --json --all
holds "[.repos[] | select(.repo == \"open\") | .changes[] | select(.change_id == \"$B\") | .knots[].id] == [\"$goal\"]" \
  "[.repos[] | select(.repo == \"guarded\") | .changes[] | select(.change_id == \"$B\") | .knots] == [[]]"
if grep -q 'plan graph' "$w/err"; then fail 'changes --all spoke of a missing plan graph without -v'; fi
ok valley changes --json --repo guarded -v
grep -qF 'guarded has no plan graph at refs/the-valley/knots, so no change shows a knot' "$w/err" \
  || fail 'changes -v did not say guarded has no plan graph'
# A graph that does not read links no change, and the listings go on as
# before. Only -v says why.
graph_tip="$(git -C "$data/open.git" rev-parse refs/the-valley/knots)"
junk="$(printf 'junk\n' | git -C "$data/open.git" hash-object -w --stdin)"
junk_tree="$(printf '100644 blob %s\tjunk.txt\n' "$junk" | git -C "$data/open.git" mktree)"
git -C "$data/open.git" update-ref refs/the-valley/knots "$(git -C "$data/open.git" commit-tree "$junk_tree" -m junk)"
ok valley changes --json
holds '(.repos[0].changes | length) == 13' '[.repos[0].changes[].knots] | unique == [[]]'
if grep -q 'plan graph' "$w/err"; then fail 'changes spoke of a broken plan graph without -v'; fi
ok valley pending -v
line '^    stack-a$'
no_line " · ($goal|$bug)\$"
ok valley status --json nxvp
holds '.knots == []'
ok valley changes --json -v
grep -qF "open's plan graph could not be read, so no change shows a knot" "$w/err" \
  || fail 'changes -v did not say why it shows no knot'
git -C "$data/open.git" update-ref refs/the-valley/knots "$graph_tip"

# ----------------------------------------------------------------------
# --checks: each ready change's and draft's required checks, passed,
# failed or pending by the note the integrator would judge each by. The host
# publishes its signers in the instance, as its publisher writes them,
# and a check worker's key is among them. The floor requires prose-format
# of each change here. The worker's note for one head's tree records a
# pass, for another a failure, and a third head has none. A note is
# stored on origin as attest run stores one: <check>/statement.note at
# refs/the-valley/attestations/<tree digest>/<key hash>.
cd "$w/open"
ssh-keygen -q -t ed25519 -N '' -C worker -f "$w/worker.key"
workerhash="$(attest key --key "$w/worker.key" --name worker | cut -d+ -f2)"
{
  cat "$w/known-signers"
  attest key --key "$w/worker.key" --name worker
} > "$w/host-signers"
keysblob="$(git -C "$instance" hash-object -w "$w/host-signers")"
nameblob="$(printf 'integrator\n' | git -C "$instance" hash-object -w --stdin)"
git -C "$instance" update-ref refs/the-valley/signers \
  "$(printf '100644 blob %s\tintegrator-name\n100644 blob %s\tknown-signers\n' "$nameblob" "$keysblob" | git -C "$instance" mktree)"

# evidence <branch> <result>: the worker's note about prose-format for the
# branch head's tree, stored on origin.
evidence() {
  local head digest blob tree
  head="$(git rev-parse "$1")"
  digest="$(tree_digest "$head")"
  jq -n --arg tree "$digest" --arg commit "$head" --arg result "$2" '{
      statementType: "the-valley/attestation/v1",
      subject: {primary: "valley-tree-v1", digest: {"valley-tree-v1": $tree, "git-sha1": $commit}},
      predicateType: "the-valley/check/pure/v1",
      predicate: {check: {name: "prose-format", runner: "nix", attribute: "prose-format"}, result: $result,
        inputs: {"valley-inputs-v1": ("cd" * 32)}, derivation: {sha256: ("ef" * 32)}, output: {sha256: ("01" * 32)}}
    }' > "$w/statement.json"
  attest render "$w/statement.json" | attest sign --key "$w/worker.key" --name worker > "$w/check.note"
  blob="$(git -C "$data/open.git" hash-object -w "$w/check.note")"
  tree="$(printf '100644 blob %s\tstatement.note\n' "$blob" | git -C "$data/open.git" mktree)"
  tree="$(printf '040000 tree %s\tprose-format\n' "$tree" | git -C "$data/open.git" mktree)"
  git -C "$data/open.git" update-ref "refs/the-valley/attestations/$digest/$workerhash" "$tree"
}
K1="$(id klkl)" K2="$(id kmkm)" K3="$(id knkn)"
git fetch --quiet origin
branch chk-pass origin/main chk-pass
ready "$K1" chk-pass
evidence chk-pass passed
branch chk-fail origin/main chk-fail
ready "$K2" chk-fail
evidence chk-fail failed
branch chk-none origin/main chk-none
ready "$K3" chk-none
# Three drafts, read from the same notes: one whose check passed, one
# whose check failed, and one with none.
D3="$(id mkmk)" D4="$(id mlml)" D5="$(id mnmn)"
branch dchk-pass origin/main dchk-pass
ready "$D3" dchk-pass
evidence dchk-pass passed
branch dchk-fail origin/main dchk-fail
ready "$D4" dchk-fail
evidence dchk-fail failed
branch dchk-none origin/main dchk-none
ready "$D5" dchk-none
ok valley unready "$D3"
ok valley unready "$D4"
ok valley unready "$D5"

ok valley changes --json --checks
checks() { printf '.repos[0].changes[] | select(.change_id == "%s") | .versions[.active-1].checks' "$1"; }
holds "$(checks "$K1") | .state == \"passed\" and .tree == \"$(tree_digest chk-pass)\"" \
  "$(checks "$K1") | .required == [{name: \"prose-format\", state: \"passed\", signer: \"worker+$workerhash\"}]" \
  "$(checks "$K2") | .state == \"failed\" and (.required | length) == 1" \
  "$(checks "$K2") | .required[0] | .name == \"prose-format\" and .state == \"failed\" and .signer == \"worker+$workerhash\"" \
  "$(checks "$K2") | .required[0].why | contains(\"predicate.result\")" \
  "$(checks "$K3") | .state == \"pending\" and .required == [{name: \"prose-format\", state: \"pending\"}]" \
  "$(checks "$D3") | .state == \"passed\" and .required == [{name: \"prose-format\", state: \"passed\", signer: \"worker+$workerhash\"}]" \
  "$(checks "$D4") | .state == \"failed\" and .required[0].name == \"prose-format\" and .required[0].signer == \"worker+$workerhash\"" \
  "$(checks "$D4") | .required[0].why | contains(\"predicate.result\")" \
  "$(checks "$D5") | .state == \"pending\" and .required == [{name: \"prose-format\", state: \"pending\"}]" \
  '[.repos[0].changes[] | select(.state == "withdrawn") | .versions[] | select(has("checks"))] == []' \
  '[.repos[0].changes[] | .versions[] | select(.landing == "landed" or .landing == "landed-recorded") | select(has("checks"))] == []'
ok valley pending --json --checks
holds "$(checks "$K2") | .state == \"failed\"" '[.repos[0].drafts[].versions[] | select(has("checks"))] == []'
ok valley pending --json --drafts --checks
holds "$(checks "$K2") | .state == \"failed\"" \
  "[.repos[0].drafts[] | [.handle, .versions[.active-1].checks.required[0].state]] == [[\"lmlm\", \"pending\"], [\"mkmk\", \"passed\"], [\"mlml\", \"failed\"], [\"mnmn\", \"pending\"], [\"qqqq\", \"pending\"], [\"zzzz\", \"pending\"]]"
# Each row counts its checks, passed out of required when all passed, and
# otherwise how many are pending. A change whose check failed moves, and
# says which. The rows are in landing order: pppp, U and H became ready
# before K1 and K3, and W's v2 last, though K1's and K3's change IDs sort
# first.
ok valley pending
after '^needs you \(6, in landing order\)$' '^  1  pppp v1  1 check pending · stack of 2 ' '^     stack-c$' \
  '^  2  vvvv v1  1 check pending ' '^     lost$' '^  3  wwww v1  1 check pending ' '^     forged$' \
  '^  4  klkl v1  checks 1/1 +[0-9]+[smhd]$' '^     chk-pass$' '^  5  knkn v1  1 check pending ' '^     chk-none$' \
  '^  6  ssss v2  1 check pending +0s$' '^     atop-gone-v2$' '^$' '^moving \(3\)$'
line '^  ✗ kmkm v1  prose-format failed$'
line '^  ◐ kkkk v1  approved · 1 check pending$'
ok valley pending --checks
after '^  4  klkl v1  ' '^     chk-pass$' '^     requires 1 quick check, 0 slow$' '^     checks: 1 passed$'
after '^  ✗ kmkm v1  prose-format failed$' '^    requires 1 quick check, 0 slow$' '^    checks: 1 failed \(prose-format\)$'
after '^  5  knkn v1  ' '^     chk-none$' '^     requires 1 quick check, 0 slow$' '^     checks: 1 pending$'
no_line '(mkmk|mlml|mnmn) v1'
# With --drafts, a draft's block says its checks in a ready row's words.
ok valley pending --drafts --checks
after '^mkmk v1 on dchk-pass  checks 1/1  ' '^  dchk-pass$' '^  1 commit · ' \
  '^  requires 1 quick check, 0 slow$' '^  checks: 1 passed$'
after '^mlml v1 on dchk-fail  ✗ prose-format failed  ' '^  dchk-fail$' '^  1 commit · ' \
  '^  requires 1 quick check, 0 slow$' '^  checks: 1 failed \(prose-format\)$'
after '^mnmn v1 on dchk-none  1 check pending  ' '^  dchk-none$' '^  1 commit · ' \
  '^  requires 1 quick check, 0 slow$' '^  checks: 1 pending$'
# review shows a draft's checks line, and still offers no approve.
printf 'a\ns\n' | ok env VALLEY_REVIEW_CHECKS_WAIT=120 valley review "$D4"
grep -qx 'mlml v1 · checks: 1 failed (prose-format) · draft (its owner, alice, has not readied it)' "$w/out" \
  || fail "review's prompt did not carry the draft's checks line"
line 'answer v or s$'
no_line '\[a\]pprove'
# Readied at the same head, the draft lists the same check states.
ready "$D4" dchk-fail
ok valley changes --json --checks
holds "$(checks "$D4") | .state == \"failed\" and .tree == \"$(tree_digest dchk-fail)\" and (.required | length) == 1" \
  "$(checks "$D4") | .required[0] | .state == \"failed\" and .signer == \"worker+$workerhash\" and (.why | contains(\"predicate.result\"))"
ok valley pending
line '^  ✗ mlml v1  prose-format failed$'

# --checks with the host's worker telemetry: each nix check's build time
# for the version, the median of its recent uncached, passing builds in
# the project, the other versions of the change left out, a check grown
# past its baseline, and a check with no history. The host is reached
# over ssh, here a fake ssh that answers the read valley telemetry read
# makes from a file of the worker's records, paged as the collector pages
# them: the newest 1000 without a cursor, else the 1000 after it. It
# refuses while $tel/refuse exists, as a host refuses a principal with no
# observe grant.
tel="$w/telemetry"
mkdir -p "$tel/bin"
{
  printf '#!%s\n' "$(command -v bash)"
  cat << 'EOF'
set -euo pipefail
command="${*: -1}"
printf '%s\n' "$command" >> "$TELEMETRY_DIR/ssh.log"
if [ -e "$TELEMETRY_DIR/refuse" ]; then
  printf '%s\n' '{"kind":"error","error":"alice holds no observe grant"}'
  exit 1
fi
since=-1
for word in $command; do
  case "$word" in --since=worker=*) since="${word##*:}" ;; esac
done
jq -c -s --argjson since "$since" '
  (map(.seq) | max) as $last
  | (if $since < 0 then [$last - 1000, 0] | max else $since end) as $after
  | [.[] | select(.seq > $after)][:1000] as $page
  | {kind: "header", host: "valley-host", format: "valley-telemetry/v1"},
    ($page[] | {kind: "record", stream: "worker"} + .),
    {kind: "end", cursor: "worker=0123456789abcdef:\(($page | last | .seq) // $after)", truncated: false}
' "$TELEMETRY_DIR/worker.jsonl"
EOF
} > "$tel/bin/ssh"
chmod +x "$tel/bin/ssh"
export TELEMETRY_DIR="$tel" PATH="$tel/bin:$PATH"
git config valley.telemetry.remote ssh://git@valley-host/open.git

# build <seq> <project> <change> <version> <tree> <result> <build ms> <eval ms> <log bytes> [<cached> [<capacity>]]:
# a check.result record of prose-format, as attest stores one.
build() {
  jq -n -c --argjson seq "$1" --arg project "$2" --arg change "$3" --argjson version "$4" --arg tree "$5" \
    --arg result "$6" --argjson build "$7" --argjson eval "$8" --argjson log "$9" --arg cached "${10:-}" --arg capacity "${11:-}" '
    {seq: $seq, type: "check.result", time: (1762128000 + $seq | todate), run: "r\($seq)", project: $project,
      change: $change, version: $version, tree: $tree, check: "prose-format",
      data: ({result: $result, build_ms: $build, eval_ms: $eval, slot_wait_ms: 0, log_bytes: $log}
        + (if $cached == "" then {} else {cached: ($cached == "true")} end)
        + (if $capacity == "" then {} else {capacity: $capacity} end))}'
}
# filler <from> <to>: the records of passes that built nothing.
filler() {
  jq -n -c --argjson from "$1" --argjson to "$2" \
    'range($from; $to + 1) | {seq: ., type: "pass.start", time: (1762128000 + . | todate), run: "p\(.)"}'
}
O1="$(id lplp)" O2="$(id lqlq)" O3="$(id lrlr)" O4="$(id lsls)" O5="$(id ltlt)" O6="$(id lulu)"
t1="$(tree_digest chk-pass)" t2="$(tree_digest chk-fail)"
{
  # The recent builds of prose-format in this project, older than the
  # newest 1000 records, so a second page reads them. A cached build, one
  # an older attest recorded with no log and so cached, a failed build and
  # another project's are no baseline. Another version of K1's is no
  # baseline for K1, and is for the other changes.
  build 1 open "$O1" 1 "" passed 50000 20000 10 false
  build 2 open "$O2" 1 "" passed 60000 20000 10
  build 3 open "$O3" 3 "" passed 70000 20000 10 false
  build 4 open "$O4" 1 "" passed 5 20000 0 true
  build 5 open "$O5" 1 "" passed 10 20000 0
  build 6 open "$O6" 1 "" failed 1000 20000 10 false
  build 7 guarded "$O1" 1 "" passed 1 20000 10 false
  build 8 open "$K1" 2 "" passed 900000 20000 10 false
  filler 9 1499
  # The versions' own builds.
  build 1500 open "$K1" 1 "$t1" passed 100000 24000 20 false
  build 1501 open "$K2" 1 "$t2" failed 61000 23000 20 false
} > "$tel/worker.jsonl"

ok valley changes --json --checks
pf() { printf '%s | .required[] | select(.name == "prose-format")' "$(checks "$1")"; }
holds "$(pf "$K1") | .build_ms == 100000 and .cached == false and .baseline_ms == 60000 and .grown == true and (has(\"new\") | not)" \
  "$(checks "$K1") | .build_ms == 100000 and .eval_ms == 24000 and .state == \"passed\"" \
  "$(pf "$K2") | .build_ms == 61000 and .baseline_ms == 70000 and (has(\"grown\") | not) and .state == \"failed\"" \
  "$(pf "$K3") | (has(\"build_ms\") | not) and .baseline_ms == 70000 and .state == \"pending\"" \
  "$(checks "$K3") | has(\"build_ms\") | not"
[ "$(wc -l < "$tel/ssh.log")" = 2 ] || fail "the listing read the worker's telemetry otherwise than in two pages: $(cat "$tel/ssh.log")"
grep -qx 'valley-telemetry read --source=worker --limit=1000' "$tel/ssh.log" || fail "the first page was not the newest: $(cat "$tel/ssh.log")"
grep -qx 'valley-telemetry read --source=worker --since=worker=0123456789abcdef:0 --limit=1000' "$tel/ssh.log" ||
  fail "the second page did not read on from the first's cursor: $(cat "$tel/ssh.log")"
rm "$tel/ssh.log"
ok valley pending --checks
after '^  4  klkl v1  ' '^     chk-pass$' '^     requires 1 quick check, 0 slow$' \
  '^     checks: 1 passed in 1m40s \(eval 24s\); prose-format 100s, \+40s against its…$'
after '^  ✗ kmkm v1  prose-format failed$' '^    requires 1 quick check, 0 slow$' \
  '^    checks: 1 failed \(prose-format\) in 1m1s \(eval 23s\)$'
after '^  5  knkn v1  ' '^     chk-none$' '^     requires 1 quick check, 0 slow$' '^     checks: 1 pending$'
[ "$(wc -l < "$tel/ssh.log")" = 2 ] || fail "pending read the worker's telemetry otherwise than in two pages: $(cat "$tel/ssh.log")"
# review shows the same line just before its prompt, whole: it is paged,
# not fitted to a terminal. It reads the line while it shows the diff and
# does not wait for it, so this run is told to wait.
printf 's\n' | ok env VALLEY_REVIEW_CHECKS_WAIT=120 valley review "$K1"
[ "$(grep -B1 '^\[a\]pprove' "$w/out" | head -1)" = 'klkl v1 · checks: 1 passed in 1m40s (eval 24s); prose-format 100s, +40s against its recent 60s' ] \
  || fail "review's prompt did not carry the checks line: $(cat "$w/out")"

# A check with no recent build in the project is new, which is no news:
# the line leaves it out.
{
  build 1 guarded "$O1" 1 "" passed 50000 20000 10 false
  build 2 open "$K1" 1 "$t1" passed 100000 24000 20 false
} > "$tel/worker.jsonl"
ok valley changes --json --checks
holds "$(pf "$K1") | .build_ms == 100000 and .new == true and (has(\"baseline_ms\") | not) and (has(\"grown\") | not)"
ok valley pending --checks
after '^  4  klkl v1  ' '^     chk-pass$' '^     requires 1 quick check, 0 slow$' \
  '^     checks: 1 passed in 1m40s \(eval 24s\)$'

# Builds on another capacity are no baseline: the check of a resized host
# has no comparable history, and has not grown.
{
  build 1 open "$O1" 1 "" passed 10000 20000 10 false "slots=1 cpus=4"
  build 2 open "$K1" 1 "$t1" passed 100000 24000 20 false "slots=2 cpus=8"
} > "$tel/worker.jsonl"
ok valley changes --json --checks
holds "$(pf "$K1") | .new == true and .other_capacity == true and (has(\"baseline_ms\") | not) and (has(\"grown\") | not)"
ok valley pending --checks
after '^  4  klkl v1  ' '^     chk-pass$' '^     requires 1 quick check, 0 slow$' \
  '^     checks: 1 passed in 1m40s \(eval 24s\)$'

# A host that refuses leaves every build time out, says why once, and
# lists the check states all the same.
touch "$tel/refuse"
ok valley changes --json --checks
holds "$(checks "$K1") | .required == [{name: \"prose-format\", state: \"passed\", signer: \"worker+$workerhash\"}] and (has(\"build_ms\") | not)" \
  "$(checks "$K2") | .required == [{name: \"prose-format\", state: \"failed\", signer: \"worker+$workerhash\", why: .required[0].why}]" \
  "$(checks "$K3") | .required == [{name: \"prose-format\", state: \"pending\"}]"
[ "$(grep -c 'no check lists its build times: .*alice holds no observe grant' "$w/err")" = 1 ] ||
  fail "a refusing host was not said once on stderr: $(cat "$w/err")"
ok valley pending --checks
after '^  4  klkl v1  ' '^     chk-pass$' '^     requires 1 quick check, 0 slow$' '^     checks: 1 passed$'

# valley help names the verbs front ends look for.
valley help > "$w/out"
line '^  ready '
line '^  changes '

touch "$out"
