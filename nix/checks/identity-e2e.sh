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
    --grants "$TMPDIR/grants" --approval-signers "$TMPDIR/approval-signers" \
    --approval-signers-hardware "$TMPDIR/approval-signers-hardware" "$@"
}

# The worked registry compiles to exactly the artifacts checked in beside
# it. This is the whole claim of the compiler, and the artifacts it replaces
# were written by hand — so the comparison is byte for byte.
serve instance "$registry"
compile --now 2026-08-09 2> first.log || exit 1
diff -u "$compiled/known-signers" "$TMPDIR/signers" || exit 1
diff -u "$compiled/authorized_keys" "$TMPDIR/keys" || exit 1
diff -u "$compiled/grants" "$TMPDIR/grants" || exit 1
# The approval files too: every key of the one human holding the approve
# grant, and no hardware key, because no key in this registry carries an
# attestation.
diff -u "$compiled/approval-signers" "$TMPDIR/approval-signers" || exit 1
diff -u "$compiled/approval-signers-hardware" "$TMPDIR/approval-signers-hardware" || exit 1

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

# A push landing while the compiler reads cannot mix two registries. The
# compiler resolves the ref to a commit once and reads every document from
# that commit. Here a git standing in front of the real one moves main to a
# commit holding the forged registry the moment the compiler has resolved
# it. A compiler reading through the ref would compile the forged registry
# and name the first commit in its journal line. The compiler runs
# unwrapped, because its wrapper puts its own git first on PATH.
resolved="$(git -C instance.git rev-parse main)"
cp forged.cue work/identity/registry.cue
git -C work commit --quiet -am forged
moved="$(git -C work rev-parse HEAD)"
git -C work push --quiet "$TMPDIR/instance.git" "$moved:refs/heads/forged"
realgit="$(command -v git)"
mkdir -p racing
cat > racing/git <<RACING
#!/bin/sh
"$realgit" "\$@" || exit
case " \$* " in
*" rev-parse "*) "$realgit" -C "$TMPDIR/instance.git" update-ref refs/heads/main "$moved" ;;
esac
RACING
chmod +x racing/git
PATH="$TMPDIR/racing:$PATH" "$identityUnwrapped" compile --repo "$TMPDIR/instance.git" \
  --schema "$identitySchema" --known-signers "$TMPDIR/signers" \
  --authorized-keys "$TMPDIR/keys" --grants "$TMPDIR/grants" --now 2026-08-09 2> raced.log || {
  cat raced.log >&2
  exit 1
}
if [ "$(git -C instance.git rev-parse main)" != "$moved" ]; then
  echo "identity-e2e: main did not move mid-read, so the check below proves nothing" >&2
  exit 1
fi
grep -qF "at $resolved:" raced.log || {
  echo "identity-e2e: the compilation did not name the commit it resolved" >&2
  cat raced.log >&2
  exit 1
}
diff -u "$compiled/known-signers" "$TMPDIR/signers" || exit 1
diff -u "$compiled/authorized_keys" "$TMPDIR/keys" || exit 1
diff -u "$compiled/grants" "$TMPDIR/grants" || exit 1
# The next compilation resolves main where it now is, and compiles the
# forged registry: the commit the race moved main to compiles differently.
compile --now 2026-08-09 || exit 1
grep -q intruder "$TMPDIR/keys" || {
  echo "identity-e2e: the commit main moved to compiles the same, so the race check proves nothing" >&2
  exit 1
}
git -C instance.git update-ref refs/heads/main "$resolved"
compile --now 2026-08-09 || exit 1
diff -u "$compiled/authorized_keys" "$TMPDIR/keys" || exit 1

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
grep -q 'approve grant (also accepted as request)' refused.err
VALLEY_PRINCIPAL=patrick git -C work push --quiet "$TMPDIR/guarded.git" "main:$request"
git -C guarded.git rev-parse --verify --quiet "$request" > /dev/null || {
  echo "identity-e2e: the principal the registry grants request could not file one" >&2
  exit 1
}
VALLEY_PRINCIPAL=patrick git -C work push --quiet "$TMPDIR/guarded.git" --delete "$request"

