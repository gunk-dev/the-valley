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
#
# The host declares approver and requester as holders of the approve
# grant, and its declared grants file names them under request, the name
# the grant had first. The hook reads one grants file more here, as a host
# with an identity registry does, and it names reviewer under approve.
printf 'approve reviewer\n' > "$w/grants"
serve() {
  local hook
  hook="$(grep -o "/nix/store/[^ ]*-valley-protect-$1" "$initScriptPath" | head -n1)"
  test -x "$hook"
  grep -q -- '--grants /nix/store/[^ ]*-valley-grants' "$hook"
  git init --quiet --bare "$w/$1.git"
  sed -e "s|/var/lib/valley-instance/known_signers|$w/no_such_keys|" -e "s|--grants |--grants $w/grants --grants |" \
    "$hook" > "$w/$1.git/hooks/valley-protect"
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
# The real CLI, as each principal: operator creates the graph, alice
# adds to it. The hook admits every write.
git config valley.principal operator
as operator valley knot new outcome "Slice one lands" > "$w/out" 2> "$w/err" || fail "operator could not create the graph"
milestone="oc-$(printf '%s' slice-one-lands | sha256sum | cut -c1-7)"
created="$(tip guarded)"
[ -n "$created" ] || fail "the create did not land"
as operator valley knot new bug "The hook reads too much" --parent "$milestone" > "$w/out" 2> "$w/err" \
  || fail "operator could not update the graph"
bug="bd-$(printf '%s' the-hook-reads-too-much | sha256sum | cut -c1-7)"
as operator valley knot comment "$bug" -m "Seen on a large graph." > "$w/out" 2> "$w/err" || fail "operator could not comment"
git config valley.principal alice
as alice valley knot new outcome "Write the hook case" --parent "$milestone" --blocked-by "$bug" \
  > "$w/out" 2> "$w/err" || fail "alice could not add a knot"
work="oc-$(printf '%s' write-the-hook-case | sha256sum | cut -c1-7)"
as alice valley knot claim "$work" --run 20261015-0058-feb0a926 > "$w/out" 2> "$w/err" || fail "alice could not claim"
as alice valley knot set "$bug" status=closed > "$w/out" 2> "$w/err" || fail "alice could not change operator's knot"
as alice valley knot comment "$work" -m "Claimed." > "$w/out" 2> "$w/err" || fail "alice could not comment"
# A question waits on its owner, and links the change that records its
# answer in the source.
as alice valley knot new question "Where does the graph live" --owner operator > "$w/out" 2> "$w/err" \
  || fail "alice could not add a question"
question="qn-$(printf '%s' where-does-the-graph-live | sha256sum | cut -c1-7)"
as alice valley knot set "$question" status=answered > "$w/out" 2> "$w/err" || fail "alice could not answer the question"
as alice valley knot link "$question" --change valley:the-valley/kkkkllllmmmmnnnnooooppppqqqqrrrr > "$w/out" 2> "$w/err" \
  || fail "alice could not link a change to a question"
git -C "$w/guarded.git" cat-file blob "refs/the-valley/knots:questions/$question-where-does-the-graph-live.md" > "$w/question"
grep -qx 'status: answered' "$w/question" || fail "the question's answer did not land"
grep -qx 'owner: "operator"' "$w/question" || fail "the question's owner did not land"
grep -qx 'changes: \["valley:the-valley/kkkkllllmmmmnnnnooooppppqqqqrrrr"\]' "$w/question" || fail "the question's change did not land"
[ "$(git -C "$w/guarded.git" log --format=%cn refs/the-valley/knots | sort -u | tr '\n' ' ')" = "alice operator " ] \
  || fail "the graph's commits are not both principals'"

# The CLI, written as a principal its key does not push as, is refused by
# the hook, and says what the hook said. A refusal is the hook's answer,
# not a race, so the CLI pushes once and does not try again.
git config valley.principal operator
: > "$w/guarded.runs"
as alice valley knot comment "$bug" -m "Forged." > "$w/out" 2> "$w/err" && fail "a write by a principal other than the pusher landed"
grep -q 'origin refused the write' "$w/err" || fail "the CLI did not report the hook's refusal"
grep -q 'operator as its committer, and alice is pushing it' "$w/err" || fail "the refusal does not name the committer"
[ "$(wc -l < "$w/guarded.runs")" -eq 1 ] || fail "a refused write ran the hook $(wc -l < "$w/guarded.runs") times, not once"

# A comment is edited in place, by the real CLI. Its author edits it, and
# so does a holder of the approve grant: approver holds it under request,
# and reviewer under approve. Each edit keeps the author and records the
# editor. The CLI forms an edit by anyone, and the hook decides who may
# make it, so a principal without the grant is refused by the hook.
# comment_text <id> <comment id> [--at <commit>]: the comment's text, as
# show prints it.
comment_text() {
  valley knot show "$1" --json "${@:3}" | jq -r ".knot.comments[] | select(.id == \"$2\") | .body"
}
git config valley.principal bob
as bob valley knot comment "$milestone" -m "First thought." > "$w/out" 2> "$w/err" || fail "bob could not comment"
edited="$(valley knot show "$milestone" --json | jq -r '.knot.comments[0].id')"
as bob valley knot comment "$milestone" --edit "$edited" -m "Second thought." > "$w/out" 2> "$w/err" \
  || fail "a comment's author could not edit it"
git config valley.principal approver
as approver valley knot comment "$milestone" --edit "$edited" -m "Moved to the bug." > "$w/out" 2> "$w/err" \
  || fail "a holder of the approve grant under request could not edit another's comment"
git config valley.principal reviewer
as reviewer valley knot comment "$milestone" --edit "$edited" -m "Removed." > "$w/out" 2> "$w/err" \
  || fail "a holder of the approve grant under approve could not edit another's comment"
git config valley.principal contributor
before="$(tip guarded)"
: > "$w/guarded.runs"
as contributor valley knot comment "$milestone" --edit "$edited" -m "Mine now." > "$w/out" 2> "$w/err" \
  && fail "an edit by a principal without the approve grant landed"
grep -q 'is a comment by bob, and contributor is editing it: a comment is edited by its author, or by a holder of the approve grant (also accepted as request)' "$w/err" \
  || fail "the hook's refusal of an edit does not say who may make it"
[ "$(tip guarded)" = "$before" ] || fail "a refused edit moved the ref"
[ "$(wc -l < "$w/guarded.runs")" -eq 1 ] || fail "a refused edit ran the hook $(wc -l < "$w/guarded.runs") times, not once"
valley knot show "$milestone" --json > "$w/out"
jq -e ".knot.comments == [{id: \"$edited\", author: \"bob\", at: .knot.comments[0].at, op: \"${edited#*-}\",
  edited: .knot.comments[0].edited, edited_by: \"reviewer\", body: \"Removed.\\n\"}]" "$w/out" > /dev/null \
  || fail "the edited comment does not keep its id and author and name its last editor"
[ "$(git -C "$w/guarded.git" ls-tree --name-only "refs/the-valley/knots:comments/$milestone/")" = "$edited.md" ] \
  || fail "the edits are not one comment's file"
# Every earlier text is still read from the ref: log lists each edit with
# its committer, and show --at reads the comment as each commit left it.
valley knot log "$milestone" --json > "$w/out"
[ "$(jq -r '[.commits[] | select(.files | any(.comment != null)) | .committer] | join(" ")' "$w/out")" = "reviewer approver bob bob" ] \
  || fail "log does not list the comment's writes, newest first, by their committers"
texts=""
for c in $(jq -r '.commits[] | select(.files | any(.comment != null)) | .commit' "$w/out"); do
  texts+="$(comment_text "$milestone" "$edited" --at "$c")|"
done
[ "$texts" = "Removed.|Moved to the bug.|Second thought.|First thought.|" ] || fail "show --at does not give back each earlier text: $texts"
git config valley.principal operator

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
# edit <author> <editor> <text> [edited]: a comment as an edit writes it.
edit() { printf -- '---\nauthor: "%s"\nedited: "%s"\nedited_by: "%s"\n---\n\n%s\n' "$1" "${4:-2026-10-25T15:00:00Z}" "$2" "$3"; }
idea() { # idea <title> <slug> <creator> [body]
  local id
  id="ida-$(printf '%s' "$2" | sha256sum | cut -c1-7)"
  printf -- '---\ntype: idea\nid: %s\nstatus: raw\ntitle: "%s"\ncreated: 2026-10-15\ncreated_by: "%s"\n---\n' "$id" "$1" "$3"
  if [ -n "${4:-}" ]; then printf '\n%s\n' "$4"; fi
}
idea_path() { echo "ideas/ida-$(printf '%s' "$1" | sha256sum | cut -c1-7)-$1.md"; }

# A well-formed next commit is admitted, so every refusal below is the
# hook's answer to the one thing each breaks.
next="$(craft "$good" operator - "+$(idea_path a-crafted-idea)=$(idea "A crafted idea" a-crafted-idea operator)")"
cp /dev/null "$w/err"

# Who: an untagged key is no principal.
refused "an untagged key's write" 'untagged key.*named principal' anonymous origin "$(push_graph "$next")"

# Shape: one commit whose only parent is the old tip.
older="$(git rev-parse "$good~1")"
rewind="$(craft "$older" operator - "+$(idea_path rewound)=$(idea Rewound rewound operator)")"
refused "a non-fast-forward update" 'only parent is the tip it replaces' operator --force origin "$(push_graph "$rewind")"
side="$(craft "$older" operator - "+$(idea_path side)=$(idea Side side operator)")"
merge="$(GIT_COMMITTER_NAME=operator git commit-tree "$next^{tree}" -p "$good" -p "$side" -m "$(printf 'merge\n\nValley-Op: 0123456789abcdef')")"
refused "a merge" 'only parent is the tip it replaces' operator origin "$(push_graph "$merge")"
two="$(craft "$next" operator - "+$(idea_path second)=$(idea Second second operator)")"
refused "two commits at once" 'only parent is the tip it replaces' operator origin "$(push_graph "$two")"
git commit --quiet --allow-empty -m unrelated
stranger="$(craft "$(git rev-parse HEAD)" operator - "+$(idea_path stranger)=$(idea Stranger stranger operator)")"
refused "a parent that is not the old tip" 'only parent is the tip it replaces' operator --force origin "$(push_graph "$stranger")"
tagged="$(printf 'object %s\ntype commit\ntag t\ntagger t <t> 0 +0000\n\nt\n' "$next" | git mktag)"
refused "a tag object as the tip" 'points at a tag' operator origin "$tagged:refs/the-valley/knots"

# Every commit: committed by the pusher, and carrying its operation.
forged="$(craft "$good" alice - "+$(idea_path forged)=$(idea Forged forged alice)")"
refused "a commit by another committer" 'names alice as its committer, and operator is pushing it' operator origin "$(push_graph "$forged")"
untrailed="$(craft "$good" operator "no trailer" "+$(idea_path untrailed)=$(idea Untrailed untrailed operator)")"
refused "a commit without its trailer" 'Valley-Op' operator origin "$(push_graph "$untrailed")"

# Transitions: comments stay, knots stay, and what never changes does not.
refused "a deleted comment" 'a comment is never deleted' operator origin "$(push_graph "$(craft "$good" operator - "-$comment_path")")"
# An edit keeps its comment's author, records who made it and when, and
# is made by the author or a holder of the approve grant.
refused "an edit that records no edit" 'changes a comment without recording the edit' operator origin \
  "$(push_graph "$(craft "$good" operator - "+$comment_path=$(comment operator 'Rewritten.')")")"
refused "an edit naming another editor" 'names alice in edited_by, and operator is pushing it' operator origin \
  "$(push_graph "$(craft "$good" operator - "+$comment_path=$(edit operator alice 'Rewritten.')")")"
refused "an edit naming its editor and not its time" 'edited and edited_by come together' operator origin \
  "$(push_graph "$(craft "$good" operator - "+$comment_path=$(printf -- '---\nauthor: "operator"\nedited_by: "operator"\n---\n\nRewritten.')")")"
refused "an edit whose time is no UTC time" 'edited is "yesterday", and it is a UTC time' operator origin \
  "$(push_graph "$(craft "$good" operator - "+$comment_path=$(edit operator operator 'Rewritten.' yesterday)")")"
refused "an edit that changes the author" 'changes its author from "operator" to "approver"' approver origin \
  "$(push_graph "$(craft "$good" approver - "+$comment_path=$(edit approver approver 'Mine now.')")")"
refused "an edit by a principal without the approve grant" 'is a comment by operator, and alice is editing it: a comment is edited by its author, or by a holder of the approve grant' \
  alice origin "$(push_graph "$(craft "$good" alice - "+$comment_path=$(edit operator alice 'Rewritten.')")")"
refused "a new comment that records an edit" 'is a new comment that records an edit' operator origin \
  "$(push_graph "$(craft "$good" operator - "+comments/$bug/$stamp-0123456789abcdef.md=$(edit operator operator 'Never written.')")")"
refused "a deleted knot" 'a knot is never deleted' operator origin "$(push_graph "$(craft "$good" operator - "-$bug_path")")"
refused "a changed id" 'filename says id' operator origin \
  "$(push_graph "$(craft "$good" operator - "+$bug_path=${bug_text/id: $bug/id: bd-0000000}")")"
refused "a changed type" "id is \"$bug\", and an outcome's id is oc-" operator origin \
  "$(push_graph "$(craft "$good" operator - "+$bug_path=${bug_text/type: bug/type: outcome}")")"
refused "a changed created_by" 'changes created_by from "operator" to "alice"' alice origin \
  "$(push_graph "$(craft "$good" alice - "+$bug_path=${bug_text/created_by: \"operator\"/created_by: \"alice\"}")")"

# Provenance: a new comment's author and a new knot's creator are the
# pusher.
refused "a comment with a forged author" 'names alice as its author, and operator is pushing it' operator origin \
  "$(push_graph "$(craft "$good" operator - "+comments/$bug/$stamp-0123456789abcdef.md=$(comment alice 'Not mine.')")")"
refused "a knot with a forged creator" 'names alice in created_by, and operator is pushing it' operator origin \
  "$(push_graph "$(craft "$good" operator - "+$(idea_path forged)=$(idea Forged forged alice)")")"

# Layout: plain files, in the layout, within the bounds.
knot_blob="$(blob "$(idea Linked linked operator)")"
refused "a symlink" 'mode 120000' operator origin "$(push_graph "$(craft "$good" operator - "120000:$(idea_path linked)=$knot_blob")")"
refused "an executable" 'mode 100755' operator origin "$(push_graph "$(craft "$good" operator - "100755:$(idea_path linked)=$knot_blob")")"
refused "a file outside the layout" 'outside the graph.s layout' operator origin "$(push_graph "$(craft "$good" operator - "+README.md=hello")")"
refused "a directory outside the layout" 'no directory a graph holds' operator origin \
  "$(push_graph "$(craft "$good" operator - "+tasks/$(basename "$(idea_path tasked)")=$(idea Tasked tasked operator)")")"
# A knot is an outcome, a bug, an idea or a question, and the hook names
# the directories each lives in.
decision="dcr-$(printf '%s' one-ref | sha256sum | cut -c1-7)"
decision_text="$(printf -- '---\ntype: decision\nid: %s\nstatus: decided\ntitle: "One ref"\ncreated: 2026-10-15\ncreated_by: "operator"\n---' "$decision")"
refused "a decision" 'decisions: is no directory a graph holds; the root holds outcomes, bugs, ideas, questions, comments' operator origin \
  "$(push_graph "$(craft "$good" operator - "+decisions/$decision-one-ref.md=$decision_text")")"
refused "a non-canonical knot" 'not in canonical form' operator origin \
  "$(push_graph "$(craft "$good" operator - "+$(idea_path loose)=$(idea Loose loose operator | sed 's/^title: "Loose"/title: Loose/')")")"
big="$(head -c 70000 /dev/zero | tr '\0' x)"
refused "an oversized knot" 'more than the 65536' operator origin \
  "$(push_graph "$(craft "$good" operator - "+$(idea_path big)=$(idea Big big operator "$big")")")"

# A tree that names one subtree many times over is refused at a bound,
# before it is walked.
leaf_lines=""
for i in $(seq 1 64); do
  leaf_lines+="$(printf '100644 blob %s\t%s-%016x.md' "$(blob "$(comment operator x)")" "$stamp" "$i")"$'\n'
done
leaf="$(printf '%s' "$leaf_lines" | git mktree)"
comments_tree="$(for i in $(seq 1 1000); do printf '040000 tree %s\toc-%07x\n' "$leaf" "$i"; done | git mktree)"
root="$(git ls-tree "$good" | grep -v $'\tcomments$' | { cat; printf '040000 tree %s\tcomments\n' "$comments_tree"; } | git mktree)"
bomb="$(GIT_COMMITTER_NAME=operator git commit-tree "$root" -p "$good" -m "$(printf 'bomb\n\nValley-Op: 0123456789abcdef')")"
start="$(date +%s)"
refused "a shared-subtree bomb" 'entries in all' operator origin "$(push_graph "$bomb")"
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
on_good() { GIT_COMMITTER_NAME=operator git commit-tree "$1" -p "$good" -m "$(printf 'raw\n\nValley-Op: %016x' "$RANDOM$RANDOM")"; }
mapfile -t root_entries < <(entries_of "$good")
mapfile -t flat_entries < <(entries_of "$good" -r)
named_idea="$(blob "$(idea Named named operator)")"
refused "a root file named ideas/<id>-<slug>.md" 'named "ideas/ida-[0-9a-f]{7}-named\.md", and git writes no name with a slash' \
  operator origin "$(push_graph "$(on_good "$(raw_tree "${root_entries[@]}" 100644 "$(idea_path named)" "$named_idea")")")"
refused "every knot and comment flattened into names with slashes" 'git writes no name with a slash' \
  operator origin "$(push_graph "$(on_good "$(raw_tree "${flat_entries[@]}")")")"
# comments/, in the mode 040000, naming a subtree with a forged comment.
hidden="$(raw_tree 40000 "$bug" "$(raw_tree 100644 "$stamp-0123456789abcdef.md" "$(blob "$(comment alice 'Never read.')")")")"
hiding=()
for ((i = 0; i < ${#root_entries[@]}; i += 3)); do
  if [ "${root_entries[i + 1]}" = comments ]; then
    hiding+=(040000 comments "$hidden")
  else
    hiding+=("${root_entries[@]:i:3}")
  fi
done
refused "a directory in the mode 040000" 'holds "comments" with mode 040000, and git writes only the modes' \
  operator origin "$(push_graph "$(on_good "$(raw_tree "${hiding[@]}")")")"
for name in . ..; do
  refused "an entry named $name" "holds an entry named \"${name//./\\.}\", which git does not write" \
    operator origin "$(push_graph "$(on_good "$(raw_tree "${root_entries[@]}" 100644 "$name" "$named_idea")")")"
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
as operator git receive-pack "$w/guarded.git" < "$w/raw.request" > "$w/raw.report" 2> "$w/err" || true
grep -q 'ng refs/the-valley/knots' "$w/raw.report" || fail "an entry with an empty name was admitted"
grep -q 'its tree: it holds an entry with an empty name' "$w/err" || fail "the hook did not refuse an entry with an empty name"
[ "$(tip guarded)" = "$good" ] || fail "an entry with an empty name moved the ref"

# Edges: every edge resolves, a parent is an outcome, and no edge forms a
# cycle. A cycle is crafted, since the CLI refuses to write one.
refused "a blocker that is no knot" 'is no knot in the graph' operator origin \
  "$(push_graph "$(craft "$good" operator - "+$work_path=${work_text/blocked_by: \[$bug\]/blocked_by: [$bug, oc-0000000]}")")"
loop_bug="${bug_text/blocked_by: \[\]/blocked_by: [$work]}"
[ "$loop_bug" != "$bug_text" ] || fail "the bug's blocked_by could not be edited"
refused "a cycle" 'blocked_by edges form a cycle' operator origin "$(push_graph "$(craft "$good" operator - "+$bug_path=$loop_bug")")"

# The ref: never deleted, and matched exactly.
refused "a deletion of the graph" 'never deleted' operator origin --delete refs/the-valley/knots
refused "a ref that only begins with the graph's name" 'written only by the machinery' operator origin "$next:refs/the-valley/knots-x"
refused "a ref below the graph's name" 'written only by the machinery' operator origin "$next:refs/the-valley/knots/x"

# Create: one commit with no parent, on a project that has no graph yet.
first="$(craft - operator - "+$(idea_path first)=$(idea First first operator)")"
child="$(craft "$first" operator - "+$(idea_path second)=$(idea Second second operator)")"
if as operator git push --quiet open "$(push_graph "$child")" 2> "$w/err"; then fail "a create with a parent was admitted"; fi
grep -q 'creates the graph has no parent' "$w/err" || fail "a create with a parent was refused for another reason"
[ -z "$(tip open)" ] || fail "a refused create made the ref"
as operator git push --quiet open "$(push_graph "$first")" 2> "$w/err" || fail "a parentless create was refused"
as operator git push --quiet open "$(push_graph "$child")" 2> "$w/err" || fail "the update after it was refused"
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
      operator "$mode" origin "$(push_graph "$next")" "$other"
    if git -C "$w/guarded.git" rev-parse --verify --quiet "${other#*:}" > /dev/null; then fail "$other landed"; fi
  done
done

# The well-formed commit itself is admitted.
as operator git push --quiet origin "$(push_graph "$next")" 2> "$w/err" || fail "a well-formed update was refused"
[ "$(tip guarded)" = "$next" ] || fail "the well-formed update did not land"

# A crafted edit of another's comment is admitted from a holder of the
# approve grant under either name, and its author edits it again after.
as approver git push --quiet origin "$(push_graph "$(craft "$next" approver - "+$comment_path=$(edit operator approver 'Merged.')")")" 2> "$w/err" \
  || fail "a holder of the approve grant under request could not edit another's comment"
as reviewer git push --quiet origin "$(push_graph "$(craft "$(tip guarded)" reviewer - "+$comment_path=$(edit operator reviewer 'Merged again.')")")" 2> "$w/err" \
  || fail "a holder of the approve grant under approve could not edit another's comment"
as operator git push --quiet origin "$(push_graph "$(craft "$(tip guarded)" operator - "+$comment_path=$(edit operator operator 'Restored.')")")" 2> "$w/err" \
  || fail "a comment's author could not edit it after another had"
[ "$(git -C "$w/guarded.git" cat-file blob "refs/the-valley/knots:$comment_path")" = "$(edit operator operator 'Restored.')" ] \
  || fail "the last edit is not what the comment holds"
touch "$out"
