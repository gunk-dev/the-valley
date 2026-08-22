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
# The project, with the two policy layers where the deriver looks without
# being told. Both checks use the command runner: the nix runner cannot be
# exercised from inside a nix build, since the sandbox has no daemon to
# call, and what is under test here is the sequencing rather than the
# runner.
repo="$TMPDIR/project"
git init --quiet "$repo"
cd "$repo" || exit 1
git remote add origin "$origin"

mkdir -p docs src schema examples/policy/instance examples/policy/project
cp "$schemaFile" schema/verification.cue
echo "the readme" > docs/readme.md
cat > examples/policy/instance/floor.cue <<'EOF'
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
	unclassified: "tree-ok": true
}
EOF
cat > examples/policy/project/policy.cue <<'EOF'
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
# 2. main moves on, so the next branch is stale. [a]sk is offered there
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

grep -q 'does not fast-forward from main: \[a\]sk, \[b\]ase onto main, \[r\]eject, \[s\]kip?' "$w/fail.out"
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
# 3. No signing key: refused before anything is built, and the prompt
#    still comes back. The host signs, and there is no unsigned mode.
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
# 4. [i]ntegrate is untouched: both paths coexist until the writers change
#    lands, and neither verb knows which one the operator may use.
git checkout --quiet main
git checkout --quiet -b topic/three
echo "another readme" > docs/readme.md
git commit --quiet -am "another readme"
git push --quiet origin topic/three

printf 'i\n' > "$w/answers"
valley review topic/three < "$w/answers" > "$w/integrate.out" 2>&1
grep -q '^topic/three: \[a\]sk, \[i\]ntegrate, \[r\]eject, \[s\]kip? ' "$w/integrate.out"
grep -q 'integrated topic/three: main is now' "$w/integrate.out"
if [ "$(git -C "$origin" rev-parse refs/heads/main)" = "$moved" ]; then
  echo "valley-request: [i]ntegrate did not move main" >&2
  exit 1
fi

# The request filed in 1 is still pending: nothing here consumes it, which
# is the integrator's own step.
git -C "$origin" rev-parse --verify --quiet \
  refs/the-valley/integration-requests/main/topic-one > /dev/null

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
