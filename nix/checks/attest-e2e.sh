# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
export HOME="$TMPDIR"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name valley-check
git config --global user.email valley-check@localhost
git config --global init.defaultBranch main

# Throwaway keys in a scratch directory. The signer is a
# parameter: provisioning a host identity is deployment, and
# nothing here reads or writes anything under a real ~/.ssh.
# A verifier is given key names and hashes, so what it holds
# is what `attest key` prints and nothing more.
ssh-keygen -q -t ed25519 -N "" -C valley-check -f "$TMPDIR/host"
ssh-keygen -q -t ed25519 -N "" -C witness -f "$TMPDIR/witness"
ssh-keygen -q -t ed25519 -N "" -C stranger -f "$TMPDIR/stranger"
host=host.valley.invalid/attestations
witness=witness.valley.invalid/attestations
stranger=stranger.elsewhere.invalid/attestations
attest key --key "$TMPDIR/host" --name "$host" > "$TMPDIR/known_keys"
attest key --key "$TMPDIR/witness" --name "$witness" >> "$TMPDIR/known_keys"
attest key --key "$TMPDIR/stranger" --name "$stranger" > "$TMPDIR/other_keys"
grep -q "^$host+[0-9a-f]\{8\}+A" "$TMPDIR/known_keys"

# A key behind a passphrase is refused rather than prompted
# for: attest runs unattended.
ssh-keygen -q -t ed25519 -N "a passphrase" -C locked -f "$TMPDIR/locked"
if attest key --key "$TMPDIR/locked" --name "$host" 2> locked.err; then
  echo "attest-e2e: an encrypted key must be refused" >&2
  exit 1
fi
grep -q 'cannot ask for a passphrase' locked.err

repo="$TMPDIR/tree"
git init --quiet "$repo"
cd "$repo" || exit 1
echo hello > hello.txt
mkdir -p sub
echo nested > sub/nested.txt
ln -s hello.txt link
printf '#!/bin/sh\ntrue\n' > run.sh
chmod 755 run.sh
git add -A
git commit --quiet -m base

digest="$(attest digest | sed 's/^valley-tree-v1://')"
case "$digest" in
  [0-9a-f][0-9a-f]*) ;;
  *) echo "attest-e2e: no tree digest: $digest" >&2; exit 1 ;;
esac

refs() { git for-each-ref --format='%(refname)' refs/the-valley; }
nothing_published() {
  if [ -n "$(refs)" ]; then
    echo "attest-e2e: $1" >&2
    git for-each-ref refs/the-valley >&2
    exit 1
  fi
}

# Without a key, nothing is emitted — an unsigned statement
# is not an attestation.
if attest run --command 'ok=true' 2> nokey.err; then
  echo "attest-e2e: a run with no signing key must fail" >&2
  exit 1
fi
grep -q 'no signing key' nokey.err
nothing_published "a run with no signing key stored a ref"

# A failing check publishes nothing, and says which check.
if attest run --key "$TMPDIR/host" --name "$host" \
  --command 'ok=true' --command 'nope=false' > failing.out 2> failing.err; then
  echo "attest-e2e: a failing check must fail the run" >&2
  exit 1
fi
grep -qE '^check   nope +command  failed$' failing.out
grep -q 'nothing published' failing.err
nothing_published "a failing check left a ref behind"

# A statement the schema rejects is stopped before it is
# signed or stored. Provenance is the caller's, so a
# malformed one is how a malformed statement gets composed
# at all.
echo '{"harness":"valley-check","authority":"root"}' > bad-provenance.json
if attest run --key "$TMPDIR/host" --name "$host" --command 'ok=true' \
  --provenance bad-provenance.json > malformed.out 2> malformed.err; then
  echo "attest-e2e: a malformed statement must fail the run" >&2
  exit 1
fi
grep -q 'provenance.authority: field not allowed' malformed.err
grep -q 'nothing published' malformed.err
nothing_published "a malformed statement left a ref behind"

