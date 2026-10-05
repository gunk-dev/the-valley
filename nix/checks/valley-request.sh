# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
export HOME="$TMPDIR"
export XDG_STATE_HOME="$TMPDIR/state"
export GIT_CONFIG_NOSYSTEM=1
# What this check writes down, kept out of the repository under test: an
# answer file or a captured stream sitting in the working tree would be
# committed by the next `git add -A` and change the tree being attested.
w="$TMPDIR/scratch"
mkdir -p "$w"
git config --global user.name valley-check
git config --global user.email valley-check@localhost
git config --global init.defaultBranch main
git config --global advice.detachedHead false

# The operator's signing key. Provisioning one is deployment, so the verb
# takes it as a parameter and this check supplies a throwaway.
ssh-keygen -q -t ed25519 -N "" -C valley-check -f "$TMPDIR/host"
export VALLEY_ATTEST_KEY="$TMPDIR/host"
export VALLEY_ATTEST_NAME=laddie.valley.invalid/attestations
keyhash="$(attest key --key "$TMPDIR/host" --name "$VALLEY_ATTEST_NAME" | cut -d+ -f2)"

origin="$TMPDIR/project.git"
git init --quiet --bare "$origin"

# Every push the bare repo receives, as its hook sees it. The claim [a]sk
# makes is that ONE push carries the evidence and the request together, and
# that claim is only observable from the receiving side.
pushlog="$TMPDIR/pushlog"
cat > "$origin/hooks/pre-receive" <<EOF
#!/bin/sh
echo '--- push' >> $pushlog
cat >> $pushlog
EOF
chmod +x "$origin/hooks/pre-receive"

# Interpose on origin's receive-pack so that the request namespace moves
# between [a]sk's read of it and the push it then makes. That window is
# where every race below lives, and a hook cannot reach it: by the time
# pre-receive runs the client has already been told what the refs were.
# One shot, so only the push under test is raced.
arm_race() {
  cat > "$TMPDIR/racing-receive-pack" <<EOF
#!/bin/sh
if [ -e "$TMPDIR/race-armed" ]; then
  rm -f "$TMPDIR/race-armed"
  $1
fi
exec git-receive-pack "\$@"
EOF
  chmod +x "$TMPDIR/racing-receive-pack"
  git config remote.origin.receivepack "$TMPDIR/racing-receive-pack"
  touch "$TMPDIR/race-armed"
}

# Put it away, and fail if it never fired: a race nothing raced is a
# scenario that proved nothing.
disarm_race() {
  git config --unset remote.origin.receivepack
  if [ -e "$TMPDIR/race-armed" ]; then
    echo "valley-request: the racing receive-pack never ran, so nothing was raced" >&2
    exit 1
  fi
}

# ----------------------------------------------------------------------
# The owning valley's repository, carrying the floor at policy/instance.
# The floor is read from here and never from the project, which is the
# tree under judgment: what governs a change has to come from outside it.
# Fetching is all the deriver does to this repository, which is what
# anyone the valley governs necessarily holds.
valley="$TMPDIR/valley.git"
git init --quiet --bare "$valley"
valleywork="$TMPDIR/valley-work"
git init --quiet "$valleywork"
mkdir -p "$valleywork/policy/instance"
cat > "$valleywork/policy/instance/floor.cue" <<'EOF'
package verification

floor: {
	checks: "tree-ok": {
		runner:  "command"
		command: "test -f docs/readme.md"
	}
	classes: prose: {
		paths: "docs/**":    true
		requires: "tree-ok": true
	}
	// A class that covers paths and asks nothing of them. Owing no check
	// is an ordinary outcome, not a hole: the policy has read these paths
	// and has nothing to require of them.
	classes: quiet: {
		paths: "quiet/**": true
		requires: {}
	}
	unclassified: "tree-ok": true
}
EOF
git -C "$valleywork" add -A
git -C "$valleywork" commit --quiet -m floor
git -C "$valleywork" push --quiet "$valley" main

# ----------------------------------------------------------------------
# The project, laid out the way the deriver reads one without being told:
# its own layer at policy/project, and its owning valley named at
# policy/valley. Both checks use the command runner: the nix runner cannot
# be exercised from inside a nix build, since the sandbox has no daemon to
# call, and what is under test here is the sequencing rather than the
# runner.
#
# examples/policy/ holds a second, contradictory pair of layers, and every
# check they require is named `phantom`. Nothing may derive it: a gate that
# came from a worked example is a gate nobody wrote, and the name is here
# so that the failure would be visible rather than plausible.
#
# schema/ is here because the-valley's own tree carries it. The packaged
# CLI never reads it; only bin/valley run straight from the checkout does
# (16).
repo="$TMPDIR/project"
git init --quiet "$repo"
cd "$repo" || exit 1
git remote add origin "$origin"

mkdir -p docs src schema policy/project examples/policy/instance examples/policy/project
cp "$schemaFile" schema/verification.cue
echo "the readme" > docs/readme.md
echo "$valley" > policy/valley
cat > policy/project/policy.cue <<'EOF'
package verification

project: {
	checks: "no-secrets": {
		runner:  "command"
		command: "! grep -rqF SECRET src"
	}
	classes: source: {
		paths: "src/**":        true
		requires: "no-secrets": true
	}
}
EOF
cat > examples/policy/instance/floor.cue <<'EOF'
package verification

floor: {
	checks: phantom: {
		runner:  "command"
		command: "false"
	}
	classes: everything: {
		paths: "**":       true
		requires: phantom: true
	}
	unclassified: phantom: true
}
EOF
cat > examples/policy/project/policy.cue <<'EOF'
package verification

project: {}
EOF
git add -A
git commit --quiet -m base
git push --quiet -u origin main
first="$(git rev-parse main)"