# The approve grant goes by two names, approve and request, and a registry
# may use either. A registry whose hook boundary is of kind approve, and one
# granting patrick at a boundary of each kind, compile exactly the artifacts
# the worked registry does, with patrick written once.
mkdir -p approve-registry both-registry
sed 's/kind: "request"/kind: "approve"/' "$registry/registry.cue" > approve-registry/registry.cue
sed -e 's/^\t"classic-laddie-request": kind: "request"$/&\n\t"classic-laddie-approve": kind: "approve"/' \
  -e 's/^\t\t\trequest: boundary: "classic-laddie-request"$/&\n\t\t\tapprove: boundary: "classic-laddie-approve"/' \
  "$registry/registry.cue" > both-registry/registry.cue
if grep -q 'kind: "request"' approve-registry/registry.cue ||
  [ "$(grep -c 'classic-laddie-approve' both-registry/registry.cue)" != 2 ]; then
  echo "identity-e2e: the registries naming the grant approve were not written, so the checks below prove nothing" >&2
  exit 1
fi
# hook_as <principal> <git push arguments…>: a push through the rendered
# hook, as the principal.
hook_as() {
  local who="$1"
  shift
  VALLEY_PRINCIPAL="$who" git -C work push --quiet "$TMPDIR/guarded.git" "$@"
}
for variant in approve-registry both-registry; do
  serve instance "$variant"
  compile --now 2026-08-09 || exit 1
  diff -u "$compiled/known-signers" "$TMPDIR/signers" || exit 1
  diff -u "$compiled/authorized_keys" "$TMPDIR/keys" || exit 1
  diff -u "$compiled/grants" "$TMPDIR/grants" || exit 1
  # And through the hook: patrick files and deletes a request, and
  # stoned-flynn, holding push alone, does neither.
  if hook_as stoned-flynn "main:$request" 2> refused.err; then
    echo "identity-e2e: under $variant, a principal holding push alone filed a request" >&2
    exit 1
  fi
  grep -q 'approve grant (also accepted as request)' refused.err
  hook_as patrick "main:$request"
  if hook_as stoned-flynn --delete "$request" 2> refused.err; then
    echo "identity-e2e: under $variant, a principal holding push alone deleted a request" >&2
    exit 1
  fi
  hook_as patrick --delete "$request"
  if git -C guarded.git rev-parse --verify --quiet "$request" > /dev/null; then
    echo "identity-e2e: under $variant, the approve holder could not delete its request" >&2
    exit 1
  fi
done

# The hook reads a grants file naming the grant approve exactly as one
# naming it request. approver is named under approve, requester under
# request, both under each, and stoned-flynn under neither. A principal
# named approve is in the file too, holding only push, a grant the hook
# does not check: a principal's name is not a grant. Each holder files a
# request, replaces it with a head that does not descend from it, and
# deletes it. stoned-flynn and approve can do none of that in between.
cat > "$TMPDIR/grants" << 'EOF'
approve approver
request requester
approve both
request both
push approve
EOF
for holder in approver requester both; do
  hook_as "$holder" "main:$request"
  git -C work commit --quiet --allow-empty -m "replaced by $holder"
  replacement="$(git -C work rev-parse HEAD)"
  git -C work reset --quiet --hard HEAD~1
  hook_as "$holder" --force "$replacement:$request"
  [ "$(git -C guarded.git rev-parse "$request")" = "$replacement" ] || {
    echo "identity-e2e: $holder could not replace its request" >&2
    exit 1
  }
  for refused in "main:$request" "--delete $request"; do
    for lacking in stoned-flynn approve; do
      # shellcheck disable=SC2086  # the push arguments are two words, split on purpose
      if hook_as "$lacking" --force $refused 2> refused.err; then
        echo "identity-e2e: $lacking, holding neither name of the approve grant, wrote $holder's request" >&2
        exit 1
      fi
      grep -q 'approve grant (also accepted as request)' refused.err
    done
  done
  hook_as "$holder" --delete "$request"
  if git -C guarded.git rev-parse --verify --quiet "$request" > /dev/null; then
    echo "identity-e2e: $holder could not delete its request" >&2
    exit 1
  fi
done

