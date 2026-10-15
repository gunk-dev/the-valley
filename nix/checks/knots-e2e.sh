# shellcheck shell=bash
# The plan graph through the real host hook: bare repositories wired with
# the pre-receive hook the protected host's valley-init renders, written by
# the real valley knot as each principal, and pushed crafted commits that
# break each rule the hook holds a push to refs/the-valley/knots to. Every
# refused push must leave the ref where it was.
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
set -euo pipefail

w="$TMPDIR/knots-e2e"
mkdir -p "$w"
export HOME="$w"
export XDG_CACHE_HOME="$w/cache"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name knots-e2e
git config --global user.email knots-e2e@localhost
git config --global init.defaultBranch main
git config --global advice.detachedHead false

fail() {
  printf 'knots-e2e: %s\n' "$*" >&2
  if [ -s "$w/err" ]; then cat "$w/err" >&2; fi
  exit 1
}

# valley is bin/valley, run by bash, with the valley-go helper on PATH.
mkdir -p "$w/bin"
printf '#!%s\nexec %q %q "$@"\n' "$BASH" "$BASH" "$cliScript" > "$w/bin/valley"
chmod +x "$w/bin/valley"
export PATH="$w/bin:$PATH"

# A bare repo wired as valley-init wires it, with the hook followed from
# the rendered init script, as protect-e2e wires it. No verifier keys are
# named: no attestation is accepted here. git runs the hook through a
# wrapper that adds a line to <name>.runs for each run.
serve() {
  local hook
  hook="$(grep -o "/nix/store/[^ ]*-valley-protect-$1" "$initScriptPath" | head -n1)"
  test -x "$hook"
  git init --quiet --bare "$w/$1.git"
  sed "s|/var/lib/valley-instance/known_signers|$w/no_such_keys|" "$hook" > "$w/$1.git/hooks/valley-protect"
  chmod +x "$w/$1.git/hooks/valley-protect"
  printf '#!%s\necho run >> %q\nexec %q\n' "$BASH" "$w/$1.runs" "$w/$1.git/hooks/valley-protect" \
    > "$w/$1.git/hooks/pre-receive"
  chmod +x "$w/$1.git/hooks/pre-receive"
}
serve guarded
serve open

# Pushing as a principal is pushing with the tag on, as protect-e2e does.
as() {
  local who="$1"
  shift
  if [ "$who" = anonymous ]; then
    env -u VALLEY_PRINCIPAL "$@"
  else
    VALLEY_PRINCIPAL="$who" "$@"
  fi
}
tip() { git -C "$w/$1.git" rev-parse --verify --quiet refs/the-valley/knots || true; }

git init --quiet "$w/work"
cd "$w/work"
git remote add origin "$w/guarded.git"
git remote add open "$w/open.git"

# ----------------------------------------------------------------------
# The real CLI, as each principal: patrick creates the graph, klaus-env
# adds to it. The hook admits every write.
git config valley.principal patrick
as patrick valley knot new outcome "Slice one lands" > "$w/out" 2> "$w/err" || fail "patrick could not create the graph"
milestone="oc-$(printf '%s' slice-one-lands | sha256sum | cut -c1-7)"
created="$(tip guarded)"
[ -n "$created" ] || fail "the create did not land"
as patrick valley knot new bug "The hook reads too much" --parent "$milestone" > "$w/out" 2> "$w/err" \
  || fail "patrick could not update the graph"
bug="bd-$(printf '%s' the-hook-reads-too-much | sha256sum | cut -c1-7)"
as patrick valley knot comment "$bug" -m "Seen on a large graph." > "$w/out" 2> "$w/err" || fail "patrick could not comment"
git config valley.principal klaus-env
as klaus-env valley knot new outcome "Write the hook case" --parent "$milestone" --blocked-by "$bug" \
  > "$w/out" 2> "$w/err" || fail "klaus-env could not add a knot"