# ----------------------------------------------------------------------
# 1. A branch whose required check passes: the evidence and the request
#    reach origin in one push, and main does not move.
git checkout --quiet -b topic/one
echo "a better readme" > docs/readme.md
git commit --quiet -am "improve the readme"
git push --quiet origin topic/one
digest="$(attest digest --rev topic/one | cut -d: -f2)"

: > "$pushlog"
printf 'a\n' > "$w/answers"
valley review topic/one < "$w/answers" > "$w/ask.out" 2> "$w/ask.err"

grep -q '^checking topic/one at .*: tree-ok$' "$w/ask.out"
grep -qE '^check   tree-ok +command  passed$' "$w/ask.out"
grep -qx "Submitted topic/one ($(git rev-parse --short topic/one)) for integration into origin/main." "$w/ask.out"
grep -qx "  request  refs/the-valley/integration-requests/main/topic-one -> $(git rev-parse --short topic/one)" "$w/ask.out"
grep -qx "  evidence refs/the-valley/attestations/$digest/$keyhash" "$w/ask.out"
grep -qx '  checks   tree-ok' "$w/ask.out"
grep -qx 'Your local checkout has not changed.' "$w/ask.out"
grep -qx 'Check the result: valley status topic/one' "$w/ask.out"

# What origin holds now. The attestation is keyed by the digest of the
# BRANCH's tree, which is what says the checks ran over the right tree and
# not over the operator's working copy.
git -C "$origin" for-each-ref --format='%(refname)' refs/the-valley > "$w/published.txt"
grep -qx "refs/the-valley/integration-requests/main/topic-one" "$w/published.txt"
grep -qx "refs/the-valley/attestations/$digest/$keyhash" "$w/published.txt"
if [ "$(git -C "$origin" rev-parse refs/the-valley/integration-requests/main/topic-one)" \
  != "$(git rev-parse topic/one)" ]; then
  echo "valley-request: the request ref does not name the branch's head" >&2
  exit 1
fi
if [ "$(git -C "$origin" rev-parse refs/heads/main)" != "$first" ]; then
  echo "valley-request: [a]sk moved main" >&2
  exit 1
fi

# One push carried both. This is the friction the verb exists to remove:
# `attest run --push` publishes the branch and the evidence and knows
# nothing about a request ref, so doing this by hand is two beats.
pushes="$(grep -c '^--- push' "$pushlog" || true)"
if [ "$pushes" != 1 ]; then
  echo "valley-request: the evidence and the request took $pushes pushes, not 1" >&2
  cat "$pushlog" >&2
  exit 1
fi
grep -q "refs/the-valley/attestations/$digest/$keyhash" "$pushlog"
grep -q 'refs/the-valley/integration-requests/main/topic-one' "$pushlog"

# The note origin holds is the one the operator signed, over that tree.
git cat-file -p "refs/the-valley/attestations/$digest/$keyhash:tree-ok/statement.note" \
  > "$w/statement.note"
attest key --key "$TMPDIR/host" --name "$VALLEY_ATTEST_NAME" > "$w/known_keys"
attest verify --note "$w/statement.note" --repo "$repo" --rev topic/one \
  --known-keys "$w/known_keys" --signer "$VALLEY_ATTEST_NAME" > "$w/verify.out"
grep -q 'matches the recorded subject digest' "$w/verify.out"

# ----------------------------------------------------------------------
# 2. The layers the derivation used are the ones that govern, and it says
#    which they were: the floor fetched from the owning valley, and the
#    project's own layer at policy/project in the TARGET's tip.
#    examples/policy/ is in this tree and requires `phantom` of every path,
#    and no default reaches it — it is derivable only by naming it, which
#    is what the options are for.
valley checks "$first" topic/one > "$w/checks.out" 2> "$w/checks.err"
grep -qx "policy: $valley policy/instance@refs/heads/main + policy/project@origin/main $(git rev-parse --short origin/main)" "$w/checks.out"
grep -qx 'classes matched: prose' "$w/checks.out"
grep -qE '^  tree-ok +mandatory +prose$' "$w/checks.out"
if grep -q phantom "$w/checks.out" "$w/checks.err"; then
  echo "valley-request: a default derived a check from examples/policy/" >&2
  cat "$w/checks.out" "$w/checks.err" >&2
  exit 1
fi

# Named, the example layers derive exactly what they declare. This is the
# other half of the claim above: examples/policy/ is readable, and reading
# it is something a person asks for.
valley checks --instance examples/policy/instance --project examples/policy/project \
  "$first" topic/one > "$w/example.out"
grep -qE '^  phantom +mandatory' "$w/example.out"
grep -qx 'policy: examples/policy/instance + examples/policy/project' "$w/example.out"

# ----------------------------------------------------------------------
# 3. main moves on, so the next branch is stale. [a]sk is offered there
#    too — the integrator applies a delta to the tip rather than
#    fast-forwarding — and a branch whose check fails publishes nothing
#    and leaves the review loop usable.
git checkout --quiet main
echo "a note" > docs/notes.md
git add -A
git commit --quiet -m "a landing that moves main"
git push --quiet origin main

git checkout --quiet -b topic/two "$first"
printf 'SECRET\n' > src/leak.txt
git add -A
git commit --quiet -m "a leak"
git push --quiet origin topic/two

: > "$pushlog"
printf 'a\ns\n' > "$w/answers"
valley review topic/two < "$w/answers" > "$w/fail.out" 2> "$w/fail.err"

# The prompt is the whole of what the operator is offered, so it is pinned
# character for character: a verb that stops being offered, or one that
# starts, is a change to the review loop and not an incidental one.
grep -qF 'topic/two does not fast-forward from main: [a]sk, [b]ase onto main, [r]eject, [s]kip? ' "$w/fail.out"
grep -q '^checking topic/two at .*: no-secrets$' "$w/fail.out"
grep -qE '^check   no-secrets +command  failed$' "$w/fail.out"
grep -q 'the checks did not pass; nothing published, main untouched' "$w/fail.err"
# The prompt is printed without a newline, so what follows a refusal shares
# its line — the answer to it is not anchored.
grep -q 'skipped topic/two; nothing done' "$w/fail.out"
if [ -s "$pushlog" ]; then
  echo "valley-request: a failing check pushed something" >&2
  cat "$pushlog" >&2
  exit 1
