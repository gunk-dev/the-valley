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
    --known-signers "$TMPDIR/signers" --authorized-keys "$TMPDIR/keys" "$@"
}

# The worked registry compiles to exactly the artifacts checked in beside
# it. This is the whole claim of the compiler, and the artifacts it replaces
# were written by hand — so the comparison is byte for byte.
serve instance "$registry"
compile --now 2026-08-09 2> first.log || exit 1
diff -u "$compiled/known-signers" "$TMPDIR/signers" || exit 1
diff -u "$compiled/authorized_keys" "$TMPDIR/keys" || exit 1

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
compile --now 2026-11-01 2> expired.log || exit 1
if grep -q "stoned-flynn" "$TMPDIR/signers" "$TMPDIR/keys"; then
  echo "identity-e2e: an expired entry stayed in the compiled artifacts" >&2
  cat "$TMPDIR/signers" "$TMPDIR/keys" >&2
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

touch "$out"