# A statement the schema admits but no line can carry is
# stopped too, at the same point and for the same reason.
printf '{"harness":"valley\\ncheck"}\n' > unwritable-provenance.json
if attest run --key "$TMPDIR/host" --name "$host" --command 'ok=true' \
  --provenance unwritable-provenance.json > unwritable.out 2> unwritable.err; then
  echo "attest-e2e: a statement with no written form must fail the run" >&2
  exit 1
fi
grep -q 'nothing published' unwritable.err
nothing_published "a statement with no written form left a ref behind"

# The successful run.
echo '{"harness":"valley-check","model":"none","delegation":[{"principal":"human:integrator","grant":"land changes"}]}' > provenance.json
attest run --key "$TMPDIR/host" --name "$host" --command 'ok=true' \
  --provenance provenance.json > run.out
grep -q "^subject valley-tree-v1:$digest\$" run.out
grep -q '^check   ok  *command  passed$' run.out
grep -qE '^time    ok +passed +[0-9][0-9.hms]* \(ran\)$' run.out
grep -q "^signer  $host+" run.out
# A timing is printed, and logged only when --timings asks.
if [ -e "$HOME/.local/state/valley/timings.jsonl" ]; then
  echo "attest-e2e: a run without --timings wrote the timing log" >&2
  exit 1
fi

# Stored at a ref keyed by the subject digest, as one note
# rather than a statement and a signature beside it.
ref="$(refs)"
case "$ref" in
  refs/the-valley/attestations/"$digest"/*) ;;
  *) echo "attest-e2e: ref $ref is not keyed by the subject digest" >&2; exit 1 ;;
esac
git cat-file -p "$ref:ok/statement.note" > statement.note
head -n 1 statement.note | grep -qx 'the-valley/attestation/v1'
grep -qx "subject.digest.valley-tree-v1 $digest" statement.note
grep -qx 'provenance.harness valley-check' statement.note
grep -q "^— $host " statement.note

# No git object is signed: the signature is over the
# statement's text and nothing else.
if git cat-file commit HEAD | grep -q '^gpgsig'; then
  echo "attest-e2e: attest signed a git object" >&2
  exit 1
fi

verify() {
  attest verify --note "$1" --known-keys "$2" --repo "$repo"
}

verify statement.note "$TMPDIR/known_keys" > verify.out
grep -q "^signature ok      $host+" verify.out
grep -q 'matches the recorded subject digest' verify.out

# One atomic native-git push carries the branch and the
# attestation ref together.
git init --quiet --bare "$TMPDIR/host.git"
git remote add valley "$TMPDIR/host.git"
attest run --key "$TMPDIR/host" --name "$host" --command 'ok=true' \
  --push valley > push.out
grep -q -- '--atomic' push.out
git -C "$TMPDIR/host.git" for-each-ref --format='%(refname)' > published.txt
grep -qx 'refs/heads/main' published.txt
grep -qx "$ref" published.txt

# A second party attests to the same statement, and its
# signature lands beside the first under the text both cover.
# This is what the note format is here for.
attest sign --key "$TMPDIR/witness" --name "$witness" statement.note \
  > countersigned.note
if [ "$(grep -c '^— ' countersigned.note)" -ne 2 ]; then
  echo "attest-e2e: countersigning did not add a sibling signature line" >&2
  cat countersigned.note >&2
  exit 1
fi
sed '/^$/q' countersigned.note | sed '$d' | cmp - <(sed '/^$/q' statement.note | sed '$d')
attest verify --note countersigned.note --known-keys "$TMPDIR/known_keys" \
  --repo "$repo" --signer "$host" --signer "$witness" > countersigned.out
if [ "$(grep -c '^signature ok' countersigned.out)" -ne 2 ]; then
  echo "attest-e2e: both signatures over one statement must verify" >&2
  cat countersigned.out >&2
  exit 1
fi

# A verifier that does not hold the witness's key still
# accepts the note, and says which signature it cannot speak
# to. Requiring that signer is how it refuses instead.
attest key --key "$TMPDIR/host" --name "$host" > "$TMPDIR/host_only"
attest verify --note countersigned.note --known-keys "$TMPDIR/host_only" \
  --repo "$repo" > partial.out
grep -q "^signature ok      $host+" partial.out
grep -q "^signature unknown $witness+" partial.out
if attest verify --note countersigned.note --known-keys "$TMPDIR/host_only" \
  --repo "$repo" --signer "$witness" > required.out 2>&1; then
  echo "attest-e2e: a required signer with no verified signature must fail" >&2
  exit 1
fi
grep -q "no signature on this note is by $witness" required.out

# A note signed by a key the verifier does not hold is not a
# bad signature — it is not a signer.
if verify statement.note "$TMPDIR/other_keys" > stranger.out 2>&1; then
  echo "attest-e2e: a note signed by nobody we hold a key for must not verify" >&2
  exit 1
fi
grep -q 'no signature on this note is by a key the verifier holds' stranger.out

# Tampering with the signed bytes: one byte of the text the
# signature covers.
sed "s/$digest/00000000000000000000000000000000000000000000000000000000000000ff/" \
  statement.note > tampered.note
if verify tampered.note "$TMPDIR/known_keys" > tampered.out 2>&1; then
  echo "attest-e2e: a tampered statement must not verify" >&2
  exit 1
fi
grep -q 'does not check out over this text' tampered.out

# A signature cannot be lifted onto a statement it was not
# made about: it sits under the text it covers, so moving it
# means moving that text with it.
sed '/^$/q' statement.note | sed '$d' > lifted.txt
sed 's/^provenance.harness .*/provenance.harness somebody-else/' lifted.txt > lifted.body
sed -n '/^$/,$p' statement.note | tail -n +2 > lifted.sigs
{ cat lifted.body; echo; cat lifted.sigs; } > lifted.note
if verify lifted.note "$TMPDIR/known_keys" > lifted.out 2>&1; then
  echo "attest-e2e: a signature was lifted onto another statement" >&2
  exit 1
