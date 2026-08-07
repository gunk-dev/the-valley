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

# Everything else is wide open to the same non-writer: topic
# branches, tags, and refs no declaration protects.
git branch idea/one
git tag v1
git branch release/1.0
as contributor git push --quiet guarded idea/one v1 release/1.0
has_ref guarded refs/heads/idea/one
has_ref guarded refs/tags/v1
has_ref guarded refs/heads/release/1.0

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

touch "$out"
