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

# The keys this host accepts evidence from, as a controller would: the
# signer's, and nobody else's.
ssh-keygen -q -t ed25519 -N "" -C signer -f "$TMPDIR/signer"
ssh-keygen -q -t ed25519 -N "" -C other -f "$TMPDIR/other"
attest key --key "$TMPDIR/signer" --name signer.valley.invalid/attestations > "$TMPDIR/known_signers"

# A bare repo wired as valley-init wires it, with the hook followed from
# the rendered init script. Every project the host serves gets the hook,
# protected or not. The one edit is where the verifier keys are, which a
# host keeps under /var/lib and this sandbox cannot; open's points at no
# file at all, as a host that names no keys would.
serve() {
  local hook keys="$2"
  hook="$(grep -o "/nix/store/[^ ]*-valley-protect-$1" "$initScriptPath" | head -n1)"
  test -x "$hook"
  grep -qF -- '--known-signers /var/lib/valley-instance/known_signers' "$hook"
  git init --quiet --bare "$1.git"
  sed "s|/var/lib/valley-instance/known_signers|$keys|" "$hook" > "$1.git/hooks/pre-receive"
  chmod +x "$1.git/hooks/pre-receive"
}
serve sealed "$TMPDIR/known_signers"
serve guarded "$TMPDIR/known_signers"
serve released "$TMPDIR/known_signers"
serve open "$TMPDIR/no_such_keys"

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

# git also reads a loose ref holding "ref:" followed by a tab, or by
# nothing, as a symbolic ref, and a filesystem symlink too. Each is refused
# the same way, to main's own writer, who could otherwise write main
# through it.
heads="$TMPDIR/guarded.git/refs/heads"
mkdir -p "$heads"
printf 'ref:\trefs/heads/main\n' > "$heads/tabbed"
printf 'ref:refs/heads/main\n' > "$heads/tight"
ln -s refs/heads/main "$heads/linked"
for alias in tabbed tight linked; do
  refused symref.err "integrator wrote through the symbolic ref $alias" \
    as integrator git push --quiet --force guarded "HEAD:refs/heads/$alias"
  grep -q 'symbolic ref' symref.err
  [ "$(git -C "$TMPDIR/guarded.git" rev-parse main)" = "$second" ]
done
rm "$heads/tabbed" "$heads/tight" "$heads/linked"

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
git commit --quiet --allow-empty -m "first request"
as requester git push --quiet guarded "HEAD:$request"
filed="$(git rev-parse HEAD)"
git reset --quiet --hard HEAD~1
git commit --quiet --allow-empty -m "second request"
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
# A change record moves only by the rows of the hook's table
# (valleyhook/record.go), here the rows that change its state. Each is
# pushed as the change's owner or the holder of the request grant, and
# each refusal leaves the record where it was. A principal that is
# neither may not move it at all.
change_id=nxvpwlkmqtsrzyoukklnpmqrstuvwxyz
record_ref="refs/the-valley/changes/main/$change_id"
# record_json <state> <seq> <active> <head…>: the record of the change,
# owned by contributor, with one version per head.
record_json() {
  local state="$1" seq="$2" active="$3" head n=0 versions=""
  shift 3
  for head in "$@"; do
    n=$((n + 1))
    [ -z "$versions" ] || versions="$versions, "
    versions="$versions{\"n\": $n, \"head\": \"git-sha1:$head\", \"title\": \"v$n\", \"branch\": \"topic\", \"uploader\": \"contributor\"}"
  done
  printf '{"schema": "valley-change-record/v1", "change_id": "%s", "target": "main", "owner": "contributor", "state": "%s", "seq": %s, "versions": [%s], "active": %s}\n' \
    "$change_id" "$state" "$seq" "$versions" "$active"
}
# record_commit <json> <parent…>: a record commit holding the JSON.
record_commit() {
  local json="$1" blob tree parent
  local -a parents=()
  shift
  for parent in "$@"; do parents+=(-p "$parent"); done
  blob="$(printf '%s' "$json" | git hash-object -w --stdin)"
  tree="$(printf '100644 blob %s\trecord.json\n' "$blob" | git mktree)"
  git commit-tree "$tree" "${parents[@]}" -m 'a change record'
}
record_at() {
  if [ "$(git -C "$TMPDIR/open.git" rev-parse "$record_ref")" != "$1" ]; then
    echo "protect-e2e: the change record is not where $2 left it" >&2
    exit 1
  fi
}
h1="$(git commit-tree "HEAD^{tree}" -p HEAD -m 'the change, v1')"
h2="$(git commit-tree "HEAD^{tree}" -p HEAD -m 'the change, v2')"

