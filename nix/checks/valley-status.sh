# shellcheck shell=bash
# The status command reads an asynchronous integration result. Exercise it
# against real refs, including the gaps between updating main, recording an
# outcome, and consuming a request. No controller or timing assumption is
# needed to make those states observable.
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

# No request is different from a request waiting to be judged.
reset_remote
status
contains 'not submitted' 'an unsubmitted branch was not identified'
lacks 'integrated into origin/main' 'an unsubmitted branch was reported integrated'
git -C "$origin" update-ref "$request" "$head"
status
contains 'awaiting' 'a new request was not reported as waiting'
contains 'decision' 'the pending message did not say what is awaited'

# A current refusal is useful, but a verdict for an earlier input is not
# the current answer. Changing the target or adding evidence schedules a
# new controller decision without requiring another request.
record_outcome "$head" "$base" stale
status
contains 'needs attention' 'current staleness did not call for attention'
contains 'stale' 'current staleness lost the recorded outcome'
record_outcome "$head" "$base" reject
status
contains 'rejected' 'a current rejection was not reported'
record_outcome "$head" "$base" 'failed 2026-09-21T12:00:00Z'
status
contains 'failed' 'a controller failure was not reported'
matches 'may.*land|might.*land|cannot.*(rule out|confirm|determine)|does not.*(prove|mean|establish).*land|not.*confirm.*land' \
  'a failure was presented as proof that nothing landed'

record_outcome "$head" "$base" stale
other_tree="$(git -C "$origin" rev-parse "$base^{tree}")"
advanced="$(printf 'another landing\n' | git -C "$origin" commit-tree "$other_tree" -p "$base")"
git -C "$origin" update-ref refs/heads/main "$advanced"
status
contains 'awaiting' 'a moved target retained the previous refusal'
contains 'new decision' 'a moved target did not explain re-evaluation'

reset_remote
git -C "$origin" update-ref "$request" "$head"
record_outcome "$head" "$base" stale
evidence="$(printf 'new evidence\n' | git -C "$origin" hash-object -w --stdin)"
git -C "$origin" update-ref "$evidence_ref" "$evidence"
status
contains 'awaiting' 'new evidence retained the previous refusal'
contains 'new decision' 'new evidence did not explain re-evaluation'

# A reused change name must not carry an earlier commit's rejection or
# landing forward to its replacement.
reset_remote
git -C "$origin" update-ref "$request" "$head"
record_outcome "$base" "$base" reject
status
contains 'awaiting' 'an earlier head verdict was applied to the current request'
lacks 'needs attention' 'an earlier rejection was treated as current'
git -C "$origin" update-ref -d "$request"
record_outcome "$base" "$base" land
status
contains 'not submitted' 'an old landing was applied to a newer branch head'
lacks 'integrated into origin/main' 'the newer branch inherited an old landing'

# Main is authoritative for direct landings. A post-commit failure may
# leave both a failed memo and an unconsumed request behind.
reset_remote
git -C "$origin" update-ref refs/heads/main "$head"
git -C "$origin" update-ref "$request" "$head"
record_outcome "$head" "$base" 'failed 2026-09-21T12:00:00Z'
status
contains 'integrated into origin/main' 'a failed bookkeeping step hid an actual landing'
contains 'git pull --ff-only origin main' 'a behind local main had no update instruction'
git -C "$origin" update-ref -d "$request"
record_outcome "$head" "$base" land
status
contains 'integrated into origin/main' 'a consumed request hid a direct landing'

# Reparenting changes the landed commit id. The matching durable outcome
# still identifies the submitted change, including after topic cleanup.
reset_remote
topic_tree="$(git -C "$origin" rev-parse "$head^{tree}")"
landed="$(printf 'reparented landing\n' | git -C "$origin" commit-tree "$topic_tree" -p "$advanced")"
git -C "$origin" update-ref refs/heads/main "$landed"
record_outcome "$head" "$advanced" land
status
contains 'integrated into origin/main' 'a reparented landing was missed'
git -C "$origin" update-ref -d refs/heads/topic
status
contains 'integrated into origin/main' 'deleting a landed topic hid its outcome'

# A disappeared request without a landing record cannot establish success.
reset_remote
git -C "$origin" update-ref -d refs/heads/topic
if (cd "$repo" && valley status topic) > "$w/status.out" 2>&1; then
  fail 'a nonexistent branch and request were accepted'
fi
matches 'no.*recorded.*request|no.*request.*record|no.*branch.*request' \
  'a missing branch and request had no actionable explanation'
lacks 'integrated into origin/main' 'absence was treated as a landing'

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
contains 'latest commit not submitted' 'a newer topic inherited an older request'
contains 'older request' 'an outstanding older request was hidden'

# Connectivity failures are not an empty pending queue. These cases do not
# require one particular exit convention, only a diagnostic and no claim
# about a successfully inspected remote.
git -C "$repo" remote set-url origin "$w/missing.git"
(cd "$repo" && valley status topic) > "$w/status.out" 2>&1 || true
matches 'origin|remote|fetch|repository' 'an unavailable origin had no useful diagnostic'
lacks 'integrated into origin/main' 'a failed fetch reported cached success'
lacks 'not submitted' 'a failed fetch was mistaken for an absent request'
git -C "$repo" remote remove origin
(cd "$repo" && valley status topic) > "$w/status.out" 2>&1 || true
matches 'origin|remote' 'a missing origin had no useful diagnostic'
lacks 'integrated into origin/main' 'a missing origin reported cached success'

echo 'valley-status: remote outcomes and local state checks passed'
touch "$out"
