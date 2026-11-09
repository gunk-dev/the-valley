# shellcheck shell=bash
# valley review with no change, end to end and unmocked, over the
# projects one test host serves: the walk through what needs the operator,
# across projects, in landing order, through the packaged CLI. The host is
# made by the integrator host's rendered valley-init, as valley-changes
# makes it, so discovery reads the project list a host publishes and every
# record and request is pushed through the hook that host installs.
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
set -euo pipefail

w="$TMPDIR/valley-walk"
mkdir -p "$w"
export HOME="$w" XDG_CACHE_HOME="$w/cache" XDG_STATE_HOME="$w/state" TERM=xterm
export GIT_CONFIG_NOSYSTEM=1
# Nobody is anybody by default: a commit made where no identity is set
# fails, so a review in a cache clone commits only with the identity the
# walk lends it from the checkout.
git config --global user.useConfigOnly true
git config --global init.defaultBranch main
git config --global advice.detachedHead false
unset VALLEY_KNOWN_SIGNERS VALLEY_INTEGRATOR_SIGNER VISUAL || true

fail() {
  printf 'valley-walk: %s\n' "$*" >&2
  for f in "$w"/out "$w"/err; do
    if [ -s "$f" ]; then cat "$f" >&2; fi
  done
  exit 1
}

ok() {
  "$@" > "$w/out" 2> "$w/err" || fail "$* failed"
}

line() {
  grep -qE -- "$1" "$w/out" || fail "no line matches $1"
}

no_line() {
  if grep -qE -- "$1" "$w/out"; then fail "a line matches $1"; fi
}

# before <ERE> <ERE>: the first line matching the first comes before the
# first line matching the second.
before() {
  local a b
  a="$(grep -nE -m 1 -- "$1" "$w/out" | cut -d: -f1 || true)"
  b="$(grep -nE -m 1 -- "$2" "$w/out" | cut -d: -f1 || true)"
  [ -n "$a" ] && [ -n "$b" ] && [ "$a" -lt "$b" ] || fail "$1 does not come before $2"
}

# walk <status> <answers> [<flag>...]: valley review with no change, from
# the open checkout, answering from a pipe; it exits with <status>.
walk() {
  local want="$1" answers="$2" status=0
  shift 2
  printf '%b' "$answers" | valley review "$@" > "$w/out" 2> "$w/err" || status=$?
  [ "$status" = "$want" ] || fail "the walk exited $status, not $want"
}

# wait_for <file> <ERE>: wait until the file shows a line matching it. The
# walk's output is a file a child writes, which a shell has no event for,
# so it is read again every tenth of a second, for a minute at most.
wait_for() {
  local i
  for i in $(seq 600); do
    if grep -qE -- "$2" "$1" 2> /dev/null; then return 0; fi
    sleep 0.1
  done
  fail "$1 never showed $2"
}

# The host: guarded, open, released and sealed, with sealed the instance
# repository, as valley-changes makes it. sealed's main carries the floor
# approving derives the policy from.
data="$w/srv/git"
mkdir -p "$data"
sed "s|/srv/git|$data|g" "$initScriptPath" > "$w/init.sh"
bash "$w/init.sh" 2> "$w/init.err" || fail "valley-init failed: $(cat "$w/init.err")"
instance="$data/sealed.git"
for name in guarded open released sealed; do
  git -C "$data/$name.git" config --unset core.sharedRepository || true