created="$(record_commit "$(record_json ready 1 1 "$h1")" "$h1")"
as contributor git push --quiet open "$created:$record_ref"
record_at "$created" "the create"

# unready: ready to draft. A draft is not made a draft again.
drafted="$(record_commit "$(record_json draft 2 1 "$h1")" "$created")"
refused unready.err "a principal neither owner nor holder unreadied a change" \
  as integrator git push --quiet open "$drafted:$record_ref"
grep -q "only the change's owner, contributor" unready.err
as contributor git push --quiet open "$drafted:$record_ref"
record_at "$drafted" "unready"
again="$(record_commit "$(record_json draft 3 1 "$h1")" "$drafted")"
refused redraft.err "a draft was made a draft again" \
  as contributor git push --quiet open "$again:$record_ref"
grep -q 'the change is draft already' redraft.err
record_at "$drafted" "unready"

# re-ready at the same version, by the operator, and unready once more.
# It changes the state and the seq by one, and is the owner's or the
# operator's to make.
resumed="$(record_commit "$(record_json ready 3 1 "$h1")" "$drafted")"
refused reready.err "a principal neither owner nor holder readied a draft" \
  as integrator git push --quiet open "$resumed:$record_ref"
grep -q "only the change's owner, contributor" reready.err
stuck="$(record_commit "$(record_json ready 2 1 "$h1")" "$drafted")"
refused reready-seq.err "a draft was readied with its seq left as it was" \
  as contributor git push --quiet open "$stuck:$record_ref"
grep -q 'its seq is 2' reready-seq.err
record_at "$drafted" "unready"
as requester git push --quiet open "$resumed:$record_ref"
record_at "$resumed" "a re-ready at the same version"
paused="$(record_commit "$(record_json draft 4 1 "$h1")" "$resumed")"
as requester git push --quiet open "$paused:$record_ref"
record_at "$paused" "the operator's unready"

# re-ready at a new version, which needs a new head.
same="$(record_commit "$(record_json ready 5 2 "$h1" "$h1")" "$paused" "$h1")"
refused samehead.err "a draft was readied at a new version at its active head" \
  as contributor git push --quiet open "$same:$record_ref"
grep -q 'a new version needs a new head' samehead.err
record_at "$paused" "the operator's unready"
versioned="$(record_commit "$(record_json ready 5 2 "$h1" "$h2")" "$paused" "$h2")"
as contributor git push --quiet open "$versioned:$record_ref"
record_at "$versioned" "a re-ready at a new version"

# withdraw, from a draft. A withdrawn change never comes back as a draft,
# by its owner or by the operator.
redrafted="$(record_commit "$(record_json draft 6 2 "$h1" "$h2")" "$versioned")"
as contributor git push --quiet open "$redrafted:$record_ref"
gone="$(record_commit "$(record_json withdrawn 7 2 "$h1" "$h2")" "$redrafted")"
refused withdraw-draft.err "a principal neither owner nor holder withdrew a draft" \
  as integrator git push --quiet open "$gone:$record_ref"
grep -q "only the change's owner, contributor" withdraw-draft.err
record_at "$redrafted" "an unready of the new version"
as contributor git push --quiet open "$gone:$record_ref"
record_at "$gone" "a withdrawal from a draft"
back="$(record_commit "$(record_json draft 8 2 "$h1" "$h2")" "$gone")"
for who in contributor requester; do
  refused withdrawn-draft.err "$who made a withdrawn change a draft" \
    as "$who" git push --quiet open "$back:$record_ref"
  grep -q 'never as a draft' withdrawn-draft.err
  record_at "$gone" "a withdrawal from a draft"