work="oc-$(printf '%s' write-the-hook-case | sha256sum | cut -c1-7)"
as klaus-env valley knot claim "$work" --run 20261015-0058-feb0a926 > "$w/out" 2> "$w/err" || fail "klaus-env could not claim"
as klaus-env valley knot set "$bug" status=closed > "$w/out" 2> "$w/err" || fail "klaus-env could not change patrick's knot"
as klaus-env valley knot comment "$work" -m "Claimed." > "$w/out" 2> "$w/err" || fail "klaus-env could not comment"
# A decision links the change that records it in the source.
as klaus-env valley knot new decision "One ref for the graph" > "$w/out" 2> "$w/err" || fail "klaus-env could not add a decision"
decision="dcr-$(printf '%s' one-ref-for-the-graph | sha256sum | cut -c1-7)"
as klaus-env valley knot link "$decision" --change valley:the-valley/kkkkllllmmmmnnnnooooppppqqqqrrrr > "$w/out" 2> "$w/err" \
  || fail "klaus-env could not link a change to a decision"
git -C "$w/guarded.git" cat-file blob "refs/the-valley/knots:decisions/$decision-one-ref-for-the-graph.md" \
  | grep -qx 'changes: \["valley:the-valley/kkkkllllmmmmnnnnooooppppqqqqrrrr"\]' || fail "the decision's change did not land"
[ "$(git -C "$w/guarded.git" log --format=%cn refs/the-valley/knots | sort -u | tr '\n' ' ')" = "klaus-env patrick " ] \
  || fail "the graph's commits are not both principals'"

# The CLI, written as a principal its key does not push as, is refused by
# the hook, and says what the hook said. A refusal is the hook's answer,
# not a race, so the CLI pushes once and does not try again.
git config valley.principal patrick
: > "$w/guarded.runs"
as klaus-env valley knot comment "$bug" -m "Forged." > "$w/out" 2> "$w/err" && fail "a write by a principal other than the pusher landed"
grep -q 'origin refused the write' "$w/err" || fail "the CLI did not report the hook's refusal"
grep -q 'patrick as its committer, and klaus-env is pushing it' "$w/err" || fail "the refusal does not name the committer"
[ "$(wc -l < "$w/guarded.runs")" -eq 1 ] || fail "a refused write ran the hook $(wc -l < "$w/guarded.runs") times, not once"

# ----------------------------------------------------------------------
# Crafted commits. Each is made on a scratch index from the current tip,
# with the edits given, by the committer given, and pushed as a principal.
git fetch --quiet origin refs/the-valley/knots
good="$(tip guarded)"
# blob writes a file's content, given as command substitution leaves it:
# without the one newline every canonical file ends with.
blob() { printf '%s\n' "$1" | git hash-object -w --stdin; }
# craft <parent|-> <committer> <message|-> <edit…>, where each edit is
# +PATH=CONTENT, -PATH, or MODE:PATH=OID.
craft() {
  local parent="$1" committer="$2" message="$3" edit tree
  shift 3
  export GIT_INDEX_FILE="$w/scratch.index"
  rm -f "$GIT_INDEX_FILE"
  if [ "$parent" = - ]; then git read-tree --empty; else git read-tree "$parent"; fi
  for edit in "$@"; do
    case "$edit" in
      +*) git update-index --add --cacheinfo "100644,$(blob "${edit#*=}"),$(p="${edit#+}" && echo "${p%%=*}")" ;;
      -*) git update-index --force-remove -- "${edit#-}" ;;
      *:*) git update-index --add --cacheinfo "${edit%%:*},${edit#*=},$(p="${edit#*:}" && echo "${p%%=*}")" ;;
    esac
  done
  tree="$(git write-tree)"
  unset GIT_INDEX_FILE
  if [ "$message" = - ]; then message="$(printf 'crafted\n\nValley-Op: %016x' "$RANDOM$RANDOM")"; fi
  local parents=()
  if [ "$parent" != - ]; then parents=(-p "$parent"); fi
  GIT_COMMITTER_NAME="$committer" git commit-tree "$tree" "${parents[@]}" -m "$message"
}
# refused <what> <ERE> <who> <git push args…>: the push fails, saying
# something matching the pattern, and the ref stays where it was.
refused() {
  local what="$1" says="$2" who="$3" before
  shift 3
  before="$(tip guarded)"
  if as "$who" git push --quiet "$@" 2> "$w/err"; then fail "$what was admitted"; fi
  grep -qE -- "$says" "$w/err" || fail "$what was refused without saying $says"
  [ "$(tip guarded)" = "$before" ] || fail "$what moved the ref"
}
push_graph() { echo "$1:refs/the-valley/knots"; }

