# shellcheck shell=bash
# The status command reads an asynchronous integration result. Exercise it
# against real refs, including the gaps between updating main, recording an
# outcome, and consuming a request. No controller or timing assumption is
# needed to make those states observable. Every state is read twice: as
# the sentences an operator reads, and through --json, the valley-status/v1
# object front ends parse.
# The derivation supplies its output path.
# shellcheck disable=SC2154
set -euo pipefail

w="$TMPDIR/valley-status"
mkdir -p "$w"
export GIT_CONFIG_GLOBAL="$w/gitconfig"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name valley-status-check
git config --global user.email valley-status-check@localhost
git config --global init.defaultBranch main
git config --global advice.detachedHead false

origin="$w/project.git"
repo="$w/project"
git init --quiet --bare "$origin"
git init --quiet "$repo"
git -C "$repo" remote add origin "$origin"
printf 'base\n' > "$repo/readme.txt"
git -C "$repo" add readme.txt
git -C "$repo" commit --quiet -m base
git -C "$repo" push --quiet origin main
base="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" checkout --quiet -b topic
printf 'topic\n' > "$repo/readme.txt"
git -C "$repo" commit --quiet -am topic
head="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" push --quiet origin topic
git -C "$repo" checkout --quiet main
git -C "$repo" fetch --quiet origin

request=refs/the-valley/integration-requests/main/topic
memo=refs/the-valley/integration-outcomes/main/topic
evidence_ref="refs/the-valley/attestations/$(printf '%064d' 0)/test"

fail() {
  printf 'valley-status: %s\n' "$*" >&2
  if [ -f "$w/status.out" ]; then cat "$w/status.out" >&2; fi
  if [ -f "$w/status.json" ]; then cat "$w/status.json" >&2; fi
  exit 1
}

contains() {
  grep -qiF -- "$1" "$w/status.out" || fail "$2"
}

lacks() {
  if grep -qiF -- "$1" "$w/status.out"; then fail "$2"; fi
}

matches() {
  grep -qiE -- "$1" "$w/status.out" || fail "$2"
}

evidence_state() {
  git -C "$origin" for-each-ref --format='%(refname) %(objectname)' \
    refs/the-valley/attestations | sha256sum | cut -c1-16
}

record_outcome() {
  local blob
  blob="$(printf '%s %s %s %s\n' "$1" "$2" "$(evidence_state)" "$3" |
    git -C "$origin" hash-object -w --stdin)"
  git -C "$origin" update-ref "$memo" "$blob"
}

reset_remote() {
  git -C "$origin" update-ref refs/heads/main "$base"
  git -C "$origin" update-ref refs/heads/topic "$head"
  git -C "$origin" update-ref -d "$request"
  git -C "$origin" update-ref -d "$memo"
  git -C "$origin" update-ref -d "$evidence_ref"
}

# A read must preserve every local branch and both kinds of uncommitted
# change. FETCH_HEAD is included because scripts and people may be using
# the preceding fetch result independently of status.
local_state() {
  git -C "$repo" symbolic-ref -q HEAD || true
  git -C "$repo" rev-parse HEAD
  git -C "$repo" for-each-ref --format='%(refname) %(objectname)' refs/heads
  git -C "$repo" status --porcelain=v1
  git -C "$repo" diff --binary
  git -C "$repo" diff --cached --binary
  if [ -f "$repo/.git/FETCH_HEAD" ]; then
    sha256sum "$repo/.git/FETCH_HEAD"
  fi
}

status() {
  local_state > "$w/before"
  (cd "$repo" && valley status "${1:-topic}") > "$w/status.out" 2>&1 \
    || fail "status could not inspect the remote"
  local_state > "$w/after"
  diff -u "$w/before" "$w/after" || fail "status changed the local checkout or FETCH_HEAD"
}

# The same read through --json: one object on one line, on stdout alone,
# and the local checkout left as it was.
status_json() {
  local_state > "$w/before"
  (cd "$repo" && valley status --json "${1:-topic}") > "$w/status.json" 2> "$w/status.err" \
    || fail "status --json could not inspect the remote"
  local_state > "$w/after"
  diff -u "$w/before" "$w/after" || fail "status --json changed the local checkout or FETCH_HEAD"
  [ "$(wc -l < "$w/status.json")" = 1 ] || fail "status --json printed other than one line"
  jq -e 'type == "object"' "$w/status.json" > /dev/null \
    || fail "status --json printed something other than one JSON object"
}