done

# re-ready a withdrawn change at the version it was withdrawn at, which
# keeps the active version.
earlier="$(record_commit "$(record_json ready 8 1 "$h1" "$h2")" "$gone")"
refused revive-active.err "a withdrawn change was readied at another version" \
  as contributor git push --quiet open "$earlier:$record_ref"
grep -q 'changes active from 2 to 1' revive-active.err
record_at "$gone" "a withdrawal from a draft"
revived="$(record_commit "$(record_json ready 8 2 "$h1" "$h2")" "$gone")"
as contributor git push --quiet open "$revived:$record_ref"
record_at "$revived" "a re-ready from withdrawn at the same version"

# ----------------------------------------------------------------------
# The restack grant: the host restacker's one record write. restacker holds
# it alone, and requester holds it beside the request grant. Neither owns
# the change, which contributor does. A holder may add exactly one version
# to the change while it is ready, on the active version's branch and under
# its title, naming itself as the uploader. Every other write the grant
# would have to authorise is refused, and leaves the record where it was.
restack_id=pqrstuvwxyzklmnopqrstuvwxyzklmno
restack_ref="refs/the-valley/changes/main/$restack_id"
# restack_json <state> <seq> <active> <version…>: the record of the change,
# owned by contributor. Each version is "<head>|<uploader>|<title>|<branch>".
restack_json() {
  local state="$1" seq="$2" active="$3" spec head who title branch n=0 versions=""
  shift 3
  for spec in "$@"; do
    IFS='|' read -r head who title branch <<< "$spec"
    n=$((n + 1))
    [ -z "$versions" ] || versions="$versions, "
    versions="$versions{\"n\": $n, \"head\": \"git-sha1:$head\", \"title\": \"$title\", \"branch\": \"$branch\", \"uploader\": \"$who\"}"
  done
  printf '{"schema": "valley-change-record/v1", "change_id": "%s", "target": "main", "owner": "contributor", "state": "%s", "seq": %s, "versions": [%s], "active": %s}\n' \
    "$restack_id" "$state" "$seq" "$versions" "$active"
}
restack_at() {
  if [ "$(git -C "$TMPDIR/open.git" rev-parse "$restack_ref")" != "$1" ]; then
    echo "protect-e2e: the restacked change's record is not where $2 left it" >&2
    exit 1
  fi
}
# restack_refused <file> <what> <commit> [<who>]: a push of the record
# commit is refused, and the record stays where it was.
restack_refused() {
  local before
  before="$(git -C "$TMPDIR/open.git" rev-parse "$restack_ref")"
  refused "$1" "$2" as "${4:-restacker}" git push --quiet open "$3:$restack_ref"
  restack_at "$before" "a refused push: $2"
}
r1="$(git commit-tree "HEAD^{tree}" -p HEAD -m 'the restacked change, v1')"
r2="$(git commit-tree "HEAD^{tree}" -p HEAD -m 'the restacked change, rebased')"
r3="$(git commit-tree "HEAD^{tree}" -p HEAD -m 'the restacked change, rebased again')"
v1="$r1|contributor|the change|topic"
at_r2="$r2|restacker|the change|topic"

rcreated="$(record_commit "$(restack_json ready 1 1 "$v1")" "$r1")"
as contributor git push --quiet open "$rcreated:$restack_ref"
restack_at "$rcreated" "the create"

# A principal without the grant may not add the version, and neither may a
# key that names no principal.
appended="$(record_commit "$(restack_json ready 2 2 "$v1" "$at_r2")" "$rcreated" "$r2")"
for who in integrator anonymous; do
  restack_refused nogrant.err "$who added a version to another's change without the restack grant" "$appended" "$who"
done
grep -q 'a key that names no principal' nogrant.err
refused nogrant.err "integrator added a version without the grant" \
  as integrator git push --quiet open "$appended:$restack_ref"
grep -q "only the change's owner, contributor, or a principal holding the request grant" nogrant.err