done
git init --quiet "$w/floor"
mkdir -p "$w/floor/policy/instance"
cp "$floor"/*.cue "$w/floor/policy/instance/"
git -C "$w/floor" add policy
git -C "$w/floor" -c user.name=integrator -c user.email=integrator@localhost commit --quiet -m floor
git -C "$w/floor" push --quiet "$instance" HEAD:refs/heads/floor
git -C "$instance" update-ref refs/heads/main "$(git -C "$w/floor" rev-parse HEAD)"

id() { printf '%s%s' "$1" "$(printf 'k%.0s' $(seq $((32 - ${#1}))))"; }
G1="$(id kmkm)" G2="$(id knkn)" O1="$(id popo)" S1="$(id sqsq)" S2="$(id srsr)"
M1="$(id mlml)" M2="$(id mnmn)" M3="$(id mpmp)"

# A checkout of a project whose main names sealed as its owning valley,
# with alice's identity in its own configuration alone.
export VALLEY_PRINCIPAL=alice
checkout() {
  local dir="$w/$1"
  git clone --quiet "$data/$1.git" "$dir" 2> /dev/null
  git -C "$dir" config valley.principal alice
  git -C "$dir" config user.name alice
  git -C "$dir" config user.email alice@example.org
  mkdir -p "$dir/policy"
  printf '%s\n' "$instance" > "$dir/policy/valley"
  git -C "$dir" add policy/valley
  git -C "$dir" commit --quiet -m base
  VALLEY_PRINCIPAL=integrator git -C "$dir" push --quiet origin main
}

# branch <name> <from> <subject…>: a branch with a commit per subject,
# pushed to origin.
branch() {
  local name="$1" from="$2" s
  shift 2
  git checkout --quiet -b "$name" "$from"
  for s in "$@"; do
    echo "$s" > "$s.txt"
    git add "$s.txt"
    git commit --quiet -m "$s"
  done
  git push --quiet origin "$name"
}

# ready <id> <branch>: the branch readied as the change, each a second
# after the last, so the landing order is the order they are readied in.
at=1700000000
ready() {
  at=$((at + 1))
  GIT_COMMITTER_DATE="@$at +0000" ok valley ready --change "$1" "$2"
}

checkout open
checkout guarded
cd "$w/guarded"
branch g-one main g-one
ready "$G1" g-one
branch g-two main g-two
ready "$G2" g-two
cd "$w/open"
branch o-one main o-one
ready "$O1" o-one

# ----------------------------------------------------------------------
# Three ready changes in two projects, guarded's read through the walk's
# cache clone: the first approved and the second rejected there, the
# third skipped. Each step is headed by its place, project, change and
# checks, and the summary names every outcome, then what still needs the
# operator: the rejected change's record is still ready.
walk 0 'a\nr\nyes\ns\n'
line '^needs you \(3, in landing order\)$'
before '^\[1/3\] guarded · kmkm v1 · 1 check pending$' '^\[2/3\] guarded · knkn v1 · 1 check pending$'
before '^\[2/3\] guarded · knkn v1' '^\[3/3\] open · popo v1 · 1 check pending$'
line 'rejected g-two: deleted from origin$'
line '^  ✓ guarded kmkm v1  approved$'
line '^  ✓ guarded knkn v1  rejected · branch deleted$'
line '^  ◇ open popo v1  skipped$'
line '^still needs you \(2\): guarded knkn, open popo$'
no_line 'nothing needs you'
[ "$(git -C "$data/guarded.git" rev-parse "refs/the-valley/integration-requests/main/c-$G1")" = \
  "$(git -C "$w/guarded" rev-parse g-one)" ] || fail "the approval filed no request at kmkm's head"
if git -C "$data/guarded.git" rev-parse --verify --quiet refs/heads/g-two > /dev/null; then
  fail "the rejection left g-two on origin"
fi
clone="$(echo "$XDG_CACHE_HOME"/valley/repos/*-guarded)"
[ -d "$clone" ] || fail "guarded was read through no cache clone"
for key in valley.principal user.name user.email; do
  if git -C "$clone" config --local --get "$key" > /dev/null; then fail "the walk wrote $key into the cache clone"; fi
done

# ----------------------------------------------------------------------
# A stale stack in guarded, based from the walk with [b]ase onto main: the
# rebase commits and writes records in the cache clone, as alice, with the
# identity the walk lent it. The rejected change before it is not
# reviewed, since its branch is gone, and the walk goes on.
cd "$w/guarded"
git checkout --quiet main
branch s-one main s-one
ready "$S1" s-one
branch s-two s-one s-two
ready "$S2" s-two
git checkout --quiet main
echo moved > moved.txt
git add moved.txt
git commit --quiet -m "main moves"
VALLEY_PRINCIPAL=integrator git push --quiet origin main
cd "$w/open"
walk 0 'b\nn\ns\n' --repo guarded
line '^needs you \(2, in landing order\)$'
line '^\[2/2\] guarded · srsr v1 · stack of 2 · 1 check pending$'
line '^knkn is not reviewed: its branch is gone from origin$'
line '^  ⊘ guarded knkn v1  not reviewed · its branch is gone from origin$'
line '^  ◇ guarded srsr v1  skipped$'
tip="$(git -C "$data/guarded.git" rev-parse main)"
for b in s-one s-two; do
  git -C "$data/guarded.git" merge-base --is-ancestor "$tip" "$b" || fail "the base left $b off main"
done
cd "$w/guarded"
ok valley-go resolve "$S2"
[ "$(cut -f4 "$w/out")" = 2 ] || fail "the base wrote no second version of srsr: $(cat "$w/out")"

# ----------------------------------------------------------------------
# Changes that move after the walk read its queue: while popo's prompt
# waits, mlml is unreadied and mnmn readied at a second version. mlml is
# not reviewed, and mnmn is said to have moved, then reviewed and approved
# at its new version.
cd "$w/open"
branch m-one main m-one
ready "$M1" m-one
branch m-two main m-two
ready "$M2" m-two
branch m-three main m-three
ready "$M3" m-three
rm -f "$w/fifo"
mkfifo "$w/fifo"
valley review --repo open < "$w/fifo" > "$w/walk.out" 2> "$w/walk.err" &
walker=$!
exec 3> "$w/fifo"
wait_for "$w/walk.out" '\[s\]kip'
ok valley unready "$M1"
git checkout --quiet m-two
echo again > m-two.txt
git commit --quiet -am "m-two again"
git push --quiet origin m-two
ready "$M2" m-two
printf 's\n' >&3
wait_for "$w/walk.out" 'moved to v2'
printf 'a\ns\n' >&3
exec 3>&-
status=0
wait "$walker" || status=$?
mv "$w/walk.out" "$w/out"
mv "$w/walk.err" "$w/err"
[ "$status" = 0 ] || fail "the walk with moving changes exited $status"
line '^\[2/4\] open · mlml v1'
line '^mlml is not reviewed: it is a draft$'
line '^mnmn moved to v2 since the queue was read$'
line '^  ⊘ open mlml v1  not reviewed · it is a draft$'
line '^  ✓ open mnmn v1  approved v2$'
line '^  ◇ open mpmp v1  skipped$'
[ "$(git -C "$data/open.git" rev-parse "refs/the-valley/integration-requests/main/c-$M2")" = "$(git rev-parse m-two)" ] ||
  fail "mnmn was not approved at its second version"

# ----------------------------------------------------------------------
# Ctrl-C at a step's prompt, on a terminal: the step is interrupted, the
# summary so far is printed, and the walk exits 130. The diff is paged
# first, by a less that quits since it fits on one screen.
git config pager.diff 'less -FX'
status=0
# On a terminal the prompt's keys are styled, so its plain tail is waited
# on. A walk that never gets there is ended rather than left waiting.
{
  wait_for "$w/session.log" '\]kip: '
  printf '\003'
  wait_for "$w/session.log" 'walked'
} | timeout 300 script -qec "valley review --repo open" /dev/null > "$w/session.log" 2>&1 || status=$?
sed -e 's/\x1b\[[0-9;?]*[a-zA-Z]//g' -e 's/\r//g' "$w/session.log" > "$w/out"
[ "$status" = 130 ] || fail "the interrupted walk exited $status, not 130: $(cat "$w/out")"
line '^walked \(1 of 2\)$'
line '^  ◇ open popo v1  interrupted$'
no_line 'still needs you|nothing needs you'
git config --unset pager.diff

# ----------------------------------------------------------------------
# A project that does not read is said, and nothing says that nothing
# needs the operator. An empty queue says that nothing does, with the
# counts pending gives, and exits 0.
mv "$data/released.git" "$data/released.moved"
walk 1 '' --repo released
line '^✗ released did not read: '
no_line 'nothing needs you'
mv "$data/released.moved" "$data/released.git"
ok valley withdraw "$O1"
ok valley withdraw "$M3"
walk 0 '' --repo open
line '^nothing needs you · 1 moving · 1 draft'

touch "$out"
