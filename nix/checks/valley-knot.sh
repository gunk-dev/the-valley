# shellcheck shell=bash
# valley knot, end to end and unmocked: the real valley against local bare
# repositories that run no hook, as any git remote would. Every verb, and
# then what a write does when it does not land the first time: two writers
# racing for the ref, a push whose outcome the command cannot tell, a claim
# another run took first, and a field another write changed.
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
set -euo pipefail

w="$TMPDIR/valley-knot"
mkdir -p "$w"
export HOME="$w"
export XDG_CACHE_HOME="$w/cache"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name valley-knot-check
git config --global user.email valley-knot-check@localhost
git config --global init.defaultBranch main

# valley is bin/valley itself, run by bash. The package's wrapper puts its
# own git ahead of anything on PATH, and the races below need a stand-in
# for git to run first.
mkdir -p "$w/bin"
printf '#!%s\nexec %q %q "$@"\n' "$BASH" "$BASH" "$cliScript" > "$w/bin/valley"
chmod +x "$w/bin/valley"
export PATH="$w/bin:$PATH"

fail() {
  printf 'valley-knot: %s\n' "$*" >&2
  for f in "$w"/out "$w"/err; do
    if [ -s "$f" ]; then cat "$f" >&2; fi
  done
  exit 1
}

# ok <command…>: the command succeeds, and its output is kept.
ok() {
  "$@" > "$w/out" 2> "$w/err" || fail "$* failed"
}

# fails <status> <ERE> <command…>: the command exits with that status,
# and says something matching the pattern.
fails() {
  local want="$1" says="$2" status=0
  shift 2
  "$@" > "$w/out" 2> "$w/err" || status=$?
  [ "$status" = "$want" ] || fail "$* exited $status, not $want"
  grep -qE -- "$says" "$w/err" || fail "$* did not say $says"
}

# holds <jq condition…>: the last JSON printed holds each.
holds() {
  local cond
  for cond in "$@"; do
    jq -e "$cond" "$w/out" > /dev/null || fail "$cond does not hold"
  done
}

line() {
  grep -qE -- "$1" "$w/out" || fail "no line matches $1"
}

srv="$w/srv"
git init --quiet --bare "$srv/the-valley.git"
git init --quiet --bare "$srv/app.git"
graph="$srv/the-valley.git"
git init --quiet "$w/work"
cd "$w/work"
git remote add origin "$srv/the-valley.git"

# A write names the principal; without one it is refused, and nothing is
# pushed.
fails 1 'valley.principal' valley knot new outcome "Slice one lands"
git config valley.principal operator
if git -C "$graph" rev-parse --verify --quiet refs/the-valley/knots > /dev/null; then
  fail "a refused write created the ref"
fi

# ----------------------------------------------------------------------
# The model, for an agent that never reads CUE.
ok valley knot schema --json
holds '.types | map(.name) == ["outcome","bug","idea","question"]' \
  '.types[0].active == ["open","in-progress"]' '.types[0].terminal == ["done","abandoned"]' \
  '[.fields[].name] | index("claimed_by") != null' '.limits.knots == 5000'
ok valley knot schema
line '^  idea'
fails 1 'the types are outcome, bug, idea, question' valley knot new task "Nothing"
fails 1 '"decision" is no type of knot; the types are outcome, bug, idea, question' valley knot new decision "Nothing"
fails 2 'knot has no verb' valley knot frob

# ----------------------------------------------------------------------
# new: the first write creates the ref, as one parentless commit by the
# principal, carrying its operation's trailer.
ok valley knot new outcome "Slice one lands" --json
milestone="$(jq -r .knot.id "$w/out")"
holds '.schema == "valley-knot-op/v1"' '.knot.status == "open"' '.knot.created_by == "operator"' \
  '.knot.blocked_by == []' '.op | test("^[0-9a-f]{16}$")'