fi
git -C "$origin" for-each-ref --format='%(refname)' refs/the-valley > "$w/after.txt"
if grep -q 'topic-two' "$w/after.txt"; then
  echo "valley-request: a failing check left a ref on origin" >&2
  cat "$w/after.txt" >&2
  exit 1
fi

# ----------------------------------------------------------------------
# 4. No signing key, with a check owed: refused before any check is run,
#    and the prompt still comes back. The host signs, and there is no
#    unsigned mode.
: > "$pushlog"
printf 'a\ns\n' > "$w/answers"
env -u VALLEY_ATTEST_KEY valley review topic/two < "$w/answers" > "$w/nokey.out" 2> "$w/nokey.err"
grep -q 'no signing key: set VALLEY_ATTEST_KEY' "$w/nokey.err"
grep -q 'skipped topic/two; nothing done' "$w/nokey.out"
if [ -s "$pushlog" ]; then
  echo "valley-request: a run with no signing key pushed something" >&2
  exit 1
fi

# ----------------------------------------------------------------------
# 5. A branch the policy requires no check of is filed as a request with
#    no evidence. The integrator lands one and records it as a landing
#    that required nothing, so refusing to file it here would strand
#    exactly the changes a policy leaves unrequired. Nothing is owed, so
#    nothing is signed: this runs with no signing key at all.
git checkout --quiet main
git checkout --quiet -b topic/quiet
mkdir -p quiet
echo "a quiet note" > quiet/note.md
git add -A
git commit --quiet -m "a change under a class that asks nothing"
git push --quiet origin topic/quiet
quiethead="$(git rev-parse --short topic/quiet)"

: > "$pushlog"
printf 'a\n' > "$w/answers"
env -u VALLEY_ATTEST_KEY valley review topic/quiet < "$w/answers" \
  > "$w/quiet.out" 2> "$w/quiet.err"

grep -q 'owes no check under .* + policy/project@origin/main .*; filing the request with no evidence' "$w/quiet.out"
grep -qF "$valley policy/instance@refs/heads/main" "$w/quiet.out"
grep -q '^Submitted topic/quiet .* for integration into origin/main\.$' "$w/quiet.out"
grep -qx "  request  refs/the-valley/integration-requests/main/topic-quiet -> $quiethead" "$w/quiet.out"
grep -qx '  evidence none — nothing was owed, so nothing was signed' "$w/quiet.out"
grep -qx '  checks   none — the policy requires no check of this change' "$w/quiet.out"

# One push, carrying the request and nothing else.
pushes="$(grep -c '^--- push' "$pushlog" || true)"
if [ "$pushes" != 1 ]; then
  echo "valley-request: the bare request took $pushes pushes, not 1" >&2
  cat "$pushlog" >&2
  exit 1
fi
if grep -q 'refs/the-valley/attestations/' "$pushlog"; then
  echo "valley-request: a request owing nothing published evidence" >&2
  cat "$pushlog" >&2
  exit 1
fi
if [ "$(git -C "$origin" rev-parse refs/the-valley/integration-requests/main/topic-quiet)" \
  != "$(git rev-parse topic/quiet)" ]; then
  echo "valley-request: the bare request does not name the branch's head" >&2
  exit 1
fi

# ----------------------------------------------------------------------
# 6. [b]ase, and the prompt it comes back to. A stale branch is rebased
#    onto main in a throwaway worktree and re-reviewed, and the prompt the
#    loop then prints is the fast-forward one — the path a reader of
#    bin/valley has to trace two loops to reach, so it is exercised here
#    rather than argued about.
git checkout --quiet main
git checkout --quiet -b topic/three "$first"
echo "another readme" > docs/readme.md
git commit --quiet -am "another readme"
git push --quiet origin topic/three
before_base="$(git -C "$origin" rev-parse refs/heads/main)"

printf 'b\ns\n' > "$w/answers"
valley review topic/three < "$w/answers" > "$w/base.out" 2>&1
grep -qF 'topic/three does not fast-forward from main: [a]sk, [b]ase onto main, [r]eject, [s]kip? ' "$w/base.out"
grep -qF 'rebased topic/three onto origin/main, pushed with --force-with-lease' "$w/base.out"
# The post-[b]ase prompt: the branch fast-forwards now, so the loop falls
# through to the prompt that has no [b]ase in it.
grep -qF 'topic/three: [a]sk, [r]eject, [s]kip? ' "$w/base.out"
grep -q 'skipped topic/three; nothing done' "$w/base.out"
git merge-base --is-ancestor refs/remotes/origin/main refs/remotes/origin/topic/three \
  || { echo "valley-request: [b]ase left the branch stale" >&2; exit 1; }
if [ "$(git -C "$origin" rev-parse refs/heads/main)" != "$before_base" ]; then
  echo "valley-request: the review loop moved main" >&2
  exit 1
fi
git checkout --quiet main

# The request filed in 1 is still pending: nothing here consumes it, which
# is the integrator's own step.
git -C "$origin" rev-parse --verify --quiet \
  refs/the-valley/integration-requests/main/topic-one > /dev/null

# ----------------------------------------------------------------------
# 7. A change does not supply the policy that gates it. This branch
#    deletes its own policy/project and repoints policy/valley at a
#    repository that does not exist. Both layers come from the target's
#    tip, so neither edit reaches the derivation: it composes the same two
#    layers it would for any other branch, and the fetch that a branch-read
#    pointer would have attempted never happens.
git checkout --quiet -b topic/nolayer
git rm --quiet -r policy/project
echo "$TMPDIR/there-is-no-such-valley.git" > policy/valley
git add -A
git commit --quiet -m "delete the project layer and repoint the valley"
git push --quiet origin topic/nolayer

