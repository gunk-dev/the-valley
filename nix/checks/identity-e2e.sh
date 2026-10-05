# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
export HOME="$TMPDIR"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name valley-check
git config --global user.email valley-check@localhost
git config --global init.defaultBranch main
cd "$TMPDIR" || exit 1

# A bare instance repository, holding one registry at its tip — what a host
# serves and what the compiler reads.
serve() {
  local name="$1" source="$2"
  rm -rf work "$name.git"
  git init --quiet --bare "$name.git"
  git init --quiet work
  mkdir -p work/identity
  cp "$source"/*.cue work/identity/
  chmod u+w work/identity/*.cue
  git -C work add -A
  git -C work commit --quiet -m "$name"
  git -C work push --quiet "$TMPDIR/$name.git" main
}

compile() {
  identity compile --repo "$TMPDIR/instance.git" \
    --known-signers "$TMPDIR/signers" --authorized-keys "$TMPDIR/keys" \
    --grants "$TMPDIR/grants" "$@"
}

# The worked registry compiles to exactly the artifacts checked in beside
# it. This is the whole claim of the compiler, and the artifacts it replaces
# were written by hand — so the comparison is byte for byte.
serve instance "$registry"
compile --now 2026-08-09 2> first.log || exit 1
diff -u "$compiled/known-signers" "$TMPDIR/signers" || exit 1
diff -u "$compiled/authorized_keys" "$TMPDIR/keys" || exit 1
diff -u "$compiled/grants" "$TMPDIR/grants" || exit 1

# A replacement ref planted in the instance repository changes nothing the
# compiler reads. refs/replace/<blob> makes git read another object wherever
# it looks that blob up, so a reader that leaves replacement refs on would
# compile a registry nobody landed — here, one granting a key push and
# request — while main stays exactly where it was.
ssh-keygen -q -t ed25519 -N "" -C intruder -f "$TMPDIR/intruder"
cp "$registry/registry.cue" forged.cue
chmod u+w forged.cue
{
  echo
  echo 'principals: "intruder": {'
  echo '	kind: "human"'
  echo '	keys: [{'
  echo '		class:  "ssh-ed25519"'
  echo '		bound:  "hardware"'
  printf '\t\tpublic: "%s"\n' "$(cut -d' ' -f1,2 < "$TMPDIR/intruder.pub")"
  echo '	}]'
  echo '	grants: {'
  echo '		push: boundary:    "classic-laddie-push"'
  echo '		request: boundary: "classic-laddie-request"'
  echo '	}'
  echo '}'
} >> forged.cue
landed="$(git -C instance.git rev-parse main:identity/registry.cue)"
forged="$(git -C instance.git hash-object -w "$TMPDIR/forged.cue")"
git -C instance.git update-ref "refs/replace/$landed" "$forged"
if ! git -C instance.git show main:identity/registry.cue | grep intruder > /dev/null; then
  echo "identity-e2e: the planted replacement did not take, so the check below proves nothing" >&2
  exit 1
fi
compile --now 2026-08-09 || exit 1
diff -u "$compiled/known-signers" "$TMPDIR/signers" || exit 1
diff -u "$compiled/authorized_keys" "$TMPDIR/keys" || exit 1
diff -u "$compiled/grants" "$TMPDIR/grants" || exit 1
if grep -q intruder "$TMPDIR/keys" "$TMPDIR/grants"; then
  echo "identity-e2e: a replacement ref put a principal nobody landed into the compiled artifacts" >&2
  exit 1
fi
# The same holds when the caller's environment asks for replacement refs.
GIT_CONFIG_PARAMETERS="'core.usereplacerefs'='true'" compile --now 2026-08-09 || exit 1
diff -u "$compiled/grants" "$TMPDIR/grants" || exit 1
git -C instance.git update-ref -d "refs/replace/$landed"

# What the hook does with the compiled grants. A bare repository is wired
# with the pre-receive hook a host compiling this registry renders,
# followed from its init script; the one edit is the directory of the
# compiled files, which a host keeps under /var/lib and this sandbox cannot.
# Pushing as a principal is pushing with the tag on, the substitution for
# sshd the hook checks make everywhere.
rendered="$(grep -o "/nix/store/[^ ]*-valley-protect-guarded" "$identityInitScriptPath" | head -n1)"
if ! grep -qF -- "--grants /var/lib/valley-identity/grants" "$rendered"; then
  echo "identity-e2e: the hook of a host compiling the registry does not read the compiled grants" >&2
  cat "$rendered" >&2
  exit 1
fi
sed -e "s|/var/lib/valley-identity/grants|$TMPDIR/grants|" \
  -e "s|/var/lib/valley-identity/known-signers|$TMPDIR/signers|" "$rendered" > hook
grep -qF -- "--known-signers $TMPDIR/signers" hook || {
  echo "identity-e2e: the hook of a host compiling the registry does not check attestations against the compiled keys" >&2
  exit 1
}
chmod +x hook
git init --quiet --bare guarded.git
ln -s "$TMPDIR/hook" guarded.git/hooks/pre-receive
request=refs/the-valley/integration-requests/main/topic
if VALLEY_PRINCIPAL=stoned-flynn git -C work push --quiet "$TMPDIR/guarded.git" "main:$request" 2> refused.err; then
  echo "identity-e2e: a principal the registry grants push and not request filed a request" >&2
  exit 1
fi
grep -q 'request grant' refused.err
VALLEY_PRINCIPAL=patrick git -C work push --quiet "$TMPDIR/guarded.git" "main:$request"
git -C guarded.git rev-parse --verify --quiet "$request" > /dev/null || {
  echo "identity-e2e: the principal the registry grants request could not file one" >&2
  exit 1
}

# Level-triggered: compiling again converges on the same bytes and touches
# nothing.
compile --now 2026-08-09 2> second.log || exit 1
if ! grep -q "unchanged" second.log; then
  echo "identity-e2e: a second compilation of an unchanged registry rewrote its artifacts" >&2
  cat second.log >&2
  exit 1
fi

# The registry is read from the integrated tip and from nowhere else, which
# is the whole of the forcing: an edit on a branch governs nothing.
git -C work checkout --quiet -b topic
cp "$expired"/*.cue work/identity/registry.cue
git -C work commit --quiet -am topic
git -C work push --quiet "$TMPDIR/instance.git" topic
compile --now 2026-08-09 || exit 1
diff -u "$compiled/known-signers" "$TMPDIR/signers" || exit 1

# The compiled verifier line is the one attest writes. The published keys
# above pin the format against a fixed vector; this pins it against the
# implementation that reads it, over a key generated here.
ssh-keygen -q -t ed25519 -N "" -C valley-check -f "$TMPDIR/scratch"
mkdir -p scratch-registry
{
  echo 'package identity'
  echo 'boundaries: "registry": kind: "registry"'
  echo 'genesis: "someone"'
  echo 'principals: "someone": {'
  echo '	kind: "human"'
  echo '	keys: [{'
  echo '		class:  "ssh-ed25519"'
  echo '		bound:  "hardware"'
  printf '\t\tpublic: "%s"\n' "$(cut -d' ' -f1,2 < "$TMPDIR/scratch.pub")"
  echo '		signs:  "someone/attestations"'
  echo '	}]'
  echo '	grants: govern: boundary: "registry"'
  echo '}'
} > scratch-registry/registry.cue
serve instance scratch-registry
compile --now 2026-08-09 || exit 1
attest key --key "$TMPDIR/scratch" --name "someone/attestations" > expected-line
if ! grep -qxFf expected-line "$TMPDIR/signers"; then
  echo "identity-e2e: the compiled verifier key is not the line attest writes" >&2
  cat expected-line "$TMPDIR/signers" >&2
  exit 1
fi

# An entry past its expiry leaves both artifacts, says so, and costs the
# rest of the registry nothing. The day before, it is still there — the
# declared day is the first day the entry no longer counts.
serve instance "$expired"
compile --now 2026-09-30 || exit 1
grep -q "stoned-flynn/attestations" "$TMPDIR/signers" || {
  echo "identity-e2e: an unexpired entry was omitted" >&2
  exit 1
}
grep -qx "request stoned-flynn" "$TMPDIR/grants" || {
  echo "identity-e2e: an unexpired holder of request was omitted from the grants" >&2
  exit 1
}
compile --now 2026-11-01 2> expired.log || exit 1
if grep -q "stoned-flynn" "$TMPDIR/signers" "$TMPDIR/keys" "$TMPDIR/grants"; then
  echo "identity-e2e: an expired entry stayed in the compiled artifacts" >&2
  cat "$TMPDIR/signers" "$TMPDIR/keys" "$TMPDIR/grants" >&2
  exit 1
fi
if ! grep -q "stoned-flynn expired 2026-10-01" expired.log; then
  echo "identity-e2e: an expired entry was dropped without saying so" >&2
  cat expired.log >&2
  exit 1
fi
grep -q "patrick" "$TMPDIR/keys" || {
  echo "identity-e2e: one expired entry took the rest of the registry with it" >&2
  exit 1
}

# The fail-safe. An invalid registry must cost the compilation and nothing
# else: the last good artifacts are what the gates keep reading, because a
# compiler bug that emptied them would lock the git user out of the host.
cp "$TMPDIR/signers" last-good-signers
cp "$TMPDIR/keys" last-good-keys
cp "$TMPDIR/grants" last-good-grants
mkdir -p invalid-registry
cp "$invalid" invalid-registry/registry.cue
serve instance invalid-registry
if compile --now 2026-08-09 2> refused.log; then
  echo "identity-e2e: an invalid registry compiled" >&2
  exit 1
fi
if ! grep -q "externalGovernance" refused.log; then
  echo "identity-e2e: the refusal did not name the field that failed" >&2
  cat refused.log >&2
  exit 1
fi
diff -u last-good-signers "$TMPDIR/signers" || exit 1
diff -u last-good-keys "$TMPDIR/keys" || exit 1
diff -u last-good-grants "$TMPDIR/grants" || exit 1

# Governance the clock orphans. This registry is valid as a document — the
# genesis entry governs — and the only entry governing carries an expiry, so
# whether it compiles depends on the day. The schema cannot see this; the
# compiler can, and refuses the whole render rather than write artifacts
# nobody could ever change again.
mkdir -p orphan-registry
{
  echo 'package identity'
  echo 'boundaries: {'
  echo '	"push": kind:     "git-push"'
  echo '	"registry": kind: "registry"'
  echo '}'
  echo 'genesis: "founder"'
  echo 'principals: {'
  echo '	"founder": {'
  echo '		kind: "human"'
  echo '		keys: [{'
  echo '			class:  "ssh-ed25519"'
  echo '			bound:  "hardware"'
  echo '			public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGCxVUxXoyFYV40QureqqSMSA17CvK9IrFB33BA6UOip"'
  echo '			signs:  "founder"'
  echo '		}]'
  echo '		grants: {'
  echo '			push: boundary:   "push"'
  echo '			govern: boundary: "registry"'
  echo '		}'
  echo '		expires: "2026-10-01"'
  echo '	}'
  echo '	"runner": {'
  echo '		kind: "machine"'
  echo '		keys: [{'
  echo '			class:  "ssh-ed25519"'
  echo '			bound:  "host"'
  echo '			public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAvpmbUKEDVsejgv2vxWaY/t4xl0JNnjFswb9SxcG9GG"'
  echo '		}]'
  echo '		grants: push: boundary: "push"'
  echo '		expires: "2027-01-01"'
  echo '	}'
  echo '}'
} > orphan-registry/registry.cue

# The day before: an ordinary compilation.
serve instance orphan-registry
compile --now 2026-09-30 || exit 1
grep -q "founder" "$TMPDIR/signers" || {
  echo "identity-e2e: the governing entry was omitted while still unexpired" >&2
  exit 1
}
cp "$TMPDIR/signers" last-good-signers
cp "$TMPDIR/keys" last-good-keys

# The day it expires, and a day well past it: the same document, refused. The
# entry still pushing does not save it — a grant at another boundary is not
# governance.
for pinned in 2026-10-01 2026-12-01; do
  if compile --now "$pinned" 2> orphaned.log; then
    echo "identity-e2e: a registry nobody governs on $pinned compiled" >&2
    exit 1
  fi
  if ! grep -q "governanceOrphaned" orphaned.log; then
    echo "identity-e2e: the refusal on $pinned did not name governanceOrphaned" >&2
    cat orphaned.log >&2
    exit 1
  fi
  diff -u last-good-signers "$TMPDIR/signers" || exit 1
  diff -u last-good-keys "$TMPDIR/keys" || exit 1
done

# A key the host declares by hand under one tag, and the registry
# authorizes as another principal, is refused: sshd reads the declared
# entry first, so the registry's principal would never be the one pushing.
serve instance "$registry"
compile --now 2026-08-09 || exit 1
cp "$TMPDIR/keys" last-good-keys
patrick_key="$(grep -m1 -o 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGCxVUxXoyFYV40QureqqSMSA17CvK9IrFB33BA6UOip' "$registry/registry.cue")"
for declared in "$patrick_key operator@laptop" "environment=\"VALLEY_PRINCIPAL=someone\" $patrick_key"; do
  printf '%s\n' "$declared" > declared-keys
  if compile --now 2026-08-09 --declared-keys declared-keys 2> declared.err; then
    echo "identity-e2e: a registry key declared by hand under another tag compiled" >&2
    exit 1
  fi
  grep -q 'declared on this host' declared.err || {
    echo "identity-e2e: the refusal did not name the declared entry" >&2
    cat declared.err >&2
    exit 1
  }
  diff -u last-good-keys "$TMPDIR/keys" || exit 1
done
printf 'environment="VALLEY_PRINCIPAL=patrick" %s\n' "$patrick_key" > declared-keys
compile --now 2026-08-09 --declared-keys declared-keys || exit 1

# Attestation names. Under a registry with two signers, the hook checks
# each pushed attestation's signature against the compiled keys, so a name
# in the create-only namespace can only be taken by the evidence it is
# named for. The pusher is never the question: evidence is relayed.
for who in victim attacker stranger; do
  ssh-keygen -q -t ed25519 -N "" -C "$who" -f "$TMPDIR/$who"
done
mkdir -p signers-registry
{
  echo 'package identity'
  echo 'boundaries: {'
  echo '	"push": kind:     "git-push"'
  echo '	"registry": kind: "registry"'
  echo '}'
  echo 'genesis: "victim"'
  for who in victim attacker; do
    printf 'principals: "%s": {\n' "$who"
    echo '	kind: "human"'
    echo '	keys: [{'
    echo '		class:  "ssh-ed25519"'
    echo '		bound:  "hardware"'
    printf '\t\tpublic: "%s"\n' "$(cut -d' ' -f1,2 < "$TMPDIR/$who.pub")"
    printf '\t\tsigns:  "%s/attestations"\n' "$who"
    echo '	}]'
    if [ "$who" = victim ]; then
      echo '	grants: {push: boundary: "push", govern: boundary: "registry"}'
    else
      echo '	grants: push: boundary: "push"'
    fi
    echo '}'
  done
} > signers-registry/registry.cue
serve instance signers-registry
compile --now 2026-08-09 || exit 1

git init --quiet --bare attested.git
ln -s "$TMPDIR/hook" attested.git/hooks/pre-receive
git init --quiet occ
git -C occ commit --quiet --allow-empty -m "a tree to attest"
attestation() {
  attest run --repo occ --key "$TMPDIR/$1" --name "$1/attestations" --command ok=true |
    sed -n 's/^stored  \(refs[^ ]*\) -> .*/\1/p'
}
mine="$(attestation attacker)"
theirs="$(attestation victim)"
unknown="$(attestation stranger)"
digest="$(echo "$mine" | cut -d/ -f4)"
victim_hash="$(echo "$theirs" | cut -d/ -f5)"
squat="refs/the-valley/attestations/$digest/$victim_hash"
push_as() { VALLEY_PRINCIPAL="$1" git -C occ push --quiet "$TMPDIR/attested.git" "$2"; }
must_refuse() {
  if push_as attacker "$1" 2> occupied.err; then
    echo "identity-e2e: $2 took an attestation name" >&2
    exit 1
  fi
  grep -qF -- "$3" occupied.err || {
    echo "identity-e2e: the refusal of $2 did not say \"$3\"" >&2
    cat occupied.err >&2
    exit 1
  }
}
# The attacker's own evidence, under its own name: accepted.
push_as attacker "$mine:$mine"
# The attacker's note, filed under the victim's key hash.
must_refuse "$mine:$squat" "the attacker's note under the victim's name" \
  "no signature under the key hash $victim_hash by a key this host accepts"