# json <state> [<jq condition>...]: topic's state through --json, the
# fields every object carries, and each condition given. The ids in a
# condition are hex, so they are written into it directly.
json() {
  local want="$1" tip got cond
  shift
  status_json
  tip="$(git -C "$origin" rev-parse refs/heads/main)"
  got="$(jq -r .state "$w/status.json")"
  [ "$got" = "$want" ] || fail "status --json reported $got where $want was expected"
  for cond in '.schema == "valley-status/v1"' '.change == "topic"' \
    '.request_name == "topic"' '.target == "main"' ".target_tip == \"$tip\"" "$@"; do
    jq -e "$cond" "$w/status.json" > /dev/null || fail "status --json: $cond does not hold"
  done
}

# A read that cannot give a status prints no object at all: a front end
# must never parse half of one, or an empty one, as an answer.
json_refused() {
  if (cd "$repo" && valley status --json "${1:-topic}") > "$w/status.json" 2> "$w/status.err"; then
    fail "status --json succeeded where it had to refuse"
  fi
  [ ! -s "$w/status.json" ] || fail "a refused status --json printed to stdout"
  [ -s "$w/status.err" ] || fail "a refused status --json did not say why"
}

# No request is different from a request waiting to be judged.
reset_remote
status
contains 'not approved' 'an unapproved branch was not identified'
lacks 'integrated into origin/main' 'an unapproved branch was reported integrated'
json not-requested ".head == \"$head\"" '.request == null' '.outcome == null'
git -C "$origin" update-ref "$request" "$head"
status
contains 'awaiting' 'a new request was not reported as waiting'
contains 'decision' 'the pending message did not say what is awaited'
json awaiting-decision ".head == \"$head\"" \
  ".request == {\"ref\": \"$request\", \"commit\": \"$head\"}" '.outcome == null'

# A current refusal is useful, but a verdict for an earlier input is not
# the current answer. Changing the target or adding evidence schedules a
# new controller decision without requiring another request.
record_outcome "$head" "$base" stale
status
contains 'needs attention' 'current staleness did not call for attention'
contains 'stale' 'current staleness lost the recorded outcome'
json stale ".outcome == {\"decision\": \"stale\", \"head\": \"$head\", \"tip\": \"$base\", \"failed_at\": null}"
record_outcome "$head" "$base" reject
status
contains 'rejected' 'a current rejection was not reported'
json rejected '.outcome.decision == "reject"' ".request.commit == \"$head\""
# The integrator's refusals: a head that is a version of a change that is
# not ready, and commits that hold a merge. The request stands for both.
record_outcome "$head" "$base" refused-not-ready
status
contains 'did not say ready' 'a refusal for a record that is not ready was not reported'
json refused-not-ready '.outcome.decision == "refused-not-ready"' ".request.commit == \"$head\"" \
  '.change_id == null and .handle == null and .landing == null'
record_outcome "$head" "$base" refused-merge
status
contains 'include a merge' 'a refusal for a merge was not reported'
json refused-merge '.outcome.decision == "refused-merge"'
# A request the integrator voided for a withdrawal is gone, and its outcome
# says why. A request filed again at that head is a fresh approval.
git -C "$origin" update-ref -d "$request"
record_outcome "$head" "$base" voided-withdrawn
status
contains 'voided its request because the change was withdrawn' 'a voided request was not reported'
json voided-withdrawn '.outcome.decision == "voided-withdrawn"' '.request == null'
git -C "$origin" update-ref "$request" "$head"
json awaiting-decision '.outcome.decision == "voided-withdrawn"'
# A stored record that does not read, and that may name the request's
# head, is warned of: the integrator cannot tell whether it holds the head
# back. Only corrupt storage holds one, since the hook refuses it.
broken="$(printf '{}\n' | git -C "$origin" hash-object -w --stdin)"
broken="$(printf '100644 blob %s\trecord.json\n' "$broken" | git -C "$origin" mktree)"
broken="$(git -C "$origin" commit-tree "$broken" -p "$head" -m 'a broken record')"
git -C "$origin" update-ref refs/the-valley/changes/main/kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk "$broken"
status
contains 'changes/main/kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk does not read' 'a record that does not read was not warned of'
contains 'awaiting' 'a record that does not read stopped the status read'
git -C "$origin" update-ref -d refs/the-valley/changes/main/kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk
record_outcome "$head" "$base" 'failed 2026-09-21T12:00:00Z'
status
contains 'failed' 'a controller failure was not reported'
matches 'may.*land|might.*land|cannot.*(rule out|confirm|determine)|does not.*(prove|mean|establish).*land|not.*confirm.*land' \
  'a failure was presented as proof that nothing landed'