valley checks "$first" topic/nolayer > "$w/nolayer.out" 2> "$w/nolayer.err"
grep -qx "policy: $valley policy/instance@refs/heads/main + policy/project@origin/main $(git rev-parse --short origin/main)" "$w/nolayer.out"
if grep -q 'there-is-no-such-valley' "$w/nolayer.out" "$w/nolayer.err"; then
  echo "valley-request: the deriver followed the branch's own valley pointer" >&2
  cat "$w/nolayer.out" "$w/nolayer.err" >&2
  exit 1
fi

: > "$pushlog"
printf 'a\n' > "$w/answers"
valley review topic/nolayer < "$w/answers" \
  > "$w/nolayer.review.out" 2> "$w/nolayer.review.err"
grep -q '^Submitted topic/nolayer .* for integration into origin/main\.$' "$w/nolayer.review.out"

# ----------------------------------------------------------------------
# 8. The reviewer's checkout contributes nothing. This is the case that
#    failed in live use: the operator's checkout was an older branch
#    without the landed policy layer, and the deriver read the working
#    tree and refused. topic/nolayer is exactly such a checkout — no
#    policy/project in it, and a policy/valley naming nowhere — so every
#    derivation below runs from it.
#
#    Byte-identical output is the claim, not merely a successful one:
#    review works the same from any branch, however stale.
git checkout --quiet main
valley checks "$first" topic/one > "$w/from-main.out"
git checkout --quiet topic/nolayer
valley checks "$first" topic/one > "$w/from-stale.out"
if ! cmp -s "$w/from-main.out" "$w/from-stale.out"; then
  echo "valley-request: the checkout changed what the derivation said" >&2
  diff -u "$w/from-main.out" "$w/from-stale.out" >&2 || true
  exit 1
fi

# And [a]sk, which is where it failed: a request filed from that same
# checkout, over a branch the stale tree knows nothing about.
git checkout --quiet -b topic/four origin/main
echo "one more readme" > docs/readme.md
git commit --quiet -am "one more readme"
git push --quiet origin topic/four
git checkout --quiet topic/nolayer

: > "$pushlog"
printf 'a\n' > "$w/answers"
valley review topic/four < "$w/answers" > "$w/stale.out" 2> "$w/stale.err"
grep -q '^checking topic/four at .*: tree-ok$' "$w/stale.out"
grep -q '^Submitted topic/four .* for integration into origin/main\.$' "$w/stale.out"
grep -qx '  checks   tree-ok' "$w/stale.out"

# ----------------------------------------------------------------------
# 9. A target carrying no project layer at all is derived under the floor
#    alone, and says so. The project layer only ever adds, so the identity
#    of "adds" is "adds nothing"; refusing instead would make a repository
#    underivable until its first policy commit, and that commit could not
#    derive either. This is the integrator's own reading of an absent
#    layer (integrator/policy.go), and the two have to agree.
bare2="$TMPDIR/floorless.git"
git init --quiet --bare "$bare2"
repo2="$TMPDIR/floorless"
git init --quiet "$repo2"
cd "$repo2" || exit 1
git remote add origin "$bare2"
mkdir -p docs policy
echo "the readme" > docs/readme.md
echo "$valley" > policy/valley
git add -A
git commit --quiet -m base
git push --quiet -u origin main
base2="$(git rev-parse main)"
echo "a second readme" > docs/readme.md
git commit --quiet -am "a second readme"

valley checks "$base2" HEAD > "$w/floorless.out" 2> "$w/floorless.err"
grep -qx "policy: $valley policy/instance@refs/heads/main + no policy/project@origin/main $(git rev-parse --short origin/main) — the floor alone" "$w/floorless.out"
grep -qE '^  tree-ok +mandatory +prose$' "$w/floorless.out"
if grep -q no-secrets "$w/floorless.out"; then
  echo "valley-request: the floor-alone derivation saw another project's layer" >&2
  exit 1
fi
cd "$repo" || exit 1
git checkout --quiet main

# ----------------------------------------------------------------------
# 10. Resubmission after main moved. This is the case that wedged in live
#     use. A request goes stale with reason=evidence when main lands
#     inside a required check's input closure: no attestation over the
#     submitted tree can transfer again, so the cure is a new head rebased
#     onto the moved main. That head is not a descendant of the one the
#     request ref holds, which makes writing it a non-fast-forward
#     replacement — and a push without a lease is refused by git's own
#     rule before origin's hook is ever consulted. The verdict that sends
#     a change down this path is the integrator's and is pinned by
#     integrator-e2e; what is under test here is that [a]sk can act on it.
git checkout --quiet -b topic/five origin/main
echo "a readme worth asking about" > docs/readme.md
git commit --quiet -am "a readme worth asking about"
git push --quiet origin topic/five
stalehead="$(git rev-parse topic/five)"
staledigest="$(attest digest --rev topic/five | cut -d: -f2)"

printf 'a\n' > "$w/answers"
valley review topic/five < "$w/answers" > "$w/five-first.out" 2> "$w/five-first.err"
grep -qx "  request  refs/the-valley/integration-requests/main/topic-five -> $(git rev-parse --short "$stalehead")" "$w/five-first.out"

# main moves, and the change is rebased onto it — the operator's half of
# a resubmission, which [a]sk does not do and does not need to.
git checkout --quiet main
echo "a second note" > docs/second.md
git add -A
git commit --quiet -m "a landing under the same class"
git push --quiet origin main
git checkout --quiet topic/five
git rebase --quiet main
git push --quiet --force-with-lease origin topic/five
newhead="$(git rev-parse topic/five)"
newdigest="$(attest digest --rev topic/five | cut -d: -f2)"

