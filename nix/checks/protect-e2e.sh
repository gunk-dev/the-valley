# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
export HOME="$TMPDIR"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name valley-check
git config --global user.email valley-check@localhost
git config --global init.defaultBranch main
git config --global advice.detachedHead false
cd "$TMPDIR" || exit 1

# A bare repo wired exactly as valley-init wires it, with
# the real store path followed from the rendered init script.
# Every project the host serves gets the hook, protected or not.
serve() {
  local hook
  hook="$(grep -o "/nix/store/[^ ]*-valley-protect-$1" "$initScriptPath" | head -n1)"
  test -x "$hook"
  git init --quiet --bare "$1.git"
  ln -s "$hook" "$1.git/hooks/pre-receive"
}
serve sealed
serve guarded
serve released
serve open

# Pushing as a principal is pushing with the tag on; pushing
# with an untagged key is pushing with it off. That sshd sets
# the tag from a key's entry, and nothing else does, is what
# ssh-e2e checks against a real sshd.
as() {
  local who="$1"
  shift
  if [ "$who" = anonymous ]; then
    env -u VALLEY_PRINCIPAL "$@"
  else
    VALLEY_PRINCIPAL="$who" "$@"
  fi
}
refs() { git -C "$TMPDIR/$1.git" for-each-ref --format='%(refname)'; }
has_ref() {
  if ! refs "$1" | grep -qx "$2"; then
    echo "protect-e2e: $1 has no $2" >&2
    refs "$1" >&2
    exit 1
  fi
}
lacks_ref() {
  if refs "$1" | grep -qx "$2"; then
    echo "protect-e2e: $1 kept $2 through a rejected push" >&2
    exit 1
  fi
}
# refused <file> <what> <command…> — the push must fail.
refused() {
  local err="$1" what="$2"
  shift 2
  if "$@" 2> "$err"; then
    echo "protect-e2e: $what" >&2
    exit 1
  fi
}

git init --quiet work
cd work || exit 1
git commit --quiet --allow-empty -m one
for name in sealed guarded released open; do
  git remote add "$name" "$TMPDIR/$name.git"
done

# ----------------------------------------------------------------------
# Protected refs.

# A non-writer cannot create a protected ref, and the
# rejection says who was pushing, what they were writing,
# and who may.
refused denied.err "a non-writer wrote a protected ref" \
  as contributor git push --quiet guarded main
grep -q 'contributor' denied.err
grep -q 'refs/heads/main' denied.err
grep -q 'writers: integrator' denied.err
lacks_ref guarded refs/heads/main

# An untagged key is nobody, and nobody is not a writer.
refused untagged.err "an untagged key wrote a protected ref" \
  as anonymous git push --quiet guarded main
grep -q '<untagged key>' untagged.err
lacks_ref guarded refs/heads/main

# Topic branches are open to the same non-writer, including a
# branch some other project's declaration protects.
git branch idea/one
git tag v1
git branch release/1.0
as contributor git push --quiet guarded idea/one release/1.0
has_ref guarded refs/heads/idea/one
has_ref guarded refs/heads/release/1.0

# The same ref is protected where a declaration says so: the
# glob in released's set matches it.
refused glob.err "a glob in the protected set did not match" \
  as contributor git push --quiet released release/1.0
grep -q 'refs/heads/release/1.0' glob.err
lacks_ref released refs/heads/release/1.0

# The writer creates it, and updates it.
as integrator git push --quiet guarded main
has_ref guarded refs/heads/main
git commit --quiet --allow-empty -m two
as integrator git push --quiet guarded main
second="$(git rev-parse HEAD)"
[ "$(git -C "$TMPDIR/guarded.git" rev-parse main)" = "$second" ]

# A delete is a write.
refused deleted.err "a non-writer deleted a protected ref" \
  as contributor git push --quiet --delete guarded main
grep -q 'refs/heads/main' deleted.err
has_ref guarded refs/heads/main

# pre-receive answers for the whole push: an open ref
# travelling with a protected one lands only if the
# protected one does, atomic push or not.
git commit --quiet --allow-empty -m three
git branch idea/two
for mode in --atomic --no-atomic; do
  refused mixed.err "a protected ref rode in on a $mode push" \
    as contributor git push --quiet "$mode" guarded idea/two main
  grep -q 'refs/heads/main' mixed.err
  lacks_ref guarded refs/heads/idea/two
  [ "$(git -C "$TMPDIR/guarded.git" rev-parse main)" = "$second" ]
done

# The norm, on a project that declares no writer at all: the
# protected ref takes no push from anyone. Even the principal
# that writes guarded's main is refused here, and told how a
# change lands instead of being told who may.
refused sealed.err "a protected ref with no declared writer took a push" \
  as integrator git push --quiet sealed main
grep -q 'integrator' sealed.err
grep -q 'no writer is declared' sealed.err
grep -q 'integration request' sealed.err
lacks_ref sealed refs/heads/main