json failed '.outcome.decision == "failed"' '.outcome.failed_at == "2026-09-21T12:00:00Z"'

record_outcome "$head" "$base" stale
other_tree="$(git -C "$origin" rev-parse "$base^{tree}")"
advanced="$(printf 'another landing\n' | git -C "$origin" commit-tree "$other_tree" -p "$base")"
git -C "$origin" update-ref refs/heads/main "$advanced"
status
contains 'awaiting' 'a moved target retained the previous refusal'
contains 'new decision' 'a moved target did not explain re-evaluation'
json awaiting-new-decision ".target_tip == \"$advanced\"" ".outcome.tip == \"$base\"" \
  '.outcome.decision == "stale"'

reset_remote
git -C "$origin" update-ref "$request" "$head"
record_outcome "$head" "$base" stale
evidence="$(printf 'new evidence\n' | git -C "$origin" hash-object -w --stdin)"
git -C "$origin" update-ref "$evidence_ref" "$evidence"
status
contains 'awaiting' 'new evidence retained the previous refusal'
contains 'new decision' 'new evidence did not explain re-evaluation'
json awaiting-new-decision ".target_tip == \"$base\"" '.outcome.decision == "stale"'

# A reused change name must not carry an earlier commit's rejection or
# landing forward to its replacement.
reset_remote
git -C "$origin" update-ref "$request" "$head"
record_outcome "$base" "$base" reject
status
contains 'awaiting' 'an earlier head verdict was applied to the current request'
lacks 'needs attention' 'an earlier rejection was treated as current'
json awaiting-decision ".head == \"$head\"" ".outcome.head == \"$base\"" \
  '.outcome.decision == "reject"'
git -C "$origin" update-ref -d "$request"
record_outcome "$base" "$base" land
status
contains 'not approved' 'an old landing was applied to a newer branch head'
lacks 'integrated into origin/main' 'the newer branch inherited an old landing'
json not-requested ".head == \"$head\"" '.request == null' \
  ".outcome.head == \"$base\"" '.outcome.decision == "land"'

# Main is authoritative for direct landings. A post-commit failure may
# leave both a failed memo and an unconsumed request behind.
reset_remote
git -C "$origin" update-ref refs/heads/main "$head"
git -C "$origin" update-ref "$request" "$head"
record_outcome "$head" "$base" 'failed 2026-09-21T12:00:00Z'
status
contains 'integrated into origin/main' 'a failed bookkeeping step hid an actual landing'
contains 'git pull --ff-only origin main' 'a behind local main had no update instruction'
json integrated ".head == \"$head\"" ".request.commit == \"$head\"" \
  '.outcome.decision == "failed"'
git -C "$origin" update-ref -d "$request"
record_outcome "$head" "$base" land
status
contains 'integrated into origin/main' 'a consumed request hid a direct landing'
json integrated ".head == \"$head\"" '.request == null' '.outcome.decision == "land"'

# Reparenting changes the landed commit id. The matching durable outcome
# still identifies the change that was approved, including after topic
# cleanup.
reset_remote
topic_tree="$(git -C "$origin" rev-parse "$head^{tree}")"
landed="$(printf 'reparented landing\n' | git -C "$origin" commit-tree "$topic_tree" -p "$advanced")"
git -C "$origin" update-ref refs/heads/main "$landed"
record_outcome "$head" "$advanced" land
status
contains 'integrated into origin/main' 'a reparented landing was missed'
json integrated-recorded ".head == \"$head\"" ".target_tip == \"$landed\"" \
  ".outcome.head == \"$head\""
git -C "$origin" update-ref -d refs/heads/topic
status
contains 'integrated into origin/main' 'deleting a landed topic hid its outcome'
json integrated-recorded ".head == \"$head\"" '.request == null'