# The premise of the scenario, asserted rather than assumed: the rebase
# produced a head off the old one's line and a tree the old evidence is
# not about. Without both, the push below would be an ordinary one and
# would prove nothing.
if git merge-base --is-ancestor "$stalehead" "$newhead"; then
  echo "valley-request: the rebased head still descends from the old one" >&2
  exit 1
fi
if [ "$staledigest" = "$newdigest" ]; then
  echo "valley-request: the rebase left the tree the old evidence is about" >&2
  exit 1
fi

: > "$pushlog"
printf 'a\n' > "$w/answers"
valley review topic/five < "$w/answers" > "$w/five-again.out" 2> "$w/five-again.err"

grep -q '^Submitted topic/five .* for integration into origin/main\.$' "$w/five-again.out"
grep -qx "  request  refs/the-valley/integration-requests/main/topic-five -> $(git rev-parse --short "$newhead")" "$w/five-again.out"
# Replacing a pending request is said out loud, and the sentence names the
# head that was replaced.
grep -qx "  replaced the pending request, which stood at $(git rev-parse --short "$stalehead") (a readme worth asking about)" "$w/five-again.out"

if [ "$(git -C "$origin" rev-parse refs/the-valley/integration-requests/main/topic-five)" \
  != "$newhead" ]; then
  echo "valley-request: the resubmitted request does not name the rebased head" >&2
  exit 1
fi

# Both attestations stand. The old one is about a tree that will never
# land, but the namespace is create-only and a record that can be dropped
# is not one; the resubmission publishes beside it, never over it.
for stands in "refs/the-valley/attestations/$staledigest/$keyhash" \
  "refs/the-valley/attestations/$newdigest/$keyhash"; do
  git -C "$origin" rev-parse --verify --quiet "$stands" > /dev/null || {
    echo "valley-request: $stands is not on origin after the resubmission" >&2
    exit 1
  }
done

# One push again, and the request ref moved off the old head inside it.
pushes="$(grep -c '^--- push' "$pushlog" || true)"
if [ "$pushes" != 1 ]; then
  echo "valley-request: the resubmission took $pushes pushes, not 1" >&2
  cat "$pushlog" >&2
  exit 1
fi
grep -qx "$stalehead $newhead refs/the-valley/integration-requests/main/topic-five" "$pushlog"
# And every attestation ref in that push is a creation: an all-zero old id
# is what the create-only namespace admits, and a resubmission that
# updated one would be refused by the real hook.
if grep '^[0-9a-f]* [0-9a-f]* refs/the-valley/attestations/' "$pushlog" \
  | grep -qv '^0* '; then
  echo "valley-request: the resubmission updated an attestation ref" >&2
  cat "$pushlog" >&2
  exit 1
fi
if [ "$(git -C "$origin" rev-parse refs/heads/main)" != "$(git rev-parse main)" ]; then
  echo "valley-request: the resubmission moved main" >&2
  exit 1
fi

# ----------------------------------------------------------------------
# 11. Two resubmissions of one change race for one ref, and the loser
#     loses visibly. The request ref's value is read before the push and
#     the push is leased against exactly that value, so the second one to
#     arrive is refused rather than allowed to overwrite the first. The
#     race is made real: a receive-pack wrapper moves the ref on origin
#     after [a]sk has read it and before the push it then makes, which is
#     what another operator getting there first looks like from here.
git checkout --quiet main
git checkout --quiet -b topic/six origin/main
echo "a readme two resubmissions both rebased" > docs/readme.md
git commit --quiet -am "a readme two resubmissions both rebased"
git push --quiet origin topic/six
sixfiled="$(git rev-parse topic/six)"

printf 'a\n' > "$w/answers"
valley review topic/six < "$w/answers" > "$w/six-first.out" 2> "$w/six-first.err"
grep -q '^Submitted topic/six .* for integration into origin/main\.$' "$w/six-first.out"

git checkout --quiet main
echo "a third note" > docs/third.md
git add -A
git commit --quiet -m "another landing under the same class"
git push --quiet origin main
git checkout --quiet topic/six
git rebase --quiet main
git push --quiet --force-with-lease origin topic/six
sixhead="$(git rev-parse topic/six)"
sixdigest="$(attest digest --rev topic/six | cut -d: -f2)"

# The other resubmission's head, built where origin can already reach
# every object in it: the same tree, parented on the same tip, so it is
# the commit a second operator rebasing this change would have produced.
racer="$(git -C "$origin" commit-tree "$(git rev-parse 'topic/six^{tree}')" \
  -p "$(git rev-parse origin/main)" -m "the other resubmission")"

arm_race "git -C '$origin' update-ref refs/the-valley/integration-requests/main/topic-six '$racer'"

: > "$pushlog"
printf 'a\ns\n' > "$w/answers"
valley review topic/six < "$w/answers" > "$w/race.out" 2> "$w/race.err"
disarm_race

# A refusal, named as one, and the review loop still usable after it.
grep -q "the pending request moved from $(git rev-parse --short "$sixfiled") to $(git rev-parse --short "$racer") while this one was being prepared" "$w/race.err"
grep -q 'the lease on refs/the-valley/integration-requests/main/topic-six refused' "$w/race.err"
grep -q 'skipped topic/six; nothing done' "$w/race.out"

# Nothing published. The winner's request stands untouched, the loser's
# evidence never reached origin, and origin saw no push at all — the lease
# refuses before any command is sent, which is what --atomic is worth.
if [ "$(git -C "$origin" rev-parse refs/the-valley/integration-requests/main/topic-six)" \
  != "$racer" ]; then
  echo "valley-request: the lease did not hold; the other resubmission was clobbered" >&2
  exit 1
fi
if git -C "$origin" rev-parse --verify --quiet \
  "refs/the-valley/attestations/$sixdigest/$keyhash" > /dev/null; then
  echo "valley-request: a refused resubmission published its evidence" >&2
  exit 1