# A project with no protection block protects nothing: its
# main is a branch like any other. Everything else the policy
# says still holds there, as the sections below show.
as contributor git push --quiet open main
has_ref open refs/heads/main

# ----------------------------------------------------------------------
# A symbolic ref takes no push. One cannot be pushed, but one
# can exist on the server, and git would write the ref it
# points at: here, a topic-branch name standing for the
# protected main. Its writer is refused too.
git -C "$TMPDIR/guarded.git" symbolic-ref refs/heads/alias refs/heads/main
for who in contributor integrator; do
  refused symref.err "$who wrote through a symbolic ref" \
    as "$who" git push --quiet --force guarded HEAD:refs/heads/alias
  grep -q 'symbolic ref' symref.err
  [ "$(git -C "$TMPDIR/guarded.git" rev-parse main)" = "$second" ]
done
git -C "$TMPDIR/guarded.git" symbolic-ref --delete refs/heads/alias

# ----------------------------------------------------------------------
# Namespaces no push may write, on a protected project and an
# unprotected one, by the protected ref's own writer.

# Tags open only through a named grant, and guarded has none.
refused tag.err "a tag was pushed that no grant opens" \
  as integrator git push --quiet guarded v1
grep -q 'refs/tags/ opens only through a named grant' tag.err
grep -q 'no grant of guarded' tag.err
lacks_ref guarded refs/tags/v1

# Notes, replacement refs and the valley's own namespace are
# closed outright, wherever they are pushed and by whom.
git notes add -m "a note" HEAD
git commit --quiet --allow-empty -m decoy
replaced="refs/replace/$(git rev-parse HEAD~1)"
git update-ref "$replaced" HEAD
outcome=refs/the-valley/integration-outcomes/main/idea
for project in guarded open; do
  for who in contributor integrator; do
    refused notes.err "$who pushed a notes ref to $project" \
      as "$who" git push --quiet "$project" refs/notes/commits
    grep -q 'refs/notes/ is closed to every push' notes.err
    refused replace.err "$who pushed a replacement ref to $project" \
      as "$who" git push --quiet "$project" "$replaced:$replaced"
    grep -q 'refs/replace/ is closed to every push' replace.err
    refused outcome.err "$who pushed an integration outcome to $project" \
      as "$who" git push --quiet "$project" "HEAD:$outcome"
    grep -q 'refs/the-valley/integration-outcomes/ is written only by the machinery' outcome.err
  done
  lacks_ref "$project" refs/notes/commits
  lacks_ref "$project" "$replaced"
  lacks_ref "$project" "$outcome"
done
git update-ref -d "$replaced"
git reset --quiet --hard HEAD~1

# A grant opens what it names to whom it names, and nothing
# more. released grants release tags to the integrator
# principal: that principal pushes one, nobody else does, and
# a tag outside the pattern stays closed to it too.
git tag release/1.0
tag=refs/tags/release/1.0
refused opened.err "a grant opened a ref to a principal it does not name" \
  as contributor git push --quiet released "$tag:$tag"
grep -q 'the grant release-tags of released opens this ref to integrator only' opened.err
lacks_ref released "$tag"
as integrator git push --quiet released "$tag:$tag"
has_ref released "$tag"
refused unopened.err "a grant opened a ref outside its pattern" \
  as integrator git push --quiet released v1
grep -q 'no grant of released' unopened.err
lacks_ref released refs/tags/v1

# ----------------------------------------------------------------------
# Integration requests take writes only from a holder of the
# request grant. Pushing topic branches does not make a
# principal one, and neither does being a declared writer.
request=refs/the-valley/integration-requests/main/idea
for who in contributor integrator anonymous; do
  refused request.err "$who filed a request without the request grant" \
    as "$who" git push --quiet guarded "idea/one:$request"
  grep -q 'refs/the-valley/integration-requests/' request.err
  grep -q 'request grant' request.err
  lacks_ref guarded "$request"
done

# The holder files one, replaces it with a head that does not
# descend from the first, and withdraws it. In between, a
# principal without the grant can neither replace nor
# withdraw what the holder filed.
git commit --quiet --allow-empty -m "first ask"
as requester git push --quiet guarded "HEAD:$request"
filed="$(git rev-parse HEAD)"
git reset --quiet --hard HEAD~1
git commit --quiet --allow-empty -m "second ask"
refused replaced.err "a principal without the grant replaced a request" \
  as contributor git push --quiet --force guarded "HEAD:$request"
grep -q 'request grant' replaced.err
refused withdrawn.err "a principal without the grant withdrew a request" \
  as contributor git push --quiet --delete guarded "$request"
grep -q 'request grant' withdrawn.err
[ "$(git -C "$TMPDIR/guarded.git" rev-parse "$request")" = "$filed" ]
as requester git push --quiet --force guarded "HEAD:$request"
[ "$(git -C "$TMPDIR/guarded.git" rev-parse "$request")" = "$(git rev-parse HEAD)" ]
as requester git push --quiet --delete guarded "$request"
lacks_ref guarded "$request"
git reset --quiet --hard HEAD~1