# Each way to write more than the one version is refused.
two="$(record_commit "$(restack_json ready 2 3 "$v1" "$at_r2" "$r3|restacker|the change|topic")" "$rcreated" "$r3")"
restack_refused two.err "a holder added two versions" "$two"
grep -q 'a new version adds one version to the 1 before it, and this record holds 3' two.err
rewritten="$(record_commit "$(restack_json ready 2 2 "$r1|contributor|rewritten|topic" "$at_r2")" "$rcreated" "$r2")"
restack_refused rewritten.err "a holder changed an earlier version" "$rewritten"
grep -q 'it changes version 1' rewritten.err
owned="$(record_commit "$(restack_json ready 2 2 "$v1" "$at_r2" | sed 's/"owner": "contributor"/"owner": "restacker"/')" "$rcreated" "$r2")"
restack_refused owned.err "a holder changed the owner" "$owned"
grep -q 'changes the owner from contributor to restacker' owned.err
retargeted="$(record_commit "$(restack_json ready 2 2 "$v1" "$at_r2" | sed 's/"target": "main"/"target": "release"/')" "$rcreated" "$r2")"
restack_refused retargeted.err "a holder changed the target" "$retargeted"
grep -q 'names the target release' retargeted.err
retitled="$(record_commit "$(restack_json ready 2 2 "$v1" "$r2|restacker|another title|topic")" "$rcreated" "$r2")"
restack_refused retitled.err "a holder changed the title" "$retitled"
grep -q 'is titled "another title"' retitled.err
moved="$(record_commit "$(restack_json ready 2 2 "$v1" "$r2|restacker|the change|elsewhere")" "$rcreated" "$r2")"
restack_refused moved.err "a holder moved the change to another branch" "$moved"
grep -q 'names the branch "elsewhere"' moved.err
forged="$(record_commit "$(restack_json ready 2 2 "$v1" "$r2|contributor|the change|topic")" "$rcreated" "$r2")"
restack_refused forged.err "a holder forged the owner as the uploader" "$forged"
grep -q 'names contributor as its uploader, and restacker is pushing it' forged.err
for state in draft withdrawn; do
  stated="$(record_commit "$(restack_json "$state" 2 1 "$v1")" "$rcreated")"
  restack_refused stated.err "a holder moved a ready change to $state" "$stated"
  grep -q 'the restack grant changes no state' stated.err
done

# The one version is added.
as restacker git push --quiet open "$appended:$restack_ref"
restack_at "$appended" "a version added under the restack grant"

# Not to a draft, nor to a withdrawn change. The owner's moves between
# states are as they were.
paused="$(record_commit "$(restack_json draft 3 2 "$v1" "$at_r2")" "$appended")"
as contributor git push --quiet open "$paused:$restack_ref"
on_draft="$(record_commit "$(restack_json ready 4 3 "$v1" "$at_r2" "$r3|restacker|the change|topic")" "$paused" "$r3")"
restack_refused draft.err "a holder added a version to a draft" "$on_draft"
grep -q 'the change is draft, and the restack grant adds a version only to a change that is ready' draft.err
gone_r="$(record_commit "$(restack_json withdrawn 4 2 "$v1" "$at_r2")" "$paused")"
as contributor git push --quiet open "$gone_r:$restack_ref"
on_withdrawn="$(record_commit "$(restack_json ready 5 3 "$v1" "$at_r2" "$r3|restacker|the change|topic")" "$gone_r" "$r3")"
restack_refused withdrawn.err "a holder added a version to a withdrawn change" "$on_withdrawn"
grep -q 'the change is withdrawn, and the restack grant adds a version only to a change that is ready' withdrawn.err
back_r="$(record_commit "$(restack_json ready 5 2 "$v1" "$at_r2")" "$gone_r")"
as contributor git push --quiet open "$back_r:$restack_ref"
restack_at "$back_r" "the owner's re-ready"

# Deleting stays the request grant's alone.
refused rdelete.err "a holder of restack alone deleted a record" \
  as restacker git push --quiet --delete open "$restack_ref"
grep -q 'only a principal holding the request grant may delete' rdelete.err
restack_at "$back_r" "a refused delete"