fi
if [ -s "$pushlog" ]; then
  echo "valley-request: a refused resubmission reached origin's hook" >&2
  cat "$pushlog" >&2
  exit 1
fi

# The checks that were run are not lost with the refusal: their notes are
# in the operator's own repository, and the refusal says where.
git rev-parse --verify --quiet "refs/the-valley/attestations/$sixdigest/$keyhash" \
  > /dev/null || {
  echo "valley-request: the refused resubmission kept no evidence locally" >&2
  exit 1
}
grep -qx 'valley: the evidence is stored locally at:' "$w/race.err"
grep -qx "  refs/the-valley/attestations/$sixdigest/$keyhash" "$w/race.err"
git checkout --quiet main

# ----------------------------------------------------------------------
# 12. Asking again at the same head. Evidence with a validity window
#     expires while the tree stays the right tree, and the answer to that
#     is a fresh observation over the same head. The request ref already
#     names that head, so it is left out of the refspec entirely rather
#     than pushed at the value origin already holds.
#
#     The request is filed by hand here. Origin holding a request at this
#     head is the whole of the state under test, and re-running [a]sk to
#     produce it would put a second-granular observation instant in the
#     note: the same tree re-attested inside one second is the identical
#     ref, and across a second boundary it is not.
git checkout --quiet -b topic/seven origin/main
echo "a readme asked about twice" > docs/readme.md
git commit --quiet -am "a readme asked about twice"
git push --quiet origin topic/seven
sevenhead="$(git rev-parse topic/seven)"
sevendigest="$(attest digest --rev topic/seven | cut -d: -f2)"
git push --quiet origin \
  "$sevenhead:refs/the-valley/integration-requests/main/topic-seven"

: > "$pushlog"
printf 'a\n' > "$w/answers"
valley review topic/seven < "$w/answers" > "$w/seven.out" 2> "$w/seven.err"

grep -q '^Submitted topic/seven .* for integration into origin/main\.$' "$w/seven.out"
grep -qx '  the request already stood at this head; the evidence is what was published' "$w/seven.out"
# The evidence went; the request ref was not in the refspec at all.
grep -q "refs/the-valley/attestations/$sevendigest/$keyhash" "$pushlog"
if grep -q 'refs/the-valley/integration-requests/main/topic-seven' "$pushlog"; then
  echo "valley-request: a same-head re-ask pushed the request ref again" >&2
  cat "$pushlog" >&2
  exit 1
fi
if [ "$(git -C "$origin" rev-parse refs/the-valley/integration-requests/main/topic-seven)" \
  != "$sevenhead" ]; then
  echo "valley-request: a same-head re-ask disturbed the request ref" >&2
  exit 1
fi

# The same head with nothing owed: no evidence to publish and no ref to
# write, so no push is made at all. topic/quiet's request has stood on
# origin at this head since 5. A push assembled with an empty refspec
# would fall back to origin's default one, and pushing whatever that
# happens to name is not what asking about a change means.
git checkout --quiet main
: > "$pushlog"
printf 'a\n' > "$w/answers"
env -u VALLEY_ATTEST_KEY valley review topic/quiet < "$w/answers" \
  > "$w/quiet-again.out" 2> "$w/quiet-again.err"
grep -qx "  request  refs/the-valley/integration-requests/main/topic-quiet -> $quiethead" "$w/quiet-again.out"
grep -qx '  the request already stood at this head and nothing was owed, so nothing needed publishing' "$w/quiet-again.out"
if [ -s "$pushlog" ]; then
  echo "valley-request: a re-ask with nothing to publish still pushed" >&2
  cat "$pushlog" >&2
  exit 1
fi

# ----------------------------------------------------------------------
# 13. Two operators filing one change at once. Reading that the request
#     ref is absent is a value like any other, and the lease covers it:
#     git spells that expectation with an empty value and refuses if the
#     ref exists at all. Unleased, this push would have fast-forwarded the
#     other filing away without saying a word.
git checkout --quiet -b topic/eight origin/main
echo "a readme two operators both filed" > docs/readme.md
git commit --quiet -am "a readme two operators both filed"
git push --quiet origin topic/eight
eighthead="$(git rev-parse topic/eight)"
eightdigest="$(attest digest --rev topic/eight | cut -d: -f2)"
# The other operator's filing: this same tree on this same tip, which is
# what a second checkout of one branch produces. It is a descendant of
# nothing here, so an unleased push over it would be a plain fast-forward.
firstfiler="$(git -C "$origin" commit-tree "$(git rev-parse 'topic/eight^{tree}')" \
  -p "$(git rev-parse origin/main)" -m "the other operator's filing")"

arm_race "git -C '$origin' update-ref refs/the-valley/integration-requests/main/topic-eight '$firstfiler'"
: > "$pushlog"
printf 'a\ns\n' > "$w/answers"
valley review topic/eight < "$w/answers" > "$w/create-race.out" 2> "$w/create-race.err"
disarm_race

# The other operator's commit is on origin and not here, so the subject
# says so rather than inventing one. Scenario 10 pins the case where the
# replaced head IS local and its subject is printed.
grep -q "a request for this change appeared while this one was being prepared, at $(git rev-parse --short "$firstfiler") (subject not in this repository)" "$w/create-race.err"
grep -q 'skipped topic/eight; nothing done' "$w/create-race.out"
if [ "$(git -C "$origin" rev-parse refs/the-valley/integration-requests/main/topic-eight)" \
  != "$firstfiler" ]; then
  echo "valley-request: an unleased filing clobbered the request that got there first" >&2
  exit 1
fi
if git -C "$origin" rev-parse --verify --quiet \
  "refs/the-valley/attestations/$eightdigest/$keyhash" > /dev/null; then
  echo "valley-request: a refused filing published its evidence" >&2
  exit 1