knot_path() { git ls-tree -r --name-only "$good" | grep -m1 -- "/$1-"; }
bug_path="$(knot_path "$bug")"
work_path="$(knot_path "$work")"
comment_path="$(git ls-tree -r --name-only "$good" -- "comments/$bug/" | head -n1)"
bug_text="$(git cat-file blob "$good:$bug_path")"
work_text="$(git cat-file blob "$good:$work_path")"
stamp="20261015T120000Z"
comment() { printf -- '---\nauthor: "%s"\n---\n\n%s\n' "$1" "$2"; }
idea() { # idea <title> <slug> <creator> [body]
  local id
  id="ida-$(printf '%s' "$2" | sha256sum | cut -c1-7)"
  printf -- '---\ntype: idea\nid: %s\nstatus: raw\ntitle: "%s"\ncreated: 2026-10-15\ncreated_by: "%s"\n---\n' "$id" "$1" "$3"
  if [ -n "${4:-}" ]; then printf '\n%s\n' "$4"; fi
}
idea_path() { echo "ideas/ida-$(printf '%s' "$1" | sha256sum | cut -c1-7)-$1.md"; }

# A well-formed next commit is admitted, so every refusal below is the
# hook's answer to the one thing each breaks.
next="$(craft "$good" patrick - "+$(idea_path a-crafted-idea)=$(idea "A crafted idea" a-crafted-idea patrick)")"
cp /dev/null "$w/err"

# Who: an untagged key is no principal.
refused "an untagged key's write" 'untagged key.*named principal' anonymous origin "$(push_graph "$next")"

# Shape: one commit whose only parent is the old tip.
older="$(git rev-parse "$good~1")"
rewind="$(craft "$older" patrick - "+$(idea_path rewound)=$(idea Rewound rewound patrick)")"
refused "a non-fast-forward update" 'only parent is the tip it replaces' patrick --force origin "$(push_graph "$rewind")"
side="$(craft "$older" patrick - "+$(idea_path side)=$(idea Side side patrick)")"
merge="$(GIT_COMMITTER_NAME=patrick git commit-tree "$next^{tree}" -p "$good" -p "$side" -m "$(printf 'merge\n\nValley-Op: 0123456789abcdef')")"
refused "a merge" 'only parent is the tip it replaces' patrick origin "$(push_graph "$merge")"
two="$(craft "$next" patrick - "+$(idea_path second)=$(idea Second second patrick)")"
refused "two commits at once" 'only parent is the tip it replaces' patrick origin "$(push_graph "$two")"
git commit --quiet --allow-empty -m unrelated
stranger="$(craft "$(git rev-parse HEAD)" patrick - "+$(idea_path stranger)=$(idea Stranger stranger patrick)")"
refused "a parent that is not the old tip" 'only parent is the tip it replaces' patrick --force origin "$(push_graph "$stranger")"
tagged="$(printf 'object %s\ntype commit\ntag t\ntagger t <t> 0 +0000\n\nt\n' "$next" | git mktag)"
refused "a tag object as the tip" 'points at a tag' patrick origin "$tagged:refs/the-valley/knots"

# Every commit: committed by the pusher, and carrying its operation.
forged="$(craft "$good" klaus-env - "+$(idea_path forged)=$(idea Forged forged klaus-env)")"
refused "a commit by another committer" 'names klaus-env as its committer, and patrick is pushing it' patrick origin "$(push_graph "$forged")"
untrailed="$(craft "$good" patrick "no trailer" "+$(idea_path untrailed)=$(idea Untrailed untrailed patrick)")"
refused "a commit without its trailer" 'Valley-Op' patrick origin "$(push_graph "$untrailed")"

