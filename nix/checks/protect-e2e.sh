# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
export HOME="$TMPDIR"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name valley-check
git config --global user.email valley-check@localhost
git config --global init.defaultBranch main
cd "$TMPDIR" || exit 1

# A bare repo wired exactly as valley-init wires it, with
# the real store path followed from the rendered init script.
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

# Pushing as a principal is pushing with the tag on; pushing
# with an untagged key is pushing with it off.
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

git init --quiet work
cd work || exit 1
git commit --quiet --allow-empty -m one
git remote add sealed "$TMPDIR/sealed.git"
git remote add guarded "$TMPDIR/guarded.git"
git remote add released "$TMPDIR/released.git"

# A non-writer cannot create a protected ref, and the
# rejection says who was pushing, what they were writing,
# and who may.
if as contributor git push --quiet guarded main 2> denied.err; then
  echo "protect-e2e: a non-writer wrote a protected ref" >&2
  exit 1
fi
grep -q 'contributor' denied.err
grep -q 'refs/heads/main' denied.err
grep -q 'writers: integrator' denied.err
lacks_ref guarded refs/heads/main

# An untagged key is nobody, and nobody is not a writer.
if as anonymous git push --quiet guarded main 2> untagged.err; then
  echo "protect-e2e: an untagged key wrote a protected ref" >&2
  exit 1
fi
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

# Every other namespace is closed, and a refusal names the
# namespace and what would open it. Tags first: nothing in
# guarded's protection opens one.
if as contributor git push --quiet guarded v1 2> tag.err; then
  echo "protect-e2e: a tag was pushed that no allow entry opens" >&2
  exit 1
fi
grep -q 'refs/tags/ is closed to pushes' tag.err
grep -q 'no allow entry' tag.err
lacks_ref guarded refs/tags/v1

# Notes, and the valley's own namespaces that only the
# integrator writes on the host: an outcome is the
# controller's record of a verdict, and a pushed one would be
# a forged verdict.
git notes add -m "a note" HEAD
if as contributor git push --quiet guarded refs/notes/commits 2> notes.err; then
  echo "protect-e2e: a notes ref was pushed" >&2
  exit 1
fi
grep -q 'refs/notes/ is closed to pushes' notes.err
lacks_ref guarded refs/notes/commits
outcome=refs/the-valley/integration-outcomes/main/idea
if as contributor git push --quiet guarded "HEAD:$outcome" 2> outcome.err; then
  echo "protect-e2e: an integration outcome was pushed" >&2
  exit 1
fi
grep -q 'refs/the-valley/integration-outcomes/ is closed to pushes' outcome.err
lacks_ref guarded "$outcome"

# Replacement refs are closed to every push, a declared
# writer's included: one makes an object stand in for another
# wherever git looks it up, which would change what every
# reader of the repository sees.
git commit --quiet --allow-empty -m decoy
replaced="refs/replace/$(git rev-parse HEAD~1)"
git update-ref "$replaced" HEAD
for who in contributor integrator; do
  if as "$who" git push --quiet guarded "$replaced:$replaced" 2> replace.err; then
    echo "protect-e2e: $who pushed a replacement ref" >&2
    exit 1
  fi
  grep -q 'refs/replace/ is closed to every push' replace.err
  lacks_ref guarded "$replaced"
done
git update-ref -d "$replaced"
git reset --quiet --hard HEAD~1

# Integration requests take writes only from a holder of the
# request grant. Pushing to a topic branch does not make a
# principal one, and neither does being a declared writer.
request=refs/the-valley/integration-requests/main/idea
for who in contributor integrator anonymous; do
  if as "$who" git push --quiet guarded "idea/one:$request" 2> request.err; then
    echo "protect-e2e: $who filed a request without the request grant" >&2
    exit 1
  fi
  grep -q 'refs/the-valley/integration-requests/' request.err
  grep -q 'request grant' request.err
  lacks_ref guarded "$request"
done

# The holder files one, replaces it with a head that does not
# descend from the first, and withdraws it.
git commit --quiet --allow-empty -m "first ask"
as requester git push --quiet guarded "HEAD:$request"
has_ref guarded "$request"
git reset --quiet --hard HEAD~1
git commit --quiet --allow-empty -m "second ask"
as requester git push --quiet --force guarded "HEAD:$request"
[ "$(git -C "$TMPDIR/guarded.git" rev-parse "$request")" = "$(git rev-parse HEAD)" ]
as requester git push --quiet --delete guarded "$request"
lacks_ref guarded "$request"
git reset --quiet --hard HEAD~1

# The same ref is protected where a declaration says so: the
# glob in released's set matches it.
if as contributor git push --quiet released release/1.0 2> glob.err; then
  echo "protect-e2e: a glob in the protected set did not match" >&2
  exit 1