fi
if [ -s "$pushlog" ]; then
  echo "valley-request: a refused filing reached origin's hook" >&2
  cat "$pushlog" >&2
  exit 1
fi

# ----------------------------------------------------------------------
# 14. A lease that refuses because the ref is gone, not because it moved.
#     The integrator consumes the request ref when the change lands, so a
#     resubmission prepared just before that lands into a lease against a
#     ref that no longer exists. Git reports it as stale info either way,
#     and only reading the ref again tells the two apart — so the two are
#     told apart, and the operator is pointed at the landing rather than
#     at a race that did not happen.
git checkout --quiet main
git checkout --quiet -b topic/nine origin/main
echo "a readme that lands mid-resubmission" > docs/readme.md
git commit --quiet -am "a readme that lands mid-resubmission"
git push --quiet origin topic/nine

printf 'a\n' > "$w/answers"
valley review topic/nine < "$w/answers" > "$w/nine-first.out" 2> "$w/nine-first.err"
grep -q '^Submitted topic/nine .* for integration into origin/main\.$' "$w/nine-first.out"

git checkout --quiet main
echo "a fourth note" > docs/fourth.md
git add -A
git commit --quiet -m "a landing that moves main again"
git push --quiet origin main
git checkout --quiet topic/nine
git rebase --quiet main
git push --quiet --force-with-lease origin topic/nine
ninedigest="$(attest digest --rev topic/nine | cut -d: -f2)"

arm_race "git -C '$origin' update-ref -d refs/the-valley/integration-requests/main/topic-nine"
: > "$pushlog"
printf 'a\ns\n' > "$w/answers"
valley review topic/nine < "$w/answers" > "$w/consumed.out" 2> "$w/consumed.err"
disarm_race

grep -q 'the pending request was consumed while this one was being prepared, which is what the integrator does when a change lands' "$w/consumed.err"
grep -q 'nothing was published; fetch and review the branch again before asking a second time' "$w/consumed.err"
# Not reported as a race, which is the distinction the re-read buys.
if grep -q 'the pending request moved from' "$w/consumed.err"; then
  echo "valley-request: a consumed request was reported as a lost race" >&2
  cat "$w/consumed.err" >&2
  exit 1
fi
grep -q 'skipped topic/nine; nothing done' "$w/consumed.out"
if git -C "$origin" rev-parse --verify --quiet \
  refs/the-valley/integration-requests/main/topic-nine > /dev/null; then
  echo "valley-request: a refused resubmission re-created the consumed request" >&2
  exit 1
fi
if git -C "$origin" rev-parse --verify --quiet \
  "refs/the-valley/attestations/$ninedigest/$keyhash" > /dev/null; then
  echo "valley-request: a refused resubmission published its evidence" >&2
  exit 1
fi
if [ -s "$pushlog" ]; then
  echo "valley-request: a refused resubmission reached origin's hook" >&2
  cat "$pushlog" >&2
  exit 1
fi

# ----------------------------------------------------------------------
# 15. The same-head push carries no request ref, so it carries no
#     compare-and-swap on one either — git leaves an up-to-date ref out of
#     what it sends. Where the ref stands after that push is therefore not
#     something the run established, and it is read again rather than
#     asserted from the read before it.
#
#     Nothing re-files it. A request for a change already in the stream is
#     judged against an empty delta on every pass, which is why the
#     integrator consumes the ref in the first place; re-creating it here
#     would be a guess about which of consumption and deletion happened,
#     paid for with a second push behind the one this verb promises. So
#     the run says what is true and stops, and asking again is the repair.
git checkout --quiet main
git checkout --quiet -b topic/ten origin/main
echo "a readme consumed under the evidence" > docs/readme.md
git commit --quiet -am "a readme consumed under the evidence"
git push --quiet origin topic/ten
tenhead="$(git rev-parse topic/ten)"
tendigest="$(attest digest --rev topic/ten | cut -d: -f2)"
git push --quiet origin \
  "$tenhead:refs/the-valley/integration-requests/main/topic-ten"

arm_race "git -C '$origin' update-ref -d refs/the-valley/integration-requests/main/topic-ten"
: > "$pushlog"
printf 'a\ns\n' > "$w/answers"
valley review topic/ten < "$w/answers" > "$w/posthoc.out" 2> "$w/posthoc.err"
disarm_race

grep -q 'the request was consumed while this run was working, which is what the integrator does when a change lands' "$w/posthoc.err"
grep -q 'Nothing here re-filed it' "$w/posthoc.err"
# The claim the old code would have made, from a read that had gone out of
# date under it.
if grep -q 'the request already stood at this head' "$w/posthoc.out"; then
  echo "valley-request: a consumed request was reported as still standing" >&2
  cat "$w/posthoc.out" >&2
  exit 1
fi
grep -q 'skipped topic/ten; nothing done' "$w/posthoc.out"

# The evidence went, because that push succeeded; the request did not come
# back, because nothing here writes it.
git -C "$origin" rev-parse --verify --quiet \
  "refs/the-valley/attestations/$tendigest/$keyhash" > /dev/null || {
  echo "valley-request: the evidence push was reported as done and is not on origin" >&2
  exit 1
}
if git -C "$origin" rev-parse --verify --quiet \
  refs/the-valley/integration-requests/main/topic-ten > /dev/null; then
  echo "valley-request: the consumed request was re-filed behind the operator" >&2
  exit 1
fi
# And that evidence went in one push, which is still the whole claim.
pushes="$(grep -c '^--- push' "$pushlog" || true)"
if [ "$pushes" != 1 ]; then
  echo "valley-request: the same-head re-ask took $pushes pushes, not 1" >&2
  cat "$pushlog" >&2
  exit 1
fi
git checkout --quiet main