first="$(git -C "$graph" rev-parse refs/the-valley/knots)"
[ "$(git -C "$graph" rev-list --parents -n 1 "$first")" = "$first" ] || fail "the first commit has a parent"
[ "$(git -C "$graph" log -1 --format=%cn "$first")" = operator ] || fail "the committer is not the principal"
[ "$(git -C "$graph" log -1 --format='%(trailers:key=Valley-Op,valueonly)' "$first")" = "$(jq -r .op "$w/out")" ] \
  || fail "the commit carries no Valley-Op trailer naming its operation"
[ "$milestone" = "oc-$(printf '%s' slice-one-lands | sha256sum | cut -c1-7)" ] \
  || fail "the id $milestone is not derived from the slug"

ok valley knot new bug "The hook reads too much" --parent "$milestone"
bug="bd-$(printf '%s' the-hook-reads-too-much | sha256sum | cut -c1-7)"
printf 'The hook case.\n\nWith a second paragraph.\n' > "$w/body"
ok valley knot new outcome "Write the hook case" --parent "$milestone" \
  --blocked-by "$bug" --blocked-by app:oc-0123456 --owner alice --body - < "$w/body"
work="oc-$(printf '%s' write-the-hook-case | sha256sum | cut -c1-7)"
# Every later commit has the previous tip as its one parent.
second="$(git -C "$graph" rev-parse refs/the-valley/knots)"
[ "$(git -C "$graph" rev-list --count "$second")" = 3 ] || fail "three writes are not three commits"
[ "$(git -C "$graph" rev-list --merges --count "$second")" = 0 ] || fail "a write made a merge"

# The same title again takes a suffix, and so another id.
ok valley knot new outcome "Write the hook case" --json
holds ".knot.id != \"$work\"" '.knot.path | endswith("-write-the-hook-case-2.md")'
twin="$(jq -r .knot.id "$w/out")"

# Edges are checked as the hook checks them.
fails 1 'is no knot in the graph' valley knot new outcome "Dangling" --parent oc-0000000
fails 1 'and a parent is an outcome' valley knot new idea "Misparented" --parent "$bug"
fails 1 'carries no blocked_by' valley knot new idea "Blocked idea" --blocked-by "$bug"
fails 1 'control character' valley knot new idea $'A bell \a here'

# ----------------------------------------------------------------------
# show and list.
ok valley knot show "$work" --json
holds '.schema == "valley-knot/v1"' ".knot.parent == \"$milestone\"" '.knot.owner == "alice"' \
  ".knot.blocked_by == [\"app:oc-0123456\", \"$bug\"]" ".knot.waiting_on == [\"app:oc-0123456\", \"$bug\"]" \
  '.knot.dangling == ["app:oc-0123456"]' '.knot.ready == false' \
  '.knot.body == "The hook case.\n\nWith a second paragraph.\n"'
ok valley knot show "$milestone" --json
holds ".knot.children | sort == ([\"$bug\", \"$work\"] | sort)" '.knot.progress == {"done": 0, "of": 2}'
ok valley knot show "$bug"
line '^blocks: +oc-'
ok valley knot list --json
holds '.schema == "valley-knots/v1"' '.knots | length == 4'
ok valley knot list --ready --json
holds "[.knots[].id] | sort == ([\"$milestone\", \"$bug\", \"$twin\"] | sort)"
ok valley knot list --type bug --json
holds ".knots | map(.id) == [\"$bug\"]"
ok valley knot list --owner alice --json
holds ".knots | map(.id) == [\"$work\"]"
fails 1 '--status takes active, terminal, or one of' valley knot list --status nonsense
fails 1 'holds no knot' valley knot show oc-0000000

# ----------------------------------------------------------------------
# set: the settable fields, and the guards.
ok valley knot set "$bug" title="The hook reads too much at once" owner=operator
ok valley knot show "$bug" --json
holds '.knot.title == "The hook reads too much at once"' '.knot.owner == "operator"'
fails 1 'set changes status, title, source, owner, target, graduated_into' valley knot set "$bug" priority=high
fails 1 'change it with valley knot link' valley knot set "$bug" parent="$work"
fails 1 "a bug's status is one of open, closed" valley knot set "$bug" status=done
fails 3 "requires \"closed\"" valley knot set "$bug" status=open --if status=closed
fails 0 'nothing to change' valley knot set "$bug" owner=operator
ok valley knot set "$bug" owner=