# A disappeared request without a landing record cannot establish success.
reset_remote
git -C "$origin" update-ref -d refs/heads/topic
if (cd "$repo" && valley status topic) > "$w/status.out" 2>&1; then
  fail 'a nonexistent branch and request were accepted'
fi
matches 'no.*recorded.*request|no.*request.*record|no.*branch.*request' \
  'a missing branch and request had no actionable explanation'
lacks 'integrated into origin/main' 'absence was treated as a landing'
json_refused

# A corrupted memo must not turn a valid pending request into success or
# abort the status read without explaining which record is unreadable.
reset_remote
git -C "$origin" update-ref "$request" "$head"
bad="$(printf 'not an outcome\n' | git -C "$origin" hash-object -w --stdin)"
git -C "$origin" update-ref "$memo" "$bad"
local_state > "$w/before"
(cd "$repo" && valley status topic) > "$w/status.out" 2>&1 || true
local_state > "$w/after"
diff -u "$w/before" "$w/after" || fail 'a malformed memo changed local state'
matches 'malformed|unreadable|invalid|unrecognized|not recognized' \
  'a malformed memo was silently accepted'
lacks 'integrated into origin/main' 'a malformed memo became a successful landing'
# An unreadable record is present and says nothing: no head, tip or time
# from it is reported as though it had been read.
json outcome-unreadable ".head == \"$head\"" \
  '.outcome == {"decision": "unknown", "head": null, "tip": null, "failed_at": null}'

# Status must remain useful with uncommitted work, and its suggestion to
# update main must disappear once the local main already has the landing.
reset_remote
git -C "$origin" update-ref refs/heads/main "$head"
record_outcome "$head" "$base" land
printf 'staged\n' > "$repo/staged.txt"
git -C "$repo" add staged.txt
printf 'unstaged\n' >> "$repo/readme.txt"
status
contains 'integrated into origin/main' 'dirty local work obscured the remote result'
json integrated ".head == \"$head\""
git -C "$repo" reset --quiet --hard "$head"
status
lacks 'git pull --ff-only origin main' 'an up-to-date local main was told to pull'

# On another branch, advice must describe local main rather than assume
# main is at the commit currently checked out.
git -C "$repo" checkout --quiet -b feature "$base"
status
contains 'git switch main' 'an up-to-date local main was not offered for viewing'
lacks 'git pull --ff-only origin main' 'an up-to-date local main was told to pull from a feature checkout'
git -C "$repo" branch --force main "$advanced" >/dev/null
status
contains 'diverged' 'a divergent local main was overlooked from a feature checkout'
lacks 'git pull --ff-only origin main' 'a divergent local main was told to fast-forward'
git -C "$repo" branch --force main "$head" >/dev/null

# A newer topic commit must not be mistaken for the older pending request.
reset_remote
git -C "$origin" update-ref "$request" "$base"
status
contains 'latest commit not approved' 'a newer topic inherited an older request'
contains 'older request' 'an outstanding older request was hidden'
json outdated-request ".head == \"$head\"" ".request.commit == \"$base\""

# A change name is carried exactly. A branch name may hold quotes, the
# characters a shell reads specially, and any space but ASCII's — here a
# no-break space: JSON escapes them, the request name maps only the slash,
# and the command the sentences suggest reads back, in a shell, as the
# same name.
odd='odd/"quoted"&<name>;$(x)'$'\302\240''nbsp'
oddrequest='odd-"quoted"&<name>;$(x)'$'\302\240''nbsp'
git -C "$origin" update-ref "refs/heads/$odd" "$head"
git -C "$origin" update-ref "refs/the-valley/integration-requests/main/$oddrequest" "$head"
status "$odd"
suggested="$(sed -n 's/^Check again: valley status //p' "$w/status.out")"
eval "set -- $suggested"
[ "$#" = 1 ] && [ "$1" = "$odd" ] || fail "the suggested command names something other than $odd"
status_json "$odd"
jq -e --arg change "$odd" --arg name "$oddrequest" --arg head "$head" '
  .change == $change and .request_name == $name and .state == "awaiting-decision"
  and .request == {ref: ("refs/the-valley/integration-requests/main/" + $name), commit: $head}
' "$w/status.json" > /dev/null || fail "status --json did not carry the name $odd exactly"
git -C "$origin" update-ref -d "refs/heads/$odd"
git -C "$origin" update-ref -d "refs/the-valley/integration-requests/main/$oddrequest"