# Transitions: comments stay, knots stay, and what never changes does not.
refused "a deleted comment" 'a comment is never deleted' patrick origin "$(push_graph "$(craft "$good" patrick - "-$comment_path")")"
refused "a changed comment" 'stays exactly as it was written' patrick origin \
  "$(push_graph "$(craft "$good" patrick - "+$comment_path=$(comment patrick 'Rewritten.')")")"
refused "a deleted knot" 'a knot is never deleted' patrick origin "$(push_graph "$(craft "$good" patrick - "-$bug_path")")"
refused "a changed id" 'filename says id' patrick origin \
  "$(push_graph "$(craft "$good" patrick - "+$bug_path=${bug_text/id: $bug/id: bd-0000000}")")"
refused "a changed type" "id is \"$bug\", and an outcome's id is oc-" patrick origin \
  "$(push_graph "$(craft "$good" patrick - "+$bug_path=${bug_text/type: bug/type: outcome}")")"
refused "a changed created_by" 'changes created_by from "patrick" to "klaus-env"' klaus-env origin \
  "$(push_graph "$(craft "$good" klaus-env - "+$bug_path=${bug_text/created_by: \"patrick\"/created_by: \"klaus-env\"}")")"

# Provenance: a new comment's author and a new knot's creator are the
# pusher.
refused "a comment with a forged author" 'names klaus-env as its author, and patrick is pushing it' patrick origin \
  "$(push_graph "$(craft "$good" patrick - "+comments/$bug/$stamp-0123456789abcdef.md=$(comment klaus-env 'Not mine.')")")"
refused "a knot with a forged creator" 'names klaus-env in created_by, and patrick is pushing it' patrick origin \
  "$(push_graph "$(craft "$good" patrick - "+$(idea_path forged)=$(idea Forged forged klaus-env)")")"

# Layout: plain files, in the layout, within the bounds.
knot_blob="$(blob "$(idea Linked linked patrick)")"
refused "a symlink" 'mode 120000' patrick origin "$(push_graph "$(craft "$good" patrick - "120000:$(idea_path linked)=$knot_blob")")"
refused "an executable" 'mode 100755' patrick origin "$(push_graph "$(craft "$good" patrick - "100755:$(idea_path linked)=$knot_blob")")"
refused "a file outside the layout" 'outside the graph.s layout' patrick origin "$(push_graph "$(craft "$good" patrick - "+README.md=hello")")"
refused "a directory outside the layout" 'no directory a graph holds' patrick origin \
  "$(push_graph "$(craft "$good" patrick - "+tasks/$(basename "$(idea_path tasked)")=$(idea Tasked tasked patrick)")")"
refused "a non-canonical knot" 'not in canonical form' patrick origin \
  "$(push_graph "$(craft "$good" patrick - "+$(idea_path loose)=$(idea Loose loose patrick | sed 's/^title: "Loose"/title: Loose/')")")"
big="$(head -c 70000 /dev/zero | tr '\0' x)"
refused "an oversized knot" 'more than the 65536' patrick origin \
  "$(push_graph "$(craft "$good" patrick - "+$(idea_path big)=$(idea Big big patrick "$big")")")"

# A tree that names one subtree many times over is refused at a bound,
# before it is walked.
leaf_lines=""
for i in $(seq 1 64); do
  leaf_lines+="$(printf '100644 blob %s\t%s-%016x.md' "$(blob "$(comment patrick x)")" "$stamp" "$i")"$'\n'