fi
grep -q 'does not check out over this text' lifted.out

# A statement written some other way than the one written
# form, signed properly over those bytes. The signature is
# good and the statement is still refused: it would verify
# here and fail wherever it was written out again, so the
# form is a rule rather than a convention.
{ head -n 1 lifted.txt; tail -n +2 lifted.txt | sort -r; } > loose.txt
cmp -s loose.txt lifted.txt \
  && { echo "attest-e2e: the reordering changed nothing" >&2; exit 1; }
attest sign --key "$TMPDIR/host" --name "$host" loose.txt > loose.note
if verify loose.note "$TMPDIR/known_keys" > loose.out 2>&1; then
  echo "attest-e2e: a statement not in the written form must not verify" >&2
  exit 1
fi
grep -q 'not the written form of what it says' loose.out

# The subject is the tree. Change the tree and the same
# signed statement stops being about it …
echo goodbye > hello.txt
git commit --quiet -am "change the tree"
if verify statement.note "$TMPDIR/known_keys" > moved.out 2>&1; then
  echo "attest-e2e: a statement about another tree must not verify" >&2
  exit 1
fi
grep -q 'tree digest mismatch' moved.out

# … while rewriting the commit over the same tree leaves the
# attestation standing, which is what a subject that is a
# tree digest buys.
git commit --quiet --amend -m "same tree, new commit"
rewritten="$(attest digest | sed 's/^valley-tree-v1://')"
git revert --quiet --no-edit HEAD
restored="$(attest digest | sed 's/^valley-tree-v1://')"
if [ "$restored" != "$digest" ]; then
  echo "attest-e2e: restoring the tree did not restore its digest" >&2
  echo "  original $digest" >&2
  echo "  restored $restored" >&2
  exit 1
fi
if [ "$rewritten" = "$digest" ]; then
  echo "attest-e2e: a changed tree kept its digest" >&2
  exit 1
fi
verify statement.note "$TMPDIR/known_keys" > rebased.out
grep -q 'matches the recorded subject digest' rebased.out

