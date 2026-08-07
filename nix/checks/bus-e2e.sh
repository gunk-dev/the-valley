# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
export HOME="$TMPDIR"
export NATS_URL=nats://127.0.0.1:4222
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name valley-check
git config --global user.email valley-check@localhost
git config --global init.defaultBranch main

wait_for() {
  for _ in $(seq 1 150); do
    "$@" >/dev/null 2>&1 && return 0
    sleep 0.2
  done
  echo "bus-e2e: timed out waiting for: $*" >&2
  return 1
}

nats-server --addr 127.0.0.1 --port 4222 --jetstream \
  --store_dir "$TMPDIR/js" &
server_pid=$!

# The real stream-init script the module renders.
bash -eu "$busInitScriptPath"
nats stream info valley >/dev/null

# A bare repo wired exactly as valley-init wires it, with the
# real store paths followed from the rendered init script.
dispatch="$(grep -o '/nix/store/[^ ]*-valley-post-receive' "$initScriptPath" | head -n1)"
bushook="$(grep -o '/nix/store/[^ ]*-valley-bus-events' "$initScriptPath" | head -n1)"
test -x "$dispatch"
test -x "$bushook"
repo="$TMPDIR/events-pilot.git"
git init --quiet --bare "$repo"
mkdir -p "$repo/hooks/post-receive.d"
ln -s "$dispatch" "$repo/hooks/post-receive"
ln -s "$bushook" "$repo/hooks/post-receive.d/valley-bus"

git clone --quiet "$repo" "$TMPDIR/work"
cd "$TMPDIR/work" || exit 1
echo one > file
git add file
git commit --quiet -m one
git push --quiet origin main
first="$(git rev-parse HEAD)"
zeros="$(printf '%040d' 0)"

# Exit criterion 1: the event arrives within seconds, with a
# payload that is exactly the git facts of the push …
msgs_is() { [ "$(nats stream info valley --json | jq .state.messages)" -eq "$1" ]; }
wait_for msgs_is 1
got="$(nats stream get valley 1 --json)"
[ "$(jq -r .subject <<<"$got")" = valley.git.events-pilot.ref-updated ]
payload="$(jq -r .data <<<"$got" | base64 -d)"
expected='{"event":"ref-updated","repo":"events-pilot","ref":"refs/heads/main","old":"'"$zeros"'","new":"'"$first"'"}'
[ "$payload" = "$expected" ]

# … valid against the shipped event schema.
echo "$payload" > "$TMPDIR/payload.json"
cue vet -d '#RefUpdated' "$eventSchema" "$TMPDIR/payload.json"

# … and visible in `valley tail`. Subscribe first, then push.
valley tail > "$TMPDIR/tail.out" 2>&1 &
tail_pid=$!
subscribed() { grep -q 'valley.>' "$TMPDIR/tail.out"; }
wait_for subscribed
echo two > file
git commit --quiet -am two
git push --quiet origin main
second="$(git rev-parse HEAD)"
wait_for msgs_is 2
payload2="$(nats stream get valley 2 --json | jq -r .data | base64 -d)"
expected2='{"event":"ref-updated","repo":"events-pilot","ref":"refs/heads/main","old":"'"$first"'","new":"'"$second"'"}'
[ "$payload2" = "$expected2" ]
tail_saw() { grep -qF "$expected2" "$TMPDIR/tail.out"; }
wait_for tail_saw
kill "$tail_pid" 2>/dev/null || true

# Exit criterion 2: rebuilding the stream from the repo's
# refs is deterministic — two replays from scratch produce
# identical events, landing on the pushed tip with the
# all-zero id as old.
dump_stream() {
  local info first_seq last_seq i
  info="$(nats stream info valley --json)"
  first_seq="$(jq .state.first_seq <<<"$info")"
  last_seq="$(jq .state.last_seq <<<"$info")"
  for i in $(seq "$first_seq" "$last_seq"); do
    nats stream get valley "$i" --json | jq -c '{subject: .subject, data: .data}'
  done
}
nats stream purge valley --force >/dev/null
valley replay "$repo"
wait_for msgs_is 1
run1="$(dump_stream)"
nats stream purge valley --force >/dev/null
valley replay "$repo"
wait_for msgs_is 1
run2="$(dump_stream)"
[ "$run1" = "$run2" ]
replayed="$(jq -r .data <<<"$run1" | base64 -d)"
[ "$replayed" = '{"event":"ref-updated","repo":"events-pilot","ref":"refs/heads/main","old":"'"$zeros"'","new":"'"$second"'"}' ]

# A dead bus never blocks a push: the event is simply lost
# (and rebuildable by replay).
kill "$server_pid"
wait "$server_pid" || true
echo three > file
git commit --quiet -am three
git push --quiet origin main

touch "$out"