# A change's restack report. status shows the latest report the host
# restacker wrote about a change, and says whether it is current: every ref
# it observed is still what it observed. Those are main, each layer's
# record and branch, a branch it saw missing, and each request that held it
# back. A report whose refs have changed is historical, and pending leaves
# it out.
reset_remote
restack_id=qqqqrrrrssssttttuuuuvvvvwwwwxxxx
record_ref="refs/the-valley/changes/main/$restack_id"
report_ref="refs/the-valley/restack/main/$restack_id"
# one_file <file> <json> <parent…>: a commit on origin holding the JSON as
# the one file given.
one_file() {
  local file="$1" json="$2" blob tree parent
  local -a parents=()
  shift 2
  for parent in "$@"; do parents+=(-p "$parent"); done
  blob="$(printf '%s' "$json" | git -C "$origin" hash-object -w --stdin)"
  tree="$(printf '100644 blob %s\t%s\n' "$blob" "$file" | git -C "$origin" mktree)"
  git -C "$origin" commit-tree "$tree" "${parents[@]}" -m "$file"
}
version_json() {
  printf '{"n": %s, "head": "git-sha1:%s", "title": "topic", "branch": "topic", "uploader": "%s"}' "$@"
}
record_v1="$(one_file record.json "{\"schema\": \"valley-change-record/v1\", \"change_id\": \"$restack_id\", \"target\": \"main\", \"owner\": \"author\", \"state\": \"ready\", \"seq\": 1, \"versions\": [$(version_json 1 "$head" author)], \"active\": 1}" "$head")"
git -C "$origin" update-ref "$record_ref" "$record_v1"
# report_json <result> <layer's published object or null>: a report about
# the change, observed at main's base, its first record, and topic at its
# head.
report_json() {
  local failing=null paths='[]'
  if [ "$1" = conflict ]; then failing="\"$restack_id\"" paths='["readme.txt"]'; fi
  printf '{"schema": "valley-restack-report/v1", "change_id": "%s", "target": "main", "target_tip": "git-sha1:%s", "operation": "op-1", "result": "%s", "layers": [{"change_id": "%s", "record": "git-sha1:%s", "version": 1, "head": "git-sha1:%s", "branch": "topic", "branch_tip": "git-sha1:%s", "published": %s}], "failing_layer": %s, "conflict_paths": %s, "blocking_requests": [], "refusal": null}' \
    "$restack_id" "$base" "$1" "$restack_id" "$record_v1" "$head" "$head" "$2" "$failing" "$paths"
}
conflict="$(one_file report.json "$(report_json conflict null)")"
git -C "$origin" update-ref "$report_ref" "$conflict"

# restack_json <current> [<jq condition>...]: the change's status, through
# --json, carries the report and says whether it is current.
restack_json() {
  local want="$1" cond
  shift
  status_json "$restack_id"
  for cond in ".restack.ref == \"$report_ref\"" ".restack.current == $want" ".change_id == \"$restack_id\"" "$@"; do
    jq -e "$cond" "$w/status.json" > /dev/null || fail "status --json: $cond does not hold"
  done
}
restack_json true ".restack.commit == \"$conflict\"" '.restack.report.result == "conflict"' \
  ".restack.report.failing_layer == \"$restack_id\"" '.restack.report.conflict_paths == ["readme.txt"]' \
  ".restack.report.layers[0].record == \"git-sha1:$record_v1\""
status "$restack_id"
contains "Its restack report: a conflict rebasing qqqq onto main at ${base:0:7}, in readme.txt." \
  'a current conflict was not reported'
contains "valley rebase qqqq" 'a current conflict did not say how to rebase by hand'
lacks 'historical' 'a current report was called historical'
(cd "$repo" && valley changes --json) > "$w/changes.json" || fail 'changes --json failed'
jq -e --arg id "$restack_id" --arg commit "$conflict" '
  .repos[0].changes[] | select(.change_id == $id)
  | .restack.current == true and .restack.commit == $commit and .restack.report.result == "conflict"
' "$w/changes.json" > /dev/null || fail 'changes --json did not carry the current report'
(cd "$repo" && valley pending) > "$w/status.out" || fail 'pending failed'
contains 'restack: a conflict rebasing qqqq onto main' 'pending did not show a current report'