# link and unlink, and a cycle refused.
ok valley knot link "$twin" --blocked-by "$bug" --change valley:the-valley/kkkkllllmmmmnnnnooooppppqqqqrrrr
ok valley knot show "$twin" --json
holds ".knot.blocked_by == [\"$bug\"]" '.knot.changes == ["valley:the-valley/kkkkllllmmmmnnnnooooppppqqqqrrrr"]'
fails 1 'blocked_by edges form a cycle' valley knot link "$bug" --blocked-by "$twin"
fails 1 'parent edges form a cycle' valley knot link "$milestone" --parent "$work"
fails 3 'unlink it first' valley knot link "$work" --parent "$twin"
fails 1 'is valley:<project>/<change ID>' valley knot link "$twin" --change https://example.com/x
ok valley knot unlink "$twin" --blocked-by "$bug"
ok valley knot show "$twin" --json
holds '.knot.blocked_by == []'

# edit replaces the body.
printf 'A new body.\n' | valley knot edit "$twin" --body - > "$w/out" 2> "$w/err" || fail "edit failed"
ok valley knot show "$twin" --json
holds '.knot.body == "A new body.\n"'

# comment: by -m and by stdin, each its own file. Comments are listed by
# the second they were written in, and by operation within one second.
ok valley knot comment "$work" -m "A finding."
printf 'A second finding,\nover two lines.\n' | valley knot comment "$work" --body - > "$w/out" 2> "$w/err" \
  || fail "a comment from stdin failed"
ok valley knot show "$work" --json
holds '.knot.comment_count == 2' \
  '[.knot.comments[].body] | sort == ["A finding.\n", "A second finding,\nover two lines.\n"]' \
  '.knot.comments | all(.author == "operator")'
[ "$(git -C "$graph" ls-tree --name-only "refs/the-valley/knots:comments/$work" | wc -l)" = 2 ] \
  || fail "two comments are not two files"
fails 1 'takes the comment' valley knot comment "$work"