# ----------------------------------------------------------------------
# 16. A project other than the-valley carries no schema/. The schema ships
#     with the tool, so the packaged CLI derives what a change owes and
#     files the request from a checkout that has none. This is the case
#     that failed in live use: [a]sk in a valley's config repository, which
#     carries its own floor at policy/instance, refused with "no policy
#     schema" and published nothing.
cfgorigin="$TMPDIR/config.git"
git init --quiet --bare "$cfgorigin"
cfg="$TMPDIR/config"
git init --quiet "$cfg"
cd "$cfg" || exit 1
git remote add origin "$cfgorigin"
mkdir -p docs policy/instance
cp "$valleywork/policy/instance/floor.cue" policy/instance/floor.cue
echo "the readme" > docs/readme.md
git add -A
git commit --quiet -m base
git push --quiet -u origin main
git checkout --quiet -b topic/config
echo "a config readme" > docs/readme.md
git commit --quiet -am "a config readme"
git push --quiet origin topic/config
cfgdigest="$(attest digest --rev topic/config | cut -d: -f2)"

printf 'a\n' > "$w/answers"
valley review topic/config < "$w/answers" > "$w/config.out" 2> "$w/config.err"
if grep -q 'no policy schema' "$w/config.err"; then
  echo "valley-request: [a]sk looked for the schema in a project's checkout" >&2
  cat "$w/config.err" >&2
  exit 1
fi
grep -q '^checking topic/config at .*: tree-ok$' "$w/config.out"
grep -qx "Submitted topic/config ($(git rev-parse --short topic/config)) for integration into origin/main." "$w/config.out"
git -C "$cfgorigin" rev-parse --verify --quiet \
  "refs/the-valley/attestations/$cfgdigest/$keyhash" > /dev/null || {
  echo "valley-request: [a]sk in a project without schema/ published no evidence" >&2
  exit 1
}
if [ "$(git -C "$cfgorigin" rev-parse refs/the-valley/integration-requests/main/topic-config)" \
  != "$(git rev-parse topic/config)" ]; then
  echo "valley-request: [a]sk in a project without schema/ filed no request for its head" >&2
  exit 1
fi

# checks without --schema reads the packaged schema too, and derives what
# [a]sk ran from this repository's own floor.
valley checks > "$w/config-checks.out" 2> "$w/config-checks.err"
grep -qF '(this repository is its own valley)' "$w/config-checks.out"
grep -qE '^  tree-ok +mandatory +prose$' "$w/config-checks.out"

# The packaged schema is a default and not a pin. A schema named in the
# environment is the one read, and --schema overrides both.
if VALLEY_VERIFICATION_SCHEMA="$TMPDIR/no-such-schema.cue" valley checks \
  > "$w/envschema.out" 2> "$w/envschema.err"; then
  echo "valley-request: VALLEY_VERIFICATION_SCHEMA did not replace the packaged schema" >&2
  exit 1
fi
grep -qF "no policy schema at $TMPDIR/no-such-schema.cue" "$w/envschema.err"
VALLEY_VERIFICATION_SCHEMA="$TMPDIR/no-such-schema.cue" valley checks --schema "$schemaFile" \
  > "$w/flagschema.out" 2> "$w/flagschema.err"
grep -qE '^  tree-ok +mandatory +prose$' "$w/flagschema.out"

# bin/valley run straight from a checkout, with nothing setting the
# variable, reads schema/verification.cue in that checkout. the-valley's own
# tree carries one, as the first scratch project does, and every other
# project's tree does not.
if env -u VALLEY_VERIFICATION_SCHEMA bash "$cliScript" checks \
  > "$w/bare-config.out" 2> "$w/bare-config.err"; then
  echo "valley-request: bin/valley run bare found a schema in a checkout without one" >&2
  exit 1
fi
grep -qF "no policy schema at $(git rev-parse --show-toplevel)/schema/verification.cue" \
  "$w/bare-config.err"
cd "$repo" || exit 1
env -u VALLEY_VERIFICATION_SCHEMA bash "$cliScript" checks "$first" topic/one \
  > "$w/bare.out" 2> "$w/bare.err"
grep -qE '^  tree-ok +mandatory +prose$' "$w/bare.out"

# ----------------------------------------------------------------------
# The verbs the loop offers are the whole of what an operator can do to a
# branch, so no path may offer one outside the set: [a]sk, [b]ase,
# [r]eject, [s]kip. Every stream this check captured is scanned at the end,
# which is what makes the claim about paths rather than about the two
# prompts pinned character for character above — the fast-forward prompt,
# the stale one, the one [b]ase comes back to, and the ones a refused [a]sk
# returns to are all in here. A verb that writes the protected ref from the
# operator's own checkout — the retired [i]ntegrate, or any successor to
# it — fails this wherever it is printed.
offered="$(cat "$w"/*.out | grep -o '\[[a-z]\][a-z]*' | sort -u || true)"
outside="$(printf '%s\n' "$offered" | grep -vxE '\[a\]sk|\[b\]ase|\[r\]eject|\[s\]kip' || true)"
if [ -n "$outside" ]; then
  echo "valley-request: a review path offered a verb outside the set:" >&2
  printf '%s\n' "$outside" >&2
  exit 1
fi
# The scan is only worth something if it saw the offers at all.
for verb in '[a]sk' '[b]ase' '[r]eject' '[s]kip'; do
  printf '%s\n' "$offered" | grep -qxF "$verb" \
    || { echo "valley-request: no captured session offered $verb" >&2; exit 1; }
done

# No throwaway worktree outlived any of it: only the checkout is left.
worktrees="$(git worktree list --porcelain | grep -c '^worktree ' || true)"
if [ "$worktrees" != 1 ]; then
  echo "valley-request: a throwaway worktree was left behind" >&2
  git worktree list >&2
  exit 1
fi

echo "every scenario held"
{
  cat "$w/ask.out"
  printf '\n--- the scratch origin, after all of it ---\n'
  git -C "$origin" for-each-ref --format='%(objectname) %(refname)'
} > "$out"