done
leaf="$(printf '%s' "$leaf_lines" | git mktree)"
comments_tree="$(for i in $(seq 1 1000); do printf '040000 tree %s\toc-%07x\n' "$leaf" "$i"; done | git mktree)"
root="$(git ls-tree "$good" | grep -v $'\tcomments$' | { cat; printf '040000 tree %s\tcomments\n' "$comments_tree"; } | git mktree)"
bomb="$(GIT_COMMITTER_NAME=patrick git commit-tree "$root" -p "$good" -m "$(printf 'bomb\n\nValley-Op: 0123456789abcdef')")"
start="$(date +%s)"
refused "a shared-subtree bomb" 'entries in all' patrick origin "$(push_graph "$bomb")"
[ $(($(date +%s) - start)) -lt 10 ] || fail "the bomb took more than 10 seconds to refuse"

# Trees written by hand. A tree object can hold any bytes, and the host
# runs no fsck on what a push brings, so the hook refuses every entry git
# does not write. A name with a slash in it would be joined into a path
# that is no directory's, and a directory in the mode 040000, which git
# reads as a tree, would be a subtree the hook never walked. mktree and
# update-index refuse to write such trees, so raw_tree writes the entries
# it is given, each a mode, a name and an object, in the order given.
raw_tree() {
  local f="$w/raw-tree"
  : > "$f"
  while [ "$#" -gt 0 ]; do
    printf '%s %s\0' "$1" "$2" >> "$f"
    # shellcheck disable=SC2059 # the format is the object id as \x escapes
    printf "$(printf '%s' "$3" | sed 's/../\\x&/g')" >> "$f"
    shift 3
  done
  git hash-object -t tree --literally -w "$f"
}
# entries_of <tree> [-r]: the tree's entries, or with -r every file below
# it at its full path, as raw_tree takes them.
entries_of() {
  git ls-tree "${@:2}" "$1" | while IFS=$' \t' read -r mode _ oid name; do
    printf '%s\n%s\n%s\n' "${mode#0}" "$name" "$oid"
  done
}
on_good() { GIT_COMMITTER_NAME=patrick git commit-tree "$1" -p "$good" -m "$(printf 'raw\n\nValley-Op: %016x' "$RANDOM$RANDOM")"; }
mapfile -t root_entries < <(entries_of "$good")
mapfile -t flat_entries < <(entries_of "$good" -r)
named_idea="$(blob "$(idea Named named patrick)")"
refused "a root file named ideas/<id>-<slug>.md" 'named "ideas/ida-[0-9a-f]{7}-named\.md", and git writes no name with a slash' \
  patrick origin "$(push_graph "$(on_good "$(raw_tree "${root_entries[@]}" 100644 "$(idea_path named)" "$named_idea")")")"
refused "every knot and comment flattened into names with slashes" 'git writes no name with a slash' \
  patrick origin "$(push_graph "$(on_good "$(raw_tree "${flat_entries[@]}")")")"