# comment --edit replaces a comment's text in place. The comment keeps its
# id, its file, its author and its stamp, and records who edited it and
# when. Each comment's id is its file's name without .md.
ok valley knot show "$work" --json
holds '.knot.comments | all(.id | test("^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{16}$"))' '.knot.comments | all(has("edited") | not)'
finding="$(jq -r '.knot.comments[] | select(.body == "A finding.\n") | .id' "$w/out")"
found_at="$(jq -r ".knot.comments[] | select(.id == \"$finding\") | .at" "$w/out")"
[ "$finding.md" = "$(git -C "$graph" ls-tree --name-only "refs/the-valley/knots:comments/$work" | grep -F "${finding#*-}")" ] \
  || fail "a comment's id is not its file's name"
unedited="$(git -C "$graph" rev-parse refs/the-valley/knots)"
ok env VALLEY_NOW=2026-10-25T15:00:00Z valley knot comment "$work" --edit "$finding" -m "A finding, restated." --json
holds ".knot.comments | map(select(.id == \"$finding\")) | .[0] | .body == \"A finding, restated.\\n\" and .author == \"patrick\"
  and .at == \"$found_at\" and .edited == \"2026-10-25T15:00:00Z\" and .edited_by == \"patrick\"" '.knot.comment_count == 2'
[ "$(git -C "$graph" ls-tree --name-only "refs/the-valley/knots:comments/$work" | wc -l)" = 2 ] \
  || fail "an edit added a file"
[ "$(git -C "$graph" show -s --format=%s refs/the-valley/knots)" = "knot comment $work: edit $finding" ] \
  || fail "an edit is not one commit naming the comment"
restated="$(git -C "$graph" rev-parse refs/the-valley/knots)"
# The same editor edits it again, and each edit is its own commit.
ok env VALLEY_NOW=2026-10-25T16:00:00Z valley knot comment "$work" --edit "$finding" -m "A finding, restated twice."
ok valley knot show "$work"
line "^— patrick, .*, edited 2026-10-25 16:00Z by patrick  \\[$finding\\]$"
fails 0 'says that already; nothing to change' valley knot comment "$work" --edit "$finding" -m "A finding, restated twice."
fails 1 "holds no comment 20261025T000000Z-0000000000000000" valley knot comment "$work" --edit 20261025T000000Z-0000000000000000 -m "Nothing."
fails 1 "comment $finding is on $work, not on $bug" valley knot comment "$bug" --edit "$finding" -m "Elsewhere."
fails 2 "--edit takes a comment's id" valley knot comment "$work" --edit "../$bug/$finding" -m "Elsewhere."
fails 1 'takes the comment' valley knot comment "$work" --edit "$finding"

# log lists every commit that touched the knot or its comments, newest
# first, in the ref's ancestry, each with its time, committer and
# operation as the commit records them. The milestone was the first knot,
# so its log ends at the root commit.
ok valley knot log "$work" --json
holds '.schema == "valley-knot-log/v1"' ".id == \"$work\"" '.commits | all(.committer == "patrick")' \
  '.commits[0].subject == "knot comment '"$work"': edit '"$finding"'"' \
  ".commits[0].files == [{\"path\": \"comments/$work/$finding.md\", \"change\": \"changed\", \"comment\": \"$finding\"}]" \
  '.commits[-1].files == [{"path": .path, "change": "added"}]'
[ "$(jq -r '.commits[].commit' "$w/out")" = "$(git -C "$graph" rev-list refs/the-valley/knots -- "$(jq -r .path "$w/out")" "comments/$work/")" ] \
  || fail "log does not list the knot's commits in the ref's order"
for c in $(jq -r '.commits[].commit' "$w/out"); do
  [ "$(jq -r ".commits[] | select(.commit == \"$c\") | [.at, .op] | join(\" \")" "$w/out")" \
    = "$(TZ=UTC git -C "$graph" log -1 --date=format-local:%Y-%m-%dT%H:%M:%SZ --format='%cd %(trailers:key=Valley-Op,valueonly)' "$c")" ] \
    || fail "log does not give commit $c's time and operation"
done
ok valley knot log "$milestone" --json
holds ".commits[-1].commit == \"$first\""
ok valley knot log "$work"
line "^${restated:0:7} [0-9-]+ [0-9:]+Z patrick  knot comment $work: edit $finding$"
line "^    changed comments/$work/$finding.md$"

# show --at reads the knot as a commit of the graph left it, so every
# earlier text of a comment is read back.
ok valley knot show "$work" --at "$unedited" --json
holds ".at == \"$unedited\"" ".tip == \"$(git -C "$graph" rev-parse refs/the-valley/knots)\"" \
  ".knot.comments | map(select(.id == \"$finding\")) | .[0] | .body == \"A finding.\\n\" and (has(\"edited\") | not)"
ok valley knot show "$work" --at "${restated:0:12}" --json
holds ".knot.comments | map(select(.id == \"$finding\")) | .[0].body == \"A finding, restated.\\n\""
ok valley knot show "$work" --at "$unedited"
line "^as of $unedited$"
# Only the graph's own history is read: a commit the tip does not descend
# from is refused, and so is one from before the knot was made.
stray="$(git commit-tree "$(git hash-object -w -t tree /dev/null)" -m stray)"
fails 1 "is no commit of the graph" valley knot show "$work" --at "$stray"
fails 1 "held no knot $work at" valley knot show "$work" --at "$first"
fails 2 '--at takes a commit of the graph' valley knot show "$work" --at main

# claim and unclaim.
ok valley knot claim "$work" --run run-1 --json
holds '.knot.claimed_by == "run-1"' '.knot.status == "in-progress"' '.knot.claim.stale == false'
fails 3 'is claimed by run-1' valley knot claim "$work" --run run-2
fails 0 'claimed by run-1 already' valley knot claim "$work" --run run-1
ok valley knot set "$twin" status=abandoned
fails 1 'only an active knot is claimed' valley knot claim "$twin" --run run-2
fails 3 'requires "run-2"' valley knot unclaim "$work" --if claimed_by=run-2
# A field no knot has is refused before anything is read. Read as the
# empty value an unset field holds, this --if would hold on every knot.
before="$(git -C "$graph" rev-parse refs/the-valley/knots)"
fails 1 'a knot has no field claimed_byy; --if compares type, id, status, title' valley knot unclaim "$work" --if claimed_byy=
[ "$(git -C "$graph" rev-parse refs/the-valley/knots)" = "$before" ] || fail "an --if naming no field let a write through"
ok valley knot unclaim "$work"
ok valley knot show "$work" --json
holds '.knot | has("claimed_by") | not' '.knot.status == "in-progress"'
ok valley knot claim "$work" --run run-2

# ----------------------------------------------------------------------
# Another project on the same host, by --project and by a qualified id.
ok valley knot new outcome "Land the module" --project app --json
holds '.project == "app"'
other="$(jq -r .knot.id "$w/out")"
ok valley knot link "$work" --blocked-by "app:$other"
ok valley knot show "$work" --json
holds ".knot.waiting_on | index(\"app:$other\") != null" ".knot.dangling | index(\"app:$other\") == null"
ok valley knot set "$other" status=done --project app
ok valley knot show "app:$other" --json
holds '.project == "app"' '.knot.status == "done"'
ok valley knot show "$work" --json
holds ".knot.waiting_on | index(\"app:$other\") == null"
git -C "$srv/app.git" rev-parse --verify --quiet refs/the-valley/knots > /dev/null \
  || fail "--project wrote no graph in app"

# ----------------------------------------------------------------------
# Ready means an outcome or a bug: an idea and an open question are not.
ok valley knot new idea "A thought, not work" --json
idea="$(jq -r .knot.id "$w/out")"
ok valley knot new question "Which ref holds the graph" --owner operator --json
asked="$(jq -r .knot.id "$w/out")"
holds '.knot.type == "question"' '.knot.status == "open"' '.knot.owner == "operator"' \
  '.knot.path | startswith("questions/qn-")'
fails 1 'carries no blocked_by' valley knot new question "A blocked question" --blocked-by "$bug"
ok valley knot list --ready --json
holds ".knots | map(.id) | index(\"$idea\") == null" ".knots | map(.id) | index(\"$asked\") == null" \
  ".knots | map(.id) | index(\"$bug\") != null"
ok valley knot show "$idea" --json
holds '.knot.ready == false'
ok valley knot show "$asked" --json
holds '.knot.ready == false'

# Work waits on a question until it is answered or dropped. An answer
# finishes the question, so it is no ending the view reports.
ok valley knot new outcome "Write the answer down" --blocked-by "$asked" --json
written="$(jq -r .knot.id "$w/out")"
ok valley knot new bug "The answer is missing" --blocked-by "$asked" --json
missing="$(jq -r .knot.id "$w/out")"
for k in "$written" "$missing"; do
  ok valley knot show "$k" --json
  holds '.knot.ready == false' ".knot.waiting_on == [\"$asked\"]"
done
ok valley knot set "$asked" status=answered
ok valley knot show "$asked" --json
holds '.knot.ready == false'
for k in "$written" "$missing"; do
  ok valley knot show "$k" --json
  holds '.knot.ready == true' '.knot.waiting_on == []' '.knot.cleared_by_ending == []'
done
# A question can open again, and the work waits on it again.
ok valley knot set "$asked" status=open
for k in "$written" "$missing"; do
  ok valley knot show "$k" --json
  holds '.knot.ready == false' ".knot.waiting_on == [\"$asked\"]"
done
# A dropped question clears its blocker without being answered, and the
# view says so.
ok valley knot new question "Is a second ref needed" --json
moot="$(jq -r .knot.id "$w/out")"
ok valley knot new outcome "Mirror the second ref" --blocked-by "$moot" --json
mirror="$(jq -r .knot.id "$w/out")"
ok valley knot set "$moot" status=dropped
ok valley knot show "$mirror" --json
holds '.knot.ready == true' '.knot.waiting_on == []' ".knot.cleared_by_ending == [\"$moot\"]"

# An answered question links the change that records its answer in the
# source, and stays never ready. An idea links no change: it graduates
# into an outcome or a question, and that links the change.
ok valley knot set "$asked" status=answered
ok valley knot link "$asked" --change https://github.com/example/the-valley/pull/7
ok valley knot show "$asked" --json
holds '.knot.changes == ["https://github.com/example/the-valley/pull/7"]' '.knot.ready == false'
ok valley knot show "$asked"
line '^change: +https://github.com/example/the-valley/pull/7$'
ok valley knot list --type question --json
holds ".knots | map(select(.id == \"$asked\")) | .[0].changes == [\"https://github.com/example/the-valley/pull/7\"]"
before="$(git -C "$graph" rev-parse refs/the-valley/knots)"
fails 1 'an idea does not carry changes' valley knot link "$idea" --change https://github.com/example/the-valley/pull/7
[ "$(git -C "$graph" rev-parse refs/the-valley/knots)" = "$before" ] || fail "a change linked to an idea was pushed"
ok valley knot schema --json
holds '.fields[] | select(.name == "changes") | .on == ["outcome","bug","question"]'

# A graduated idea names where it went: a question to settle, or an
# outcome taken on as work, and never a bug.
ok valley knot set "$idea" status=graduated graduated_into="$asked"
ok valley knot show "$idea" --json
holds '.knot.status == "graduated"' ".knot.graduated_into == \"$asked\""
ok valley knot new idea "A thought taken on as work" --json
taken="$(jq -r .knot.id "$w/out")"
fails 1 "an outcome's or a question's id" valley knot set "$taken" status=graduated graduated_into="$bug"
ok valley knot set "$taken" status=graduated graduated_into="$milestone"
ok valley knot show "$taken" --json
holds '.knot.status == "graduated"' ".knot.graduated_into == \"$milestone\""

# Finishing a knot releases its claim, in the same commit.
ok valley knot new outcome "Finish the claimed work" --json
finished="$(jq -r .knot.id "$w/out")"
ok valley knot claim "$finished" --run run-3
ok valley knot set "$finished" status=done --json
holds '.knot.status == "done"' '.knot | has("claimed_by") | not' '.knot | has("claimed_at") | not'
ok valley knot show "$finished" --json
holds '.knot | has("claim") | not' '.knot.ready == false'
[ "$(git -C "$graph" show -s --format=%s refs/the-valley/knots)" = "knot set $finished: status=done" ] \
  || fail "finishing was not one commit"

# A reference to a project that cannot be read is shown as dangling, and
# git's own errors stay off stderr.
ok valley knot new outcome "Wait on nowhere" --blocked-by nowhere:oc-0123456 --json
quiet="$(jq -r .knot.id "$w/out")"
ok valley knot show "$quiet" --json
holds '.knot.unresolved == ["nowhere:oc-0123456"]' '.knot.ready == false'
[ ! -s "$w/err" ] || fail "show printed noise for an unreadable project: $(cat "$w/err")"
ok valley knot show "$quiet"
line 'dangling: its project could not be read'
[ ! -s "$w/err" ] || fail "show printed noise for an unreadable project: $(cat "$w/err")"
ok valley knot list
[ ! -s "$w/err" ] || fail "list printed noise for an unreadable project: $(cat "$w/err")"

# ----------------------------------------------------------------------
# What happens when a push does not land the first time. A stand-in for
# git runs first on PATH, and on its first push it does one thing given in
# RACE before or after passing the push on, then fails or not as told.
real_path="$PATH"
mkdir -p "$w/shim"
{
  printf '#!%s\n' "$BASH"
  cat << 'EOF'
if [ "$1" = push ] && [ ! -e "$RACE_MARKER" ]; then
  touch "$RACE_MARKER"
  if [ "$RACE_WHEN" = before ]; then PATH="$REAL_PATH" "$BASH" -c "$RACE"; fi
  if [ "$RACE_PUSH" = yes ]; then PATH="$REAL_PATH" git "$@" || exit; fi
  if [ "$RACE_WHEN" = after ]; then PATH="$REAL_PATH" "$BASH" -c "$RACE"; fi
  if [ "$RACE_FAIL" = yes ]; then
    echo "fatal: the remote end hung up unexpectedly" >&2
    exit 128
  fi
  [ "$RACE_PUSH" = yes ] && exit 0
fi
PATH="$REAL_PATH" exec git "$@"
EOF
} > "$w/shim/git"
chmod +x "$w/shim/git"
race=0
# raced <when> <push> <fail> <other writer> -- <command…>
raced() {
  local when="$1" pushes="$2" fails="$3" other="$4"
  shift 5
  race=$((race + 1))
  env PATH="$w/shim:$real_path" REAL_PATH="$real_path" RACE_MARKER="$w/race.$race" \
    RACE_WHEN="$when" RACE_PUSH="$pushes" RACE_FAIL="$fails" RACE="$other" "$@"
}
comments_saying() {
  local n=0 f
  for f in $(git -C "$graph" ls-tree -r --name-only refs/the-valley/knots -- comments/); do
    if git -C "$graph" cat-file blob "refs/the-valley/knots:$f" | grep -qxF -- "$1"; then n=$((n + 1)); fi
  done
  echo "$n"
}

# Two writers race: the other lands between this one's read and its push,
# so the lease fails, and this one is formed again on the new tip. Both
# land, each once.
raced before no no "valley knot comment $bug -m 'The other writer.'" -- \
  valley knot comment "$bug" -m "This writer." > "$w/out" 2> "$w/err" || fail "a writer that lost a race did not retry"
[ "$(comments_saying 'The other writer.')" = 1 ] || fail "the other writer's comment did not land once"
[ "$(comments_saying 'This writer.')" = 1 ] || fail "the writer that lost the race did not land once"

# Two writers at once, three writes each, all land.
pids=()
for who in a b; do
  (for n in 1 2 3; do valley knot comment "$milestone" -m "Writer $who, write $n." || exit 1; done) \
    > "$w/parallel.$who" 2>&1 &
  pids+=("$!")
done
wait "${pids[0]}" || fail "the first parallel writer failed: $(cat "$w/parallel.a")"
wait "${pids[1]}" || fail "the second parallel writer failed: $(cat "$w/parallel.b")"
for who in a b; do
  for n in 1 2 3; do
    [ "$(comments_saying "Writer $who, write $n.")" = 1 ] || fail "writer $who's write $n did not land once"
  done
done

# The push lands, and the connection drops before the command hears so,
# while another writer moves the ref on. The command finds its operation
# on the ref and stops: nothing is written twice.
raced after yes yes "valley knot comment $bug -m 'Landed meanwhile.'" -- \
  valley knot comment "$bug" -m "Landed, unheard." > "$w/out" 2> "$w/err" \
  || fail "a write that landed unheard was reported failed"
[ "$(comments_saying 'Landed, unheard.')" = 1 ] || fail "a write that landed unheard was written again"
[ "$(comments_saying 'Landed meanwhile.')" = 1 ] || fail "the other writer's comment is not there once"
# The push does not land, and the command cannot tell: it looks, finds
# nothing, and tries again, once.
raced none no yes "" -- valley knot comment "$bug" -m "Lost, then sent." > "$w/out" 2> "$w/err" \
  || fail "a write whose push was lost was not tried again"
[ "$(comments_saying 'Lost, then sent.')" = 1 ] || fail "a lost write did not land once"

# Two edits of one comment race: the other lands between this one's read
# and its push. This one was written against the text before it, so on
# the new tip it is a conflict, and the other edit stands.
ok valley knot show "$bug" --json
raced_comment="$(jq -r '.knot.comments[] | select(.body == "This writer.\n") | .id' "$w/out")"
status=0
raced before no no "valley knot comment $bug --edit $raced_comment -m 'Their edit.'" -- \
  valley knot comment "$bug" --edit "$raced_comment" -m "My edit." > "$w/out" 2> "$w/err" || status=$?
[ "$status" = 3 ] || fail "an edit overwrote an edit it never read (status $status)"
grep -q "conflict: comment $raced_comment on $bug changed while this was written" "$w/err" \
  || fail "the conflict does not name the comment"
[ "$(comments_saying 'Their edit.')" = 1 ] || fail "the other edit does not stand"
[ "$(comments_saying 'My edit.')" = 0 ] || fail "the edit that lost the race landed"
# The edit lands, and the connection drops before the command hears so,
# while another writer moves the ref on. On the new tip the comment holds
# this edit's own text, which a retry would take for another edit's; the
# command finds its operation on the ref first, and stops.
raced after yes yes "valley knot comment $bug -m 'Moved on meanwhile.'" -- \
  valley knot comment "$bug" --edit "$raced_comment" -m "Edited, unheard." > "$w/out" 2> "$w/err" \
  || fail "an edit that landed unheard was reported failed"
[ "$(comments_saying 'Edited, unheard.')" = 1 ] || fail "an edit that landed unheard is not there"
[ "$(git -C "$graph" log --format=%s refs/the-valley/knots | grep -cxF "knot comment $bug: edit $raced_comment")" = 2 ] \
  || fail "an edit that landed unheard was written again"

# A claim another run took first is a conflict, never an overwrite.
ok valley knot unclaim "$work"
raced before no no "valley knot claim $work --run run-other" -- \
  valley knot claim "$work" --run run-mine > "$w/out" 2> "$w/err" && fail "a claim overwrote another run's"
grep -q 'conflict: .*is claimed by run-other' "$w/err" || fail "a lost claim was not reported as a conflict"
ok valley knot show "$work" --json
holds '.knot.claimed_by == "run-other"'

# An unclaim releases only the claim it read. Here that claim is released,
# and the knot claimed by another run, before this unclaim's push: on the
# new tip it finds a claim it never read, and that is a conflict.
status=0
raced before no no "valley knot unclaim $work && valley knot claim $work --run run-replacement" -- \
  valley knot unclaim "$work" > "$w/out" 2> "$w/err" || status=$?
[ "$status" = 3 ] || fail "an unclaim released a claim it never read (status $status)"
grep -q "conflict: $work's claim changed from run-other since .* to run-replacement since " "$w/err" \
  || fail "the conflict does not say how the claim changed"
ok valley knot show "$work" --json
holds '.knot.claimed_by == "run-replacement"'

# A field another write changed meanwhile is a conflict too, unless
# --force says to set it anyway.
status=0
raced before no no "valley knot set $bug title='Their title'" -- \
  valley knot set "$bug" title="My title" > "$w/out" 2> "$w/err" || status=$?
[ "$status" = 3 ] || fail "a title changed meanwhile was overwritten (status $status)"
grep -q 'changed from' "$w/err" || fail "the conflict does not say what changed"
raced before no no "valley knot set $bug title='Their second title'" -- \
  valley knot set "$bug" title="My title" --force > "$w/out" 2> "$w/err" || fail "--force did not set the title"
ok valley knot show "$bug" --json
holds '.knot.title == "My title"'

# ----------------------------------------------------------------------
# None of it touched the checkout.
[ -z "$(git status --porcelain)" ] || fail "a write touched the worktree"
if git rev-parse --verify --quiet HEAD > /dev/null; then fail "a write made a commit on the checkout"; fi
# Every commit on the ref is one operation by the principal, with one
# parent, and no operation appears twice.
git -C "$graph" log --format=%cn refs/the-valley/knots | sort -u | grep -qvx operator && fail "a commit names another committer"
[ "$(git -C "$graph" log --format=%B refs/the-valley/knots | grep -c '^Valley-Op:')" \
  = "$(git -C "$graph" rev-list --count refs/the-valley/knots)" ] || fail "a commit carries no operation"
dupes="$(git -C "$graph" log --format=%B refs/the-valley/knots | grep '^Valley-Op:' | sort | uniq -d)"
[ -z "$dupes" ] || fail "an operation was recorded twice: $dupes"

touch "$out"