# Where protection covers the request namespace too, each rule
# still applies and neither stands in for the other: released's
# writer lacks the grant, and the holder of the grant is not
# released's writer.
refused overlap-writer.err "a writer filed a request without the grant" \
  as integrator git push --quiet released "idea/one:$request"
grep -q 'request grant' overlap-writer.err
refused overlap-holder.err "a request holder wrote a protected request ref" \
  as requester git push --quiet released "idea/one:$request"
grep -q 'a protected ref of released' overlap-holder.err
lacks_ref released "$request"

# The grant is the same on a project that declares no
# protection at all.
refused open-request.err "a request was filed on an unprotected project without the grant" \
  as contributor git push --quiet open "idea/one:$request"
as requester git push --quiet open "idea/one:$request"
has_ref open "$request"

# ----------------------------------------------------------------------
# Attestation refs: anyone may create one, if it is what its
# name says. This host names no verifier keys, so what a note
# claims about its signer is checked and its signature is not
# (identity-e2e checks the signature against compiled keys).
ssh-keygen -q -t ed25519 -N "" -C signer -f "$TMPDIR/signer"
ssh-keygen -q -t ed25519 -N "" -C other -f "$TMPDIR/other"
attest run --key "$TMPDIR/signer" --name signer.valley.invalid/attestations \
  --command ok=true > run.out
att="$(sed -n 's/^stored  \(refs[^ ]*\) -> .*/\1/p' run.out)"
digest="$(echo "$att" | cut -d/ -f4)"
keyhash="$(echo "$att" | cut -d/ -f5)"
as contributor git push --quiet guarded "$att:$att"
has_ref guarded "$att"
landed="$(git -C "$TMPDIR/guarded.git" rev-parse "$att")"

# … and nobody may rewrite or drop one, writer or not.
git update-ref "$att" "$(git rev-parse HEAD^{tree})"
refused rewritten.err "an attestation ref was rewritten" \
  as contributor git push --quiet --force guarded "$att:$att"
grep -q 'create-only' rewritten.err
grep -q "$att" rewritten.err
refused rewritten-by-writer.err "a writer rewrote an attestation ref" \
  as integrator git push --quiet --force guarded "$att:$att"
grep -q 'create-only' rewritten-by-writer.err
refused dropped.err "an attestation ref was deleted" \
  as integrator git push --quiet --delete guarded "$att"
grep -q 'create-only' dropped.err
[ "$(git -C "$TMPDIR/guarded.git" rev-parse "$att")" = "$landed" ]
git update-ref "$att" "$landed"

# Occupation: a name in a create-only namespace is held for
# good by whoever creates it first, so a name may only hold
# what it says. Each of these would keep the real signer's
# evidence out, and each is refused on a project that has
# none yet.
other_hash="$(attest key --key "$TMPDIR/other" --name other.valley.invalid/attestations | cut -d+ -f2)"
occupy() {
  local name="$1" target="$2" err="$3" why="$4"
  refused "$err" "an attestation ref took a name it does not hold: $why" \
    as contributor git push --quiet sealed "$target:refs/the-valley/attestations/$name"
  lacks_ref sealed "refs/the-valley/attestations/$name"
}
# A ref named for the digest alone would block every ref
# beneath it, and a name that is not hex is no signer's.
occupy "$digest" "$landed" shape.err "the digest alone"
grep -q '<digest>/<key hash>' shape.err
occupy "$digest/$keyhash/extra" "$landed" deep.err "a segment too many"
occupy "$digest/NOTAHASH" "$landed" nothex.err "a key hash that is not hex"
# The real signer's note, under another signer's key hash.
occupy "$digest/$other_hash" "$landed" squat.err "another signer's slot"
grep -q "no signature under the key hash $other_hash" squat.err
# The real signer's note, under another tree's digest.
elsewhere="$(printf '%064d' 7)"
occupy "$elsewhere/$keyhash" "$landed" subject.err "another tree's slot"
grep -q 'is about the tree' subject.err
# Something that is not a tree of notes at all.
occupy "$elsewhere/$keyhash" HEAD commit.err "a commit"
grep -q 'points at a commit' commit.err
# The ref the real evidence wants is still free, and takes it.
as anonymous git push --quiet sealed "$att:$att"
has_ref sealed "$att"

# ----------------------------------------------------------------------
# A hook a project composes after the policy runs once the
# policy accepts, and can only refuse more: released's refuses
# branches under frozen/.
git branch frozen/one
refused frozen.err "the composed hook did not run" \
  as contributor git push --quiet released frozen/one
grep -q 'released: refs/heads/frozen/one is frozen' frozen.err
lacks_ref released refs/heads/frozen/one
as contributor git push --quiet released idea/one
has_ref released refs/heads/idea/one

touch "$out"