fi
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
if as contributor git push --quiet --delete guarded main 2> deleted.err; then
  echo "protect-e2e: a non-writer deleted a protected ref" >&2
  exit 1
fi
grep -q 'refs/heads/main' deleted.err
has_ref guarded refs/heads/main

# pre-receive answers for the whole push: an open ref
# travelling with a protected one lands only if the
# protected one does.
git commit --quiet --allow-empty -m three
git branch idea/two
if as contributor git push --quiet --atomic guarded idea/two main 2> atomic.err; then
  echo "protect-e2e: a protected ref rode in on an atomic push" >&2
  exit 1
fi
grep -q 'refs/heads/main' atomic.err
lacks_ref guarded refs/heads/idea/two
[ "$(git -C "$TMPDIR/guarded.git" rev-parse main)" = "$second" ]

# Attestation refs: anyone may create one …
att="refs/the-valley/attestations/$(printf '%064d' 0)/key0"
git update-ref "$att" HEAD~1
as contributor git push --quiet guarded "$att:$att"
has_ref guarded "$att"
landed="$(git -C "$TMPDIR/guarded.git" rev-parse "$att")"

# … and nobody may rewrite or drop one, writer or not.
git update-ref "$att" HEAD
if as contributor git push --quiet --force guarded "$att:$att" 2> rewritten.err; then
  echo "protect-e2e: an attestation ref was rewritten" >&2
  exit 1
fi
grep -q 'create-only' rewritten.err
grep -q "$att" rewritten.err
if as integrator git push --quiet --force guarded "$att:$att" 2> rewritten-by-writer.err; then
  echo "protect-e2e: a writer rewrote an attestation ref" >&2
  exit 1
fi
grep -q 'create-only' rewritten-by-writer.err
if as integrator git push --quiet --delete guarded "$att" 2> dropped.err; then
  echo "protect-e2e: an attestation ref was deleted" >&2
  exit 1
fi
grep -q 'create-only' dropped.err
[ "$(git -C "$TMPDIR/guarded.git" rev-parse "$att")" = "$landed" ]

# A second attestation beside it is still just a creation.
att2="refs/the-valley/attestations/$(printf '%064d' 0)/key1"
git update-ref "$att2" HEAD
as contributor git push --quiet guarded "$att2:$att2"
has_ref guarded "$att2"

# The norm, on a project that declares no writer at all: the
# protected ref takes no push from anyone. Even the principal
# that writes guarded's main is refused here, and told how a
# change lands instead of being told who may.
if as integrator git push --quiet sealed main 2> sealed.err; then
  echo "protect-e2e: a protected ref with no declared writer took a push" >&2
  exit 1
fi
grep -q 'integrator' sealed.err
grep -q 'no writer is declared' sealed.err
grep -q 'integration request' sealed.err
lacks_ref sealed refs/heads/main

# What the declaration does not name is decided there as
# anywhere: the wall closes one ref, not the project, and the
# namespaces the allowlist leaves out stay closed.
as contributor git push --quiet sealed idea/one
has_ref sealed refs/heads/idea/one
if as contributor git push --quiet sealed v1 2> sealed-tag.err; then
  echo "protect-e2e: a tag was pushed to a project that opens none" >&2
  exit 1
fi
lacks_ref sealed refs/tags/v1

# An allow entry opens what it names to whom it names, and
# nothing more. released opens release tags to the integrator
# principal: that principal pushes one, nobody else does, and
# a tag outside the pattern stays closed to it too.
git tag release/1.0
tag=refs/tags/release/1.0
if as contributor git push --quiet released "$tag:$tag" 2> opened.err; then
  echo "protect-e2e: an allow entry opened a ref to a principal it does not name" >&2
  exit 1
fi
grep -q 'opens this ref to integrator only' opened.err
lacks_ref released "$tag"
as integrator git push --quiet released "$tag:$tag"
has_ref released "$tag"
if as integrator git push --quiet released v1 2> unopened.err; then
  echo "protect-e2e: an allow entry opened a ref outside its pattern" >&2
  exit 1
fi
grep -q 'no allow entry' unopened.err
lacks_ref released refs/tags/v1

# And the attestation namespace is unchanged by any of it —
# create-only, for everyone, with or without a writers list.
as contributor git push --quiet sealed "$att:$att"
has_ref sealed "$att"
git update-ref "$att" HEAD~1
if as integrator git push --quiet --force sealed "$att:$att" 2> sealed-att.err; then
  echo "protect-e2e: an attestation ref was rewritten on a writer-less project" >&2
  exit 1
fi
grep -q 'create-only' sealed-att.err

touch "$out"