# comments/, in the mode 040000, naming a subtree with a forged comment.
hidden="$(raw_tree 40000 "$bug" "$(raw_tree 100644 "$stamp-0123456789abcdef.md" "$(blob "$(comment klaus-env 'Never read.')")")")"
hiding=()
for ((i = 0; i < ${#root_entries[@]}; i += 3)); do
  if [ "${root_entries[i + 1]}" = comments ]; then
    hiding+=(040000 comments "$hidden")
  else
    hiding+=("${root_entries[@]:i:3}")
  fi
done
refused "a directory in the mode 040000" 'holds "comments" with mode 040000, and git writes only the modes' \
  patrick origin "$(push_graph "$(on_good "$(raw_tree "${hiding[@]}")")")"
for name in . ..; do
  refused "an entry named $name" "holds an entry named \"${name//./\\.}\", which git does not write" \
    patrick origin "$(push_graph "$(on_good "$(raw_tree "${root_entries[@]}" 100644 "$name" "$named_idea")")")"
done
# git push walks every tree it sends, and will not send one that holds an
# entry with an empty name. So the update is sent to receive-pack as any
# client could send it: the command, then a pack of exactly the objects
# named. git refuses the update itself, since its own walk cannot read the
# tree, and the hook, which git still runs, refuses it too.
nameless_tree="$(raw_tree "${root_entries[@]}" 100644 "" "$named_idea")"
nameless="$(on_good "$nameless_tree")"
update_line="$good $nameless refs/the-valley/knots"
capabilities=report-status
{
  printf '%04x%s\0%s\n' $((4 + ${#update_line} + 1 + ${#capabilities} + 1)) "$update_line" "$capabilities"
  printf 0000
  printf '%s\n' "$nameless" "$nameless_tree" "$named_idea" | git pack-objects --quiet --stdout
} > "$w/raw.request"
as patrick git receive-pack "$w/guarded.git" < "$w/raw.request" > "$w/raw.report" 2> "$w/err" || true
grep -q 'ng refs/the-valley/knots' "$w/raw.report" || fail "an entry with an empty name was admitted"
grep -q 'its tree: it holds an entry with an empty name' "$w/err" || fail "the hook did not refuse an entry with an empty name"
[ "$(tip guarded)" = "$good" ] || fail "an entry with an empty name moved the ref"

# Edges: every edge resolves, a parent is an outcome, and no edge forms a
# cycle. A cycle is crafted, since the CLI refuses to write one.
refused "a blocker that is no knot" 'is no knot in the graph' patrick origin \
  "$(push_graph "$(craft "$good" patrick - "+$work_path=${work_text/blocked_by: \[$bug\]/blocked_by: [$bug, oc-0000000]}")")"
loop_bug="${bug_text/blocked_by: \[\]/blocked_by: [$work]}"
[ "$loop_bug" != "$bug_text" ] || fail "the bug's blocked_by could not be edited"
refused "a cycle" 'blocked_by edges form a cycle' patrick origin "$(push_graph "$(craft "$good" patrick - "+$bug_path=$loop_bug")")"

# The ref: never deleted, and matched exactly.
refused "a deletion of the graph" 'never deleted' patrick origin --delete refs/the-valley/knots
refused "a ref that only begins with the graph's name" 'written only by the machinery' patrick origin "$next:refs/the-valley/knots-x"
refused "a ref below the graph's name" 'written only by the machinery' patrick origin "$next:refs/the-valley/knots/x"

# Create: one commit with no parent, on a project that has no graph yet.
first="$(craft - patrick - "+$(idea_path first)=$(idea First first patrick)")"
child="$(craft "$first" patrick - "+$(idea_path second)=$(idea Second second patrick)")"
if as patrick git push --quiet open "$(push_graph "$child")" 2> "$w/err"; then fail "a create with a parent was admitted"; fi
grep -q 'creates the graph has no parent' "$w/err" || fail "a create with a parent was refused for another reason"
[ -z "$(tip open)" ] || fail "a refused create made the ref"
as patrick git push --quiet open "$(push_graph "$first")" 2> "$w/err" || fail "a parentless create was refused"
as patrick git push --quiet open "$(push_graph "$child")" 2> "$w/err" || fail "the update after it was refused"
[ "$(tip open)" = "$child" ] || fail "the open project's graph is not at the update"

# A push that carries a valid graph update with an invalid write to
# another namespace is refused whole, atomic or not: a change record that
# is no record, an attestation no key here accepts, and an integration
# request from a principal without the request grant.
record="refs/the-valley/changes/main/$(printf 'k%.0s' $(seq 1 32))"
attestation="refs/the-valley/attestations/$(printf '%064d' 0)/00000000"
request="refs/the-valley/integration-requests/main/topic"
for other in "$next:$record" "$next:$attestation" "HEAD:$request"; do
  for mode in --atomic --no-atomic; do
    refused "a graph update beside $other ($mode)" 'refs/the-valley/(changes|attestations|integration-requests)/' \
      patrick "$mode" origin "$(push_graph "$next")" "$other"
    if git -C "$w/guarded.git" rev-parse --verify --quiet "${other#*:}" > /dev/null; then fail "$other landed"; fi
  done
done

# The well-formed commit itself is admitted.
as patrick git push --quiet origin "$(push_graph "$next")" 2> "$w/err" || fail "a well-formed update was refused"
[ "$(tip guarded)" = "$next" ] || fail "the well-formed update did not land"
touch "$out"
