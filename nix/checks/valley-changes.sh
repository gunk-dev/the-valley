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
  '(.repos[0].changes | length) == 10'
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
  "$(c "$H") | .versions[0].landing == \"uncertain\""
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
holds '[.repos[].repo] == ["guarded", "open", "released", "sealed"]' \
  '.repos[] | select(.repo == "released") | .error != null and .changes == [] and .url == "'"$data/released.git"'"' \
  '.repos[] | select(.repo != "released") | .error == null' \
  '.repos[] | select(.repo == "sealed") | .changes == []' \
  '[.repos[].changes[].handle] | length == (unique | length)' \
  "[.repos[].changes[] | select(.change_id == \"$A\" or .change_id == \"$R\") | .handle] == [\"nxvpwlkz\", \"nxvpwlkm\"]" \
  '.repos[] | select(.repo == "guarded") | [.changes[].state] == ["ready", "draft", "withdrawn"]'
# --repo limits the listing to one repository, read through its clone.
ok valley changes --json --repo guarded
holds '[.repos[].repo] == ["guarded"]' "[.repos[0].changes[].change_id] == [\"$R\", \"$D2\", \"$E2\"]" \
  '.repos[0].changes[0].handle == "nxvp"'
cd "$w/guarded"
ok valley changes --json
holds '[.repos[].repo] == ["guarded"]' '.repos[0].changes[0].handle == "nxvp"'
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

# ----------------------------------------------------------------------
# pending: the ready changes that have not landed, by handle, version and
# title, then the branches no record is on, then the drafts.
ok valley pending
line '^nxvp v1 \| stack-a$'
line '^mmmm v1 \| stack-b \| includes nxvp$'
line '^pppp v1 \| stack-c \| includes mmmm$'
line '^ssss v1 \| atop-gone \| includes rrrr$'
line '^vvvv v1 \| lost \| uncertain$'
line '^wwww v1 \| forged \| uncertain$'
no_line '^(tttt|uuuu|rrrr|qqqq) v1 \|'
line '^branches$'
line '^loose \| loose \| .* \| ahead 1 \| 1 file changed'
line '^drafts$'
line '^qqqq v1 on paused \| paused \| .* \| ahead 1 \| '
[ "$(grep -n '^branches$' "$w/out" | cut -d: -f1)" -lt "$(grep -n '^drafts$' "$w/out" | cut -d: -f1)" ] ||
  fail "pending listed the drafts before the branches"
# --json is the same listing in valley-changes/v1's shape.
ok valley pending --json
holds '.schema == "valley-pending/v1"' \
  '[.repos[0].changes[].handle] == ["mmmm", "nxvp", "pppp", "qqqq", "ssss", "vvvv", "wwww"]' \
  '.repos[0].branches == [{branch: "loose", head: "'"$(git rev-parse loose)"'"}]'
# Across the host, each repository is headed by its name.
ok valley pending --all
line '^guarded$'
line '^nxvpwlkz v1 \| g-ready$'
line '^guarded drafts$'
line '^open$'
line '^nxvpwlkm v1 \| stack-a$'
line '^released: could not read '

# --owes ends each change's line with the checks it owes, by cost, as
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
line '^nxvpwlkm v1 \| stack-a \| owes 1 quick, 0 slow$'
line '^mmmm v1 \| stack-b \| includes nxvpwlkm \| owes 1 quick, 0 slow$'
line '^nxvpwlkz v1 \| g-ready \| owes 1 quick, 0 slow$'

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

# valley help names the verbs klaus looks for.
valley help > "$w/out"
line '^  ready '
line '^  changes '

touch "$out"