# A replacement ref in the repository changes nothing a standalone
# verify reads. refs/replace/<commit> makes git read another commit
# wherever it looks that one up, so a reader that left replacement
# refs on would digest the attested tree for a revision holding a
# different one, and pass it. The caller's environment asking for
# replacement refs is not listened to either.
attested="$(git rev-parse HEAD)"
echo "a change nobody attested" > evil.txt
git add evil.txt
git commit --quiet -m "a change nobody attested"
evil="$(git rev-parse HEAD)"
git update-ref "refs/replace/$evil" "$attested"
if [ "$(git rev-parse "$evil^{tree}")" != "$(git rev-parse "$attested^{tree}")" ]; then
  echo "attest-e2e: the replacement did not take, so this case proves nothing" >&2
  exit 1
fi
if GIT_CONFIG_PARAMETERS="'core.usereplacerefs'='true'" attest verify --note statement.note \
  --known-keys "$TMPDIR/known_keys" --repo "$repo" --rev "$evil" > replaced.out 2>&1; then
  echo "attest-e2e: a replacement ref made an unattested revision verify" >&2
  cat replaced.out >&2
  exit 1
fi
if grep -q 'matches the recorded subject digest' replaced.out; then
  echo "attest-e2e: the digest of a replaced revision matched the attested one" >&2
  exit 1
fi
[ "$(attest digest --rev "$evil" | sed 's/^valley-tree-v1://')" != "$digest" ] || {
  echo "attest-e2e: attest digested the replacement, not the revision" >&2
  exit 1
}
git update-ref -d "refs/replace/$evil"

# ----------------------------------------------------------------------
# Pure checks, through integrator-e2e's stand-in for nix, which logs every
# call here. One shared evaluation names every check, and each check is
# built by a nix build of its own, whose result is that check's. A check
# that declares a failure (checks/<name>.fails) fails with what it
# declares. Two checks can share an output, as fixed-output derivations
# can (checks/<name>.output): one whose own build failed stays failed when
# the other builds that output. A run with any check that failed or did
# not evaluate publishes nothing, neither here nor on the remote.
cp -r "$nixStandIn" "$TMPDIR/stand-ins"
chmod -R +w "$TMPDIR/stand-ins"
patchShebangs "$TMPDIR/stand-ins" > /dev/null
nixlog="$TMPDIR/nix.log"
mkdir -p "$TMPDIR/nix-bin"
for tool in nix nix-store; do
  cat > "$TMPDIR/nix-bin/$tool" <<EOF
#!$(command -v bash)
printf '%s %s\n' "$tool" "\$*" >> "$nixlog"
exec "$TMPDIR/stand-ins/$tool" "\$@"
EOF
  chmod +x "$TMPDIR/nix-bin/$tool"
done
export PATH="$TMPDIR/nix-bin:$PATH" VALLEY_FAKE_STORE="$TMPDIR/store"
export XDG_STATE_HOME="$TMPDIR/state"

pure="$TMPDIR/pure"
remote="$TMPDIR/pure-remote.git"
git init --quiet "$pure"
git init --quiet --bare "$remote"
cd "$pure" || exit 1
mkdir -p src checks
echo source > src/main.txt
for check in one two broken maker loser; do echo src > "checks/$check.inputs"; done
echo "the broken check says why" > checks/broken.fails
echo shared > checks/maker.output
echo shared > checks/loser.output
echo "the loser's own build failed" > checks/loser.fails
git add -A
git commit --quiet -m "pure checks"
nothing_on_remote() {
  if [ -n "$(git --git-dir="$remote" for-each-ref)" ]; then
    echo "attest-e2e: $1" >&2
    git --git-dir="$remote" for-each-ref >&2
    exit 1
  fi
}
pure_run() {
  : > "$nixlog"
  attest run --key "$TMPDIR/host" --name "$host" --push "$remote" --branch main "$@"
}