# Holding restack beside request leaves request's powers as they were:
# requester unreadies the change, which restack alone may not.
held="$(record_commit "$(restack_json draft 6 2 "$v1" "$at_r2")" "$back_r")"
as requester git push --quiet open "$held:$restack_ref"
restack_at "$held" "an unready under the request grant"

# A holder keeps a named principal's own powers: it creates its own
# record, and unreadies it.
own_id=lmnopqrstuvwxyzklmnopqrstuvwxyzk
own_ref="refs/the-valley/changes/main/$own_id"
own_json() {
  printf '{"schema": "valley-change-record/v1", "change_id": "%s", "target": "main", "owner": "restacker", "state": "%s", "seq": %s, "versions": [{"n": 1, "head": "git-sha1:%s", "title": "its own", "branch": "own", "uploader": "restacker"}], "active": 1}\n' \
    "$own_id" "$1" "$2" "$r1"
}
own="$(record_commit "$(own_json ready 1)" "$r1")"
as restacker git push --quiet open "$own:$own_ref"
own_draft="$(record_commit "$(own_json draft 2)" "$own")"
as restacker git push --quiet open "$own_draft:$own_ref"
[ "$(git -C "$TMPDIR/open.git" rev-parse "$own_ref")" = "$own_draft" ]

# The grant opens nothing on integration requests: restacker files none,
# and neither replaces nor withdraws the one requester filed.
refused rrequest.err "a holder of restack alone filed a request" \
  as restacker git push --quiet open "$r2:refs/the-valley/integration-requests/main/c-$restack_id"
grep -q 'request grant' rrequest.err
lacks_ref open "refs/the-valley/integration-requests/main/c-$restack_id"
filed_request="$(git -C "$TMPDIR/open.git" rev-parse "$request")"
refused rreplace.err "a holder of restack alone replaced a request" \
  as restacker git push --quiet --force open "$r2:$request"
grep -q 'request grant' rreplace.err
refused rwithdraw.err "a holder of restack alone withdrew a request" \
  as restacker git push --quiet --delete open "$request"
grep -q 'request grant' rwithdraw.err
[ "$(git -C "$TMPDIR/open.git" rev-parse "$request")" = "$filed_request" ]

# ----------------------------------------------------------------------
# Restack reports, at refs/the-valley/restack/main/<change-id>: a holder of
# the restack grant creates one, and extends it with a report whose one
# parent is the one before. Nothing else writes one, and nothing replaces
# its history or deletes it.
report_ref="refs/the-valley/restack/main/$restack_id"
# report_json [<result>]: a report about the restacked change, observed at
# its record and head now.
report_json() {
  printf '{"schema": "valley-restack-report/v1", "change_id": "%s", "target": "main", "target_tip": "git-sha1:%s", "operation": "op-1", "result": "%s", "layers": [{"change_id": "%s", "record": "git-sha1:%s", "version": 2, "branch": "topic", "head": "git-sha1:%s", "published": null}], "failing_layer": "%s", "conflict_paths": ["readme.txt"], "blocking_requests": [], "refusal": null}\n' \
    "$restack_id" "$(git -C "$TMPDIR/open.git" rev-parse main)" "${1:-conflict}" "$restack_id" "$held" "$r2" "$restack_id"
}
# report_commit <json> [<file>] [<parent…>]: a commit holding the JSON as
# the one file given, report.json by default.
report_commit() {
  local json="$1" file="${2:-report.json}" blob tree parent
  local -a parents=()
  shift
  [ "$#" = 0 ] || shift
  for parent in "$@"; do parents+=(-p "$parent"); done
  blob="$(printf '%s' "$json" | git hash-object -w --stdin)"
  tree="$(printf '100644 blob %s\t%s\n' "$blob" "$file" | git mktree)"
  git commit-tree "$tree" "${parents[@]}" -m 'a restack report'
}
report_at() {
  if [ "$(git -C "$TMPDIR/open.git" rev-parse "$report_ref")" != "$1" ]; then
    echo "protect-e2e: the restack report is not where $2 left it" >&2
    exit 1
  fi
}
# report_refused <file> <what> <commit> [<who>] [<ref>]: the push is
# refused, and the report stays where it was.
report_refused() {
  local before
  before="$(git -C "$TMPDIR/open.git" rev-parse --verify --quiet "$report_ref" || true)"
  refused "$1" "$2" as "${4:-restacker}" git push --quiet --force open "$3:${5:-$report_ref}"
  if [ -n "$before" ]; then report_at "$before" "a refused push: $2"; else lacks_ref open "$report_ref"; fi
}
report="$(report_json)"

