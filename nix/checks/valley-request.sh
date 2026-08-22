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
grep -qx 'requested topic/one -> refs/heads/main' "$w/ask.out"
grep -qx "  request  refs/the-valley/integration-requests/main/topic-one -> $(git rev-parse --short topic/one)" "$w/ask.out"
grep -qx "  evidence refs/the-valley/attestations/$digest/$keyhash" "$w/ask.out"
grep -qx '  checks   tree-ok' "$w/ask.out"
grep -q 'main is untouched' "$w/ask.out"

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
moved="$(git rev-parse main)"

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
grep -qx 'requested topic/quiet -> refs/heads/main' "$w/quiet.out"
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
# 6. [i]ntegrate is untouched: both paths coexist until the writers change
#    lands, and neither verb knows which one the operator may use.
git checkout --quiet main
git checkout --quiet -b topic/three
echo "another readme" > docs/readme.md
git commit --quiet -am "another readme"
git push --quiet origin topic/three

printf 'i\n' > "$w/answers"
valley review topic/three < "$w/answers" > "$w/integrate.out" 2>&1
grep -qF 'topic/three: [a]sk, [i]ntegrate, [r]eject, [s]kip? ' "$w/integrate.out"
grep -q 'integrated topic/three: main is now' "$w/integrate.out"
if [ "$(git -C "$origin" rev-parse refs/heads/main)" = "$moved" ]; then
  echo "valley-request: [i]ntegrate did not move main" >&2
  exit 1
fi

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
grep -qx 'requested topic/nolayer -> refs/heads/main' "$w/nolayer.review.out"

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
grep -qx 'requested topic/four -> refs/heads/main' "$w/stale.out"
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
mkdir -p docs schema policy
cp "$schemaFile" schema/verification.cue
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