# Each ref it observed changing makes it historical: the branch, the
# branch gone, main, and the record. Status says so, and pending leaves it
# out. historical <what> [<what the report says>]
historical() {
  restack_json false
  status "$restack_id"
  contains "Its last restack report is historical: ${2:-a conflict rebasing}" "$1: a historical report was not called historical"
  lacks 'valley rebase' "$1: a historical conflict was given as one to resolve"
  (cd "$repo" && valley pending) > "$w/status.out" || fail 'pending failed'
  lacks 'restack:' "$1: pending showed a historical report"
}
git -C "$origin" update-ref refs/heads/topic "$base"
historical 'a moved branch'
git -C "$origin" update-ref refs/heads/topic "$head"
git -C "$origin" update-ref refs/heads/main "$advanced"
historical 'a moved main'
git -C "$origin" update-ref refs/heads/main "$base"
record_v1_draft="$(one_file record.json "{\"schema\": \"valley-change-record/v1\", \"change_id\": \"$restack_id\", \"target\": \"main\", \"owner\": \"author\", \"state\": \"draft\", \"seq\": 2, \"versions\": [$(version_json 1 "$head" author)], \"active\": 1}" "$record_v1")"
git -C "$origin" update-ref "$record_ref" "$record_v1_draft"
historical 'a moved record'
git -C "$origin" update-ref "$record_ref" "$record_v1"
restack_json true
git -C "$origin" update-ref -d refs/heads/topic
restack_json false
git -C "$origin" update-ref refs/heads/topic "$head"

# A complete report is matched against what it published: the head it
# moved the branch to, and the record that recorded it. It is current once
# both stand on origin, and historical while the branch is at the head it
# was moved from.
rebased="$(git -C "$origin" commit-tree "$head^{tree}" -p "$base" -m 'topic, rebased')"
record_v2="$(one_file record.json "{\"schema\": \"valley-change-record/v1\", \"change_id\": \"$restack_id\", \"target\": \"main\", \"owner\": \"author\", \"state\": \"ready\", \"seq\": 2, \"versions\": [$(version_json 1 "$head" author), $(version_json 2 "$rebased" restacker)], \"active\": 2}" "$record_v1" "$rebased")"
complete="$(one_file report.json "$(report_json complete "{\"head\": \"git-sha1:$rebased\", \"version\": 2, \"record\": \"git-sha1:$record_v2\"}")" "$conflict")"
git -C "$origin" update-ref "$report_ref" "$complete"
git -C "$origin" update-ref "$record_ref" "$record_v2"
restack_json false ".restack.commit == \"$complete\"" '.restack.report.result == "complete"'
git -C "$origin" update-ref refs/heads/topic "$rebased"
restack_json true '.restack.report.layers[0].published.version == 2' ".version == 2"
status "$restack_id"
contains "Its restack report: rebased onto main at ${base:0:7}." 'a current complete report was not reported'
git -C "$origin" update-ref "$record_ref" "$record_v1"
restack_json false
git -C "$origin" update-ref refs/heads/topic "$head"