# Who.
first_report="$(report_commit "$report")"
for who in contributor integrator anonymous; do
  report_refused report-who.err "$who wrote a restack report without the grant" "$first_report" "$who"
  grep -q 'refs/the-valley/restack/ takes writes only from a principal holding the restack grant' report-who.err
done

# The ref: main alone, and a well-formed change ID that names a change the
# repository records.
report_refused report-target.err "a report for another target was written" "$first_report" restacker \
  "refs/the-valley/restack/release/$restack_id"
grep -q 'a restack report.s ref is refs/the-valley/restack/main/<change ID>' report-target.err
lacks_ref open "refs/the-valley/restack/release/$restack_id"
report_refused report-id.err "a report under a malformed change ID was written" "$first_report" restacker \
  "refs/the-valley/restack/main/${restack_id:0:31}"
grep -q '32 letters from k to z' report-id.err
unknown_id=zyxwvutsrqponmlkzyxwvutsrqponmlk
report_refused report-unknown.err "a report about a change nobody records was written" \
  "$(report_commit "${report//$restack_id/$unknown_id}")" restacker "refs/the-valley/restack/main/$unknown_id"
grep -q "holds no record of the change $unknown_id" report-unknown.err

# The first report has no parent.
report_refused report-parent.err "a first report with a parent was written" \
  "$(report_commit "$report" report.json "$r1")"
grep -q 'first restack report has no parent' report-parent.err
as restacker git push --quiet open "$first_report:$report_ref"
report_at "$first_report" "the first report"

# The report itself.
report_refused report-file.err "a report with a second file was written" \
  "$(git commit-tree "$(printf '100644 blob %s\treport.json\n100644 blob %s\textra\n' \
    "$(printf '%s' "$report" | git hash-object -w --stdin)" "$(printf x | git hash-object -w --stdin)" | git mktree)" \
    -p "$first_report" -m 'a restack report')"
grep -q "a restack report's tree holds report.json alone" report-file.err
report_refused report-name.err "a report under another name was written" \
  "$(report_commit "$report" record.json "$first_report")"
grep -q 'holds "record.json"' report-name.err
report_refused report-big.err "an oversized report was written" \
  "$(report_commit "$report$(head -c 65536 /dev/zero | tr '\0' ' ')" report.json "$first_report")"
grep -q 'more than the 65536' report-big.err
report_refused report-unknown-field.err "a report with an unknown field was written" \
  "$(report_commit "$(printf '%s' "$report" | sed 's/"refusal": null/"refusal": null, "note": "x"/')" report.json "$first_report")"
grep -q 'which the schema does not have' report-unknown-field.err
report_refused report-malformed.err "a report that is no JSON was written" \
  "$(report_commit "conflict in readme.txt" report.json "$first_report")"
grep -q 'its report.json does not read' report-malformed.err
report_refused report-other.err "a report about another change was written" \
  "$(report_commit "$(report_json | sed "s/\"change_id\": \"$restack_id\", \"target\"/\"change_id\": \"$own_id\", \"target\"/")" report.json "$first_report")"
grep -q "no layer names the change $own_id" report-other.err

# History: a later report extends the one before, and nothing replaces the
# history or deletes it.
report_refused report-restart.err "a report that starts the history over was written" \
  "$(report_commit "$(report_json blocked | sed 's/"failing_layer": "[a-z]*"/"failing_layer": null/')")"
grep -q 'as its one parent, so a push only adds to the history of reports' report-restart.err
report_refused report-elsewhere.err "a report on another parent was written" \
  "$(report_commit "$report" report.json "$r1")"
grep -q 'as its one parent' report-elsewhere.err
second_report="$(report_commit "$report" report.json "$first_report")"
as restacker git push --quiet open "$second_report:$report_ref"
report_at "$second_report" "the report that extends the first"
refused report-delete.err "a restack report was deleted" \
  as restacker git push --quiet --delete open "$report_ref"
grep -q 'no push deletes one' report-delete.err
report_at "$second_report" "a refused delete"

# A principal holding both grants writes a report too.
third_report="$(report_commit "$report" report.json "$second_report")"
as requester git push --quiet open "$third_report:$report_ref"
report_at "$third_report" "a report by a holder of both grants"

# ----------------------------------------------------------------------
# Attestation refs: anyone may create one, if it is what its
# name says, and it opens under the keys this host accepts.
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
# The real signer's note with a line appended that no verifier
# would read: the signature in it is still genuine, and the
# note is still unusable, so it may not take the name either.
git cat-file blob "$landed:ok/statement.note" > genuine.note
{ cat genuine.note; printf '— x\t AAAAAAA=\n'; } > poisoned.note
blob="$(git hash-object -w poisoned.note)"
sub="$(printf '100644 blob %s\tstatement.note\n' "$blob" | git mktree)"
poisoned="$(printf '040000 tree %s\tok\n' "$sub" | git mktree)"
occupy "$digest/$keyhash" "$poisoned" poisoned.err "a poisoned note"
grep -q 'does not open' poisoned.err
# A tree naming one subtree over and over, which describes more
# paths than any walk could visit, is refused at a bound.
wide="$sub"
for _ in 1 2; do
  wide="$(for i in $(seq -w 1 64); do printf '040000 tree %s\tt%s\n' "$wide" "$i"; done | git mktree)"
done
occupy "$digest/$keyhash" "$wide" wide.err "a tree of shared subtrees"
grep -q 'entries in all' wide.err
# The genuine note, at the root of a tree written by hand under the name
# ok/statement.note, a name with a slash in it, which git never writes and
# mktree refuses to. The tree is written byte by byte: the mode, the name,
# a NUL, and the note's object ID in binary.
flat_file="$TMPDIR/flat-tree"
printf '100644 %s\0' ok/statement.note > "$flat_file"
# shellcheck disable=SC2059 # the format is the object ID as \x escapes
printf "$(git rev-parse "$landed:ok/statement.note" | sed 's/../\\x&/g')" >> "$flat_file"
flat="$(git hash-object -t tree --literally -w "$flat_file")"
occupy "$digest/$keyhash" "$flat" flat.err "a note named with a slash"
grep -q 'it holds an entry named "ok/statement.note", and git writes no name with a slash' flat.err
# The ref the real evidence wants is still free, and takes it.
as anonymous git push --quiet sealed "$att:$att"
has_ref sealed "$att"

# A host that names no keys accepts no attestation at all.
refused nokeys.err "an attestation was accepted where no key is named" \
  as anonymous git push --quiet open "$att:$att"
grep -q 'names no keys it accepts evidence from' nokeys.err
lacks_ref open "$att"

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

# ----------------------------------------------------------------------
# A protected project's first main. No push may create it — sealed's main
# has refused every one above — and the integrator lands only onto a main
# that exists. So it is seeded on the host, by the git user, from a source
# whose commit the operator has checked: fetched, compared with the commit
# expected, and created only if no main exists yet. A local write is not a
# push, so no hook is asked.
expected="$(git rev-parse HEAD)"
git -C "$TMPDIR/sealed.git" fetch --quiet --no-tags "$TMPDIR/work" HEAD
[ "$(git -C "$TMPDIR/sealed.git" rev-parse FETCH_HEAD)" = "$expected" ]
git -C "$TMPDIR/sealed.git" update-ref refs/heads/main "$expected" ""
has_ref sealed refs/heads/main
# The empty old value makes the seed a creation: over a main that exists,
# it refuses.
if git -C "$TMPDIR/sealed.git" update-ref refs/heads/main "$expected~1" "" 2> /dev/null; then
  echo "protect-e2e: a seed overwrote a main that existed" >&2
  exit 1
fi
[ "$(git -C "$TMPDIR/sealed.git" rev-parse main)" = "$expected" ]

touch "$out"