# Change records, through the same hook and the same grants file. patrick
# owns the change and holds nothing in this file. A holder of the approve
# grant under either name, or both, may update another principal's change
# record, here by withdrawing the change, and may delete the record.
# stoned-flynn and approve may do neither, and each refusal leaves the
# record where it was. The record is created afresh for each holder.
owned_ref=refs/the-valley/changes/main/klmnopqrstuvwxyzklmnopqrstuvwxyz
# owned_record <state> <seq> <parent>: a record commit of patrick's change,
# at one version whose head is owned_head.
owned_record() {
  local blob tree
  blob="$(printf '{"schema": "valley-change-record/v1", "change_id": "%s", "target": "main", "owner": "patrick", "state": "%s", "seq": %s, "versions": [{"n": 1, "head": "git-sha1:%s", "title": "the change", "branch": "topic", "uploader": "patrick"}], "active": 1}\n' \
    "${owned_ref##*/}" "$1" "$2" "$owned_head" | git -C work hash-object -w --stdin)"
  tree="$(printf '100644 blob %s\trecord.json\n' "$blob" | git -C work mktree)"
  git -C work commit-tree "$tree" -p "$3" -m "a change record"
}
# owned_at <commit> <what>: the record is at the commit, or gone for "".
owned_at() {
  if [ "$(git -C guarded.git rev-parse --verify --quiet "$owned_ref")" != "$1" ]; then
    echo "identity-e2e: $2" >&2
    exit 1
  fi
}
# owned_refused <principal> <commit>: the principal may neither move the
# record to the commit nor delete it, for holding neither name of the
# approve grant.
owned_refused() {
  local who="$1" before
  before="$(git -C guarded.git rev-parse "$owned_ref")"
  if hook_as "$who" "$2:$owned_ref" 2> refused.err; then
    echo "identity-e2e: $who, holding neither name of the approve grant, updated another principal's change record" >&2
    exit 1
  fi
  grep -q "only the change's owner, patrick, or a principal holding the approve grant (also accepted as request) may update its record" refused.err
  if hook_as "$who" --delete "$owned_ref" 2> refused.err; then
    echo "identity-e2e: $who, holding neither name of the approve grant, deleted another principal's change record" >&2
    exit 1
  fi
  grep -q "only a principal holding the approve grant (also accepted as request) may delete a change record, and $who holds none" refused.err
  owned_at "$before" "a refused write by $who moved the change record"
}
# owned_commits: patrick's change, and the two records of it pushed below,
# written into work. filed creates the change, and retired withdraws it.
# serve replaces work, so they are written again after it.
owned_commits() {
  owned_head="$(git -C work commit-tree "main^{tree}" -p main -m "patrick's change")"
  filed="$(owned_record ready 1 "$owned_head")"
  retired="$(owned_record withdrawn 2 "$filed")"
}
owned_commits
for holder in approver requester both; do
  hook_as patrick "$filed:$owned_ref"
  owned_at "$filed" "patrick could not create a change record"
  owned_refused stoned-flynn "$retired"
  owned_refused approve "$retired"
  hook_as "$holder" "$retired:$owned_ref"
  owned_at "$retired" "$holder could not withdraw another principal's change"
  hook_as "$holder" --delete "$owned_ref"
  owned_at "" "$holder could not delete another principal's change record"
done

# A grant's name is not its kind. A registry granting stoned-flynn a grant
# named approve at a boundary of another kind compiles exactly the grants
# the worked registry does, so the hook refuses stoned-flynn the approve
# grant's writes.
for boundary in classic-laddie-push registry; do
  mkdir -p "mislabelled-$boundary"
  sed "s/^\t\tgrants: push: boundary: \"classic-laddie-push\"$/\t\tgrants: {push: boundary: \"classic-laddie-push\", approve: boundary: \"$boundary\"}/" \
    "$registry/registry.cue" > "mislabelled-$boundary/registry.cue"
  if ! grep -q "approve: boundary: \"$boundary\"" "mislabelled-$boundary/registry.cue"; then
    echo "identity-e2e: the registry granting approve at $boundary was not written, so the check below proves nothing" >&2
    exit 1
  fi
  serve instance "mislabelled-$boundary"
  compile --now 2026-08-09 || exit 1
  diff -u "$compiled/grants" "$TMPDIR/grants" || exit 1
  owned_commits
  if hook_as stoned-flynn "main:$request" 2> refused.err; then
    echo "identity-e2e: a grant named approve at $boundary let its holder file a request" >&2
    exit 1
  fi
  grep -q 'approve grant (also accepted as request)' refused.err
  hook_as patrick "$filed:$owned_ref"
  owned_refused stoned-flynn "$retired"
  hook_as patrick --delete "$owned_ref"
  owned_at "" "patrick could not delete its own change record, holding the approve grant"
done

# The worked registry again, for what follows.
serve instance "$registry"
compile --now 2026-08-09 || exit 1

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
cp "$TMPDIR/approval-signers" last-good-approval
cp "$TMPDIR/approval-signers-hardware" last-good-hardware
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
diff -u last-good-approval "$TMPDIR/approval-signers" || exit 1
diff -u last-good-hardware "$TMPDIR/approval-signers-hardware" || exit 1

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

# A hardware approval key, from a real YubiKey. The registry is written
# here from the test vectors in identity/testdata: the-valley operator's
# own approval key and the certificates its YubiKey wrote, which are the
# only chain at hand that verifies to Yubico Attestation Root 1. No
# example enrolls them. The registry enrolls that key under test-vector,
# beside the example laptop key. Both approve. Only the YubiKey's is a
# hardware approval key: its attestation verifies to the root the
# compiler carries, names the entry's public key, and says every
# signature needs a touch. The compiler reports what the attestation
# says, for whoever reviews the enrollment.
yubikey="$(cat "$testdata/valley-approve.pub")"
laptop="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDgoIskk25f2EYhLkPeJniiairVGv46CCYt2CUZ8EoxH"
# attested_registry <dir> <public> <slot pem> <f9 pem>: a registry whose
# one principal, test-vector, governs and approves with the attested key
# and the laptop key.
attested_registry() {
  mkdir -p "$1"
  {
    echo 'package identity'
    echo 'boundaries: {'
    echo '	"push": kind:     "git-push"'
    echo '	"registry": kind: "registry"'
    echo '	"approve": kind:  "approve"'
    echo '}'
    echo 'genesis: "test-vector"'
    echo 'principals: "test-vector": {'
    echo '	kind: "human"'
    echo '	keys: [{'
    echo '		class:  "ssh-ed25519"'
    echo '		bound:  "hardware"'
    printf '\t\tpublic: "%s"\n' "$2"
    echo '		attestation: {'
    echo '			slot: """'
    sed 's/^/\t\t\t\t/' "$3"
    echo '				"""'
    echo '			f9: """'
    sed 's/^/\t\t\t\t/' "$4"
    echo '				"""'
    echo '		}'
    echo '	}, {'
    echo '		class:  "ssh-ed25519"'
    echo '		bound:  "host"'
    printf '\t\tpublic: "%s"\n' "$laptop"
    echo '	}]'
    echo '	grants: {push: boundary: "push", approve: boundary: "approve", govern: boundary: "registry"}'
    echo '}'
  } > "$1/registry.cue"
}
attested_registry device-registry "$yubikey" "$testdata/valley-approve.attest.pem" "$testdata/yubico-f9.pem"
serve instance device-registry
compile --now 2026-10-22 2> attested.log || exit 1
diff -u "$testdata/compiled/approval-signers" "$TMPDIR/approval-signers" || exit 1
diff -u "$testdata/compiled/approval-signers-hardware" "$TMPDIR/approval-signers-hardware" || exit 1
grep -qxF "identity: test-vector: key 0 is on $(cat "$testdata/valley-approve.report"); it approves as a hardware key" attested.log || {
  echo "identity-e2e: the compiler did not report the attestation it verified" >&2
  cat attested.log >&2
  exit 1
}
cp "$TMPDIR/approval-signers" last-good-approval
cp "$TMPDIR/approval-signers-hardware" last-good-hardware

# Evidence that does not verify fails the compilation, and the last good
# approval files stand: the key is never compiled as an ordinary key
# instead. The cases:
# - wrong-key: the attestation offered for the laptop's key;
# - altered-slot: the slot certificate with one character of its
#   signature changed, the first of its last base64 line;
# - the example registry: its attestation chains to the test-only root
#   "the-valley example attestation root, not Yubico", so an entry copied
#   from it compiles only with the enrolling device's own certificates.
attested_registry wrong-key "$laptop" "$testdata/valley-approve.attest.pem" "$testdata/yubico-f9.pem"
last="$(($(grep -n -- '-----END' "$testdata/valley-approve.attest.pem" | cut -d: -f1) - 1))"
awk -v n="$last" 'NR == n { $0 = (substr($0, 1, 1) == "A" ? "B" : "A") substr($0, 2) } { print }' \
  "$testdata/valley-approve.attest.pem" > altered-slot.pem
if cmp -s altered-slot.pem "$testdata/valley-approve.attest.pem"; then
  echo "identity-e2e: the altered slot certificate was not written, so the check below proves nothing" >&2
  exit 1
fi
attested_registry altered-slot "$yubikey" altered-slot.pem "$testdata/yubico-f9.pem"
# The example's certificates are the ones the unit tests verify under the
# test-only root, so its refusal here is the root's alone.
for which in slot f9; do
  sed -n "/^\t\t\t\t\t$which: \"\"\"\$/,/\"\"\"\$/p" "$attested/registry.cue" | sed '1d;$d' | tr -d '\t' > "example-$which.pem"
  diff -u "$testdata/example-$which.pem" "example-$which.pem" || {
    echo "identity-e2e: the example's $which certificate is not the one the unit tests verify" >&2
    exit 1
  }
done
# refused <registry dir> <key> <reason>: the registry fails to compile,
# naming the key and the reason, and the last good approval files stand.
refused() {
  serve instance "$1"
  if compile --now 2026-10-22 2> refused-attestation.err; then
    echo "identity-e2e: a registry whose attestation does not verify ($1) compiled" >&2
    exit 1
  fi
  if ! grep -qF "$2: " refused-attestation.err || ! grep -qF "$3" refused-attestation.err; then
    echo "identity-e2e: the refusal of $1 did not name \"$2\" and say \"$3\"" >&2
    cat refused-attestation.err >&2
    exit 1
  fi
  diff -u last-good-approval "$TMPDIR/approval-signers" || exit 1
  diff -u last-good-hardware "$TMPDIR/approval-signers-hardware" || exit 1
}
refused wrong-key "test-vector: key 0" "attests a different key"
refused altered-slot "test-vector: key 0" "does not verify to Yubico Attestation Root 1"
refused "$attested" "example-operator: key 0" \
  "does not verify to Yubico Attestation Root 1: x509: certificate signed by unknown authority"

# The approval files are written only where the caller names them, so a
# host that does not ask for them gets neither.
rm "$TMPDIR/approval-signers" "$TMPDIR/approval-signers-hardware"
serve instance device-registry
identity compile --repo "$TMPDIR/instance.git" --known-signers "$TMPDIR/signers" \
  --authorized-keys "$TMPDIR/keys" --grants "$TMPDIR/grants" --now 2026-10-22 || exit 1
if [ -e "$TMPDIR/approval-signers" ] || [ -e "$TMPDIR/approval-signers-hardware" ]; then
  echo "identity-e2e: a compilation that named no approval file wrote one" >&2
  exit 1
fi

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

# The restack grant. A principal holding a grant at a boundary of kind
# restack is compiled into the grants as "restack <principal>", and the
# hook a host renders then lets it add a version to another principal's
# ready change and write that change's restack report. The same write from
# a principal the registry grants push alone is refused.
ssh-keygen -q -t ed25519 -N "" -C restacker -f "$TMPDIR/restacker"
mkdir -p restack-registry
{
  echo 'package identity'
  echo 'boundaries: {'
  echo '	"push": kind:     "git-push"'
  echo '	"registry": kind: "registry"'
  echo '	"restack": kind:  "restack"'
  echo '}'
  echo 'genesis: "author"'
  echo 'principals: "author": {'
  echo '	kind: "human"'
  echo '	keys: [{'
  echo '		class:  "ssh-ed25519"'
  echo '		bound:  "hardware"'
  printf '\t\tpublic: "%s"\n' "$(cut -d' ' -f1,2 < "$TMPDIR/victim.pub")"
  echo '	}]'
  echo '	grants: {push: boundary: "push", govern: boundary: "registry"}'
  echo '}'
  echo 'principals: "valley-restacker": {'
  echo '	kind: "service"'
  echo '	keys: [{'
  echo '		class:  "ssh-ed25519"'
  echo '		bound:  "host"'
  printf '\t\tpublic: "%s"\n' "$(cut -d' ' -f1,2 < "$TMPDIR/restacker.pub")"
  echo '	}]'
  echo '	grants: {push: boundary: "push", restack: boundary: "restack"}'
  echo '	expires: "2027-01-01"'
  echo '}'
} > restack-registry/registry.cue
serve instance restack-registry
compile --now 2026-08-09 || exit 1
if [ "$(grep -v '^#' "$TMPDIR/grants")" != "restack valley-restacker" ]; then
  echo "identity-e2e: the restack grant did not compile into the one line it holds" >&2
  cat "$TMPDIR/grants" >&2
  exit 1
fi

git init --quiet --bare restacked.git
ln -s "$TMPDIR/hook" restacked.git/hooks/pre-receive
git init --quiet rs
git -C rs commit --quiet --allow-empty -m "the change"
h1="$(git -C rs rev-parse HEAD)"
h2="$(git -C rs commit-tree "HEAD^{tree}" -p HEAD -m "the change, restacked")"
change_id=nxvpwlkmqtsrzyoukklnpmqrstuvwxyz
version() { printf '{"n": %s, "head": "git-sha1:%s", "title": "the change", "branch": "topic", "uploader": "%s"}' "$@"; }
# one_file <file> <json> <parent…>: a commit whose tree holds the JSON as
# the file.
one_file() {
  local file="$1" json="$2" blob tree parent
  local -a parents=()
  shift 2
  for parent in "$@"; do parents+=(-p "$parent"); done
  blob="$(printf '%s' "$json" | git -C rs hash-object -w --stdin)"
  tree="$(printf '100644 blob %s\t%s\n' "$blob" "$file" | git -C rs mktree)"
  git -C rs commit-tree "$tree" "${parents[@]}" -m "$file"
}
rpush() { VALLEY_PRINCIPAL="$1" git -C rs push --quiet "$TMPDIR/restacked.git" "$2"; }
record_ref="refs/the-valley/changes/main/$change_id"
created="$(one_file record.json "{\"schema\": \"valley-change-record/v1\", \"change_id\": \"$change_id\", \"target\": \"main\", \"owner\": \"author\", \"state\": \"ready\", \"seq\": 1, \"versions\": [$(version 1 "$h1" author)], \"active\": 1}" "$h1")"
rpush author "$created:$record_ref"
appended="$(one_file record.json "{\"schema\": \"valley-change-record/v1\", \"change_id\": \"$change_id\", \"target\": \"main\", \"owner\": \"author\", \"state\": \"ready\", \"seq\": 2, \"versions\": [$(version 1 "$h1" author), $(version 2 "$h2" valley-restacker)], \"active\": 2}" "$created" "$h2")"
if rpush stoned-flynn "$appended:$record_ref" 2> restack.err; then
  echo "identity-e2e: a principal the registry does not grant restack added a version to another's change" >&2
  exit 1
fi
grep -q "only the change's owner, author" restack.err
rpush valley-restacker "$appended:$record_ref"
[ "$(git -C restacked.git rev-parse "$record_ref")" = "$appended" ] || {
  echo "identity-e2e: the principal the registry grants restack could not add a version" >&2
  exit 1
}
report_ref="refs/the-valley/restack/main/$change_id"
report="$(one_file report.json "{\"schema\": \"valley-restack-report/v1\", \"change_id\": \"$change_id\", \"target\": \"main\", \"target_tip\": \"git-sha1:$h1\", \"operation\": \"op-1\", \"result\": \"complete\", \"layers\": [{\"change_id\": \"$change_id\", \"record\": \"git-sha1:$created\", \"version\": 1, \"head\": \"git-sha1:$h1\", \"branch\": \"topic\", \"branch_tip\": \"git-sha1:$h1\", \"published\": {\"head\": \"git-sha1:$h2\", \"version\": 2, \"record\": \"git-sha1:$appended\"}}], \"failing_layer\": null, \"conflict_paths\": [], \"blocking_requests\": [], \"refusal\": null}")"
if rpush author "$report:$report_ref" 2> report.err; then
  echo "identity-e2e: a principal the registry does not grant restack wrote a restack report" >&2
  exit 1
fi
grep -q 'restack grant' report.err
rpush valley-restacker "$report:$report_ref"
[ "$(git -C restacked.git rev-parse "$report_ref")" = "$report" ] || {
  echo "identity-e2e: the principal the registry grants restack could not write a restack report" >&2
  exit 1
}

touch "$out"