# A report that saw the branch missing records a null branch_tip. It stays
# current while the branch is missing, and is historical once anyone
# creates the branch, even at the head the record names.
git -C "$origin" update-ref -d refs/heads/topic
missing="$(one_file report.json "$(report_json blocked null | jq -c '
  .layers[0].branch_tip = null
  | .refusal = "qqqq is on the branch topic, and origin has no such branch; nothing rebased"')" "$complete")"
git -C "$origin" update-ref "$report_ref" "$missing"
restack_json true '.restack.report.layers[0].branch == "topic"' '.restack.report.layers[0].branch_tip == null'
status "$restack_id"
contains "Its restack report: held before rebasing onto main at ${base:0:7}: qqqq is on the branch topic, and origin has no such branch; nothing rebased." \
  'a current report of a missing branch was not reported'
(cd "$repo" && valley pending) > "$w/status.out" || fail 'pending failed'
contains 'restack: held before rebasing onto main' 'pending did not show a current report of a missing branch'
git -C "$origin" update-ref refs/heads/topic "$head"
historical 'a missing branch created' 'held before rebasing'

# A version that names no branch has no branch to observe. Its layer's
# branch is empty and its branch_tip null, and the report is current while
# main and the record stand, wherever any branch is.
bare_version="{\"n\": 1, \"head\": \"git-sha1:$head\", \"title\": \"topic\", \"branch\": \"\", \"uploader\": \"author\"}"
record_bare="$(one_file record.json "{\"schema\": \"valley-change-record/v1\", \"change_id\": \"$restack_id\", \"target\": \"main\", \"owner\": \"author\", \"state\": \"ready\", \"seq\": 1, \"versions\": [$bare_version], \"active\": 1}" "$head")"
git -C "$origin" update-ref "$record_ref" "$record_bare"
bare="$(one_file report.json "$(report_json blocked null | jq -c --arg record "git-sha1:$record_bare" '
  .layers[0].record = $record | .layers[0].branch = "" | .layers[0].branch_tip = null
  | .refusal = "qqqq records no branch; nothing rebased"')" "$missing")"
git -C "$origin" update-ref "$report_ref" "$bare"
restack_json true '.restack.report.layers[0].branch == ""' '.restack.report.layers[0].branch_tip == null' \
  ".restack.report.layers[0].record == \"git-sha1:$record_bare\""
status "$restack_id"
contains "Its restack report: held before rebasing onto main at ${base:0:7}: qqqq records no branch; nothing rebased." \
  'a current report of a version with no branch was not reported'
git -C "$origin" update-ref -d refs/heads/topic
restack_json true
git -C "$origin" update-ref refs/heads/topic "$head"
git -C "$origin" update-ref "$record_ref" "$record_v1"
historical 'the record of a version with no branch moved' 'held before rebasing'

# A block names each request that held it back, at the tip it read. It is
# current while every one of them stays there, and historical once one is
# deleted or moved to another head.
request_ref=refs/the-valley/integration-requests/main/topic
git -C "$origin" update-ref "$request_ref" "$head"
blocked="$(one_file report.json "$(report_json blocked null | jq -c --arg ref "$request_ref" --arg tip "git-sha1:$head" '
  .blocking_requests = [{ref: $ref, tip: $tip}]')" "$bare")"
git -C "$origin" update-ref "$report_ref" "$blocked"
restack_json true ".restack.report.blocking_requests == [{\"ref\": \"$request_ref\", \"tip\": \"git-sha1:$head\"}]"
status "$restack_id"
contains "Its restack report: held before rebasing onto main at ${base:0:7} by the request topic." \
  'a current block was not reported with its request'
git -C "$origin" update-ref -d "$request_ref"
historical 'a deleted blocking request' "held before rebasing onto main at ${base:0:7} by the request topic"
git -C "$origin" update-ref "$request_ref" "$head"
restack_json true
git -C "$origin" update-ref "$request_ref" "$advanced"
historical 'a retargeted blocking request' "held before rebasing onto main at ${base:0:7} by the request topic"
git -C "$origin" update-ref -d "$request_ref"
git -C "$origin" update-ref -d "$record_ref"
git -C "$origin" update-ref -d "$report_ref"

# A branch no record is on has no restack report to show.
status_json topic
jq -e '.restack == null' "$w/status.json" > /dev/null || fail 'a branch status carried a restack report'

# A target with no main is no answer either.
git -C "$origin" update-ref -d refs/heads/main
json_refused
grep -q 'no main branch' "$w/status.err" || fail 'a missing main had no useful diagnostic'
git -C "$origin" update-ref refs/heads/main "$base"

# Connectivity failures are not an empty pending queue. These cases do not
# require one particular exit convention, only a diagnostic and no claim
# about a successfully inspected remote.
git -C "$repo" remote set-url origin "$w/missing.git"
(cd "$repo" && valley status topic) > "$w/status.out" 2>&1 || true
matches 'origin|remote|fetch|repository' 'an unavailable origin had no useful diagnostic'
lacks 'integrated into origin/main' 'a failed fetch reported cached success'
lacks 'not approved' 'a failed fetch was mistaken for an absent request'
json_refused
grep -q 'could not read origin' "$w/status.err" || fail 'an unreadable origin was not named as such'
git -C "$repo" remote remove origin
(cd "$repo" && valley status topic) > "$w/status.out" 2>&1 || true
matches 'origin|remote' 'a missing origin had no useful diagnostic'
lacks 'integrated into origin/main' 'a missing origin reported cached success'
json_refused

echo 'valley-status: remote outcomes and local state checks passed'
touch "$out"