# A note whose signature line claims the victim's key and carries the
# attacker's signature.
git -C occ cat-file blob "$mine:ok/statement.note" > mine.note
sig="$(sed -n 's/^— attacker\/attestations //p' mine.note)"
forged_sig="$( { printf "$(echo "$victim_hash" | sed 's/../\\x&/g')"; printf '%s' "$sig" | base64 -d | tail -c 64; } | base64 -w0)"
sed "s|^— attacker/attestations .*|— victim/attestations $forged_sig|" mine.note > forged.note
blob="$(git -C occ hash-object -w "$TMPDIR/forged.note")"
sub="$(printf '100644 blob %s\tstatement.note\n' "$blob" | git -C occ mktree)"
top="$(printf '040000 tree %s\tok\n' "$sub" | git -C occ mktree)"
must_refuse "$top:$squat" "a forged signature" "does not check out"
# A key the registry does not hold, under its own name.
must_refuse "$unknown:$unknown" "an unknown signer's note" "does not open"
# The victim's genuine note, with a line appended that no verifier reads.
# Its signature still verifies, and the note is still one no verifier
# opens, so it may not take the victim's name.
git -C occ cat-file blob "$theirs:ok/statement.note" > theirs.note
{ cat theirs.note; printf '— x\t AAAAAAA=\n'; } > poisoned.note
blob="$(git -C occ hash-object -w "$TMPDIR/poisoned.note")"
sub="$(printf '100644 blob %s\tstatement.note\n' "$blob" | git -C occ mktree)"
top="$(printf '040000 tree %s\tok\n' "$sub" | git -C occ mktree)"
must_refuse "$top:$theirs" "a poisoned relay of the victim's note" "does not open"
# And the victim's own evidence, relayed by the attacker, takes the name
# that none of the above could.
push_as attacker "$theirs:$theirs"
git -C attested.git rev-parse --verify --quiet "$theirs" > /dev/null || {
  echo "identity-e2e: relayed evidence could not take its own name" >&2
  exit 1
}

touch "$out"