# Two checks pass and one fails: each keeps its own result, the failing
# one keeps its own log under its own name, and nothing is published.
if pure_run --timings --check one --check broken --check two > mixed.out 2> mixed.err; then
  echo "attest-e2e: a run with a failing pure check succeeded" >&2
  exit 1
fi
grep -qE '^check   one +nix +passed$' mixed.out
grep -qE '^check   broken +nix +failed$' mixed.out
grep -qE '^check   two +nix +passed$' mixed.out
[ "$(grep -c '^--- ' mixed.err)" = 1 ]
sed -n '/^--- broken ---$/,$p' mixed.err > broken.log
grep -qx 'the broken check says why' broken.log
grep -q '1 of 3 checks failed; nothing published' mixed.err
nothing_published "a run with a failing pure check stored a ref"
nothing_on_remote "a run with a failing pure check pushed"
# One evaluation named all three checks, and each had a build of its own.
[ "$(grep -c '^nix eval ' "$nixlog")" = 1 ]
grep '^nix eval --json --apply ' "$nixlog" > shared-eval.log
for check in one broken two; do
  grep -qF "\"$check\"" shared-eval.log
  [ "$(grep -cE "^nix build .*/$check-[A-Za-z0-9]{6}\.drv\^\*\$" "$nixlog")" = 1 ]
done
[ "$(grep -c '^nix build ' "$nixlog")" = 3 ]
# Each check is logged with its evaluation time.
[ "$(grep -c '"runner":"nix".*"eval_ms":' "$XDG_STATE_HOME/valley/timings.jsonl")" = 3 ]

# A check whose own build failed stays failed, though the other check that
# shares its output built that output.
if pure_run --check loser --check maker > shared.out 2> shared.err; then
  echo "attest-e2e: a check whose build failed passed on another's output" >&2
  exit 1
fi
grep -qE '^check   loser +nix +failed$' shared.out
grep -qE '^check   maker +nix +passed$' shared.out
[ -f "$VALLEY_FAKE_STORE/shared.out/result" ]
sed -n '/^--- loser ---$/,$p' shared.err > loser.log
grep -qx "the loser's own build failed" loser.log
grep -q '1 of 2 checks failed; nothing published' shared.err
nothing_published "a run with a failed build behind a shared output stored a ref"
nothing_on_remote "a run with a failed build behind a shared output pushed"

# A check that does not evaluate is an error for that check alone: the
# shared evaluation names it as nothing, it is evaluated alone for its own
# error, and the other check is still built and reported.
if pure_run --check one --check absent > absent.out 2> absent.err; then
  echo "attest-e2e: a run with a check that does not evaluate succeeded" >&2
  exit 1
fi
grep -qE '^check   one +nix +passed$' absent.out
grep -qE '^time    absent +error ' absent.out
grep -q 'declares no check absent' absent.err
grep -qE '^nix build .*/one-[A-Za-z0-9]{6}\.drv\^\*$' "$nixlog"
nothing_published "a run with a check that does not evaluate stored a ref"
nothing_on_remote "a run with a check that does not evaluate pushed"

# When every check passes, the run publishes the branch and one note per
# check, and each note verifies.
pure_run --check one --check two --check maker > passing.out 2> passing.err || {
  echo "attest-e2e: a run whose pure checks pass failed" >&2
  cat passing.out passing.err >&2
  exit 1
}
puredigest="$(attest digest | sed 's/^valley-tree-v1://')"
pureref="$(git --git-dir="$remote" for-each-ref --format='%(refname)' "refs/the-valley/attestations/$puredigest")"
[ -n "$pureref" ]
[ "$(git --git-dir="$remote" rev-parse refs/heads/main)" = "$(git rev-parse HEAD)" ]
for check in one two maker; do
  git --git-dir="$remote" cat-file blob "$pureref:$check/statement.note" > "$check.note"
  attest verify --note "$check.note" --known-keys "$TMPDIR/known_keys" --repo "$pure" > "$check.verify"
  grep -q 'matches the recorded subject digest' "$check.verify"
done
touch "$out"
