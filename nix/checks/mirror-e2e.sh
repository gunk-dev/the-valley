# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
export HOME="$TMPDIR"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name valley-check
git config --global user.email valley-check@localhost
git config --global init.defaultBranch main
cd "$TMPDIR" || exit 1

wait_for() {
  for _ in $(seq 1 150); do
    "$@" >/dev/null 2>&1 && return 0
    sleep 0.2
  done
  echo "mirror-e2e: timed out waiting for: $*" >&2
  return 1
}
heads() { git -C mirror.git for-each-ref --format='%(refname)' refs/heads; }
tags() { git -C mirror.git for-each-ref --format='%(refname)' refs/tags; }
mirror_main_is() { [ "$(git -C mirror.git rev-parse main)" = "$tip" ]; }
only_main() { [ "$(heads)" = refs/heads/main ]; }
queued() { find "$1/valley-publish-queue" -mindepth 1 -maxdepth 1 | wc -l; }

# A primary repo wired exactly as valley-init wires it, with
# the real store paths followed from the rendered init script.
dispatch="$(grep -o '/nix/store/[^ ]*-valley-post-receive' "$initScriptPath" | head -n1)"
mhook="$(grep -o '/nix/store/[^ ]*-valley-mirrors-mirror-pilot' "$initScriptPath" | head -n1)"
deadhook="$(grep -o '/nix/store/[^ ]*-valley-mirrors-dead-mirror' "$initScriptPath" | head -n1)"
test -x "$dispatch" && test -x "$mhook" && test -x "$deadhook"
wire() {
  mkdir -p "$1/hooks/post-receive.d" "$1/valley-publish-queue"
  ln -s "$dispatch" "$1/hooks/post-receive"
  ln -s "$2" "$1/hooks/post-receive.d/valley-mirrors"
}

# The drains, followed from the rendered publish unit, and one run
# of a project's drain as the unit runs it: in the repository.
drains="$(sed -n 's|^ExecStart=\(.*\)/%i$|\1|p' "$publishUnitPath")"
test -x "$drains/mirror-pilot" && test -x "$drains/dead-mirror" && test -x "$drains/race"
drain() { (cd "$1.git" && "$drains/$1"); }

# The mirror as it stands today: every head and tag of the
# primary, topic branches included. Seeded before the hook is
# wired, so nothing sweeps it out from under the setup.
git init --quiet --bare mirror-pilot.git
git init --quiet --bare mirror.git
git clone --quiet "$PWD/mirror-pilot.git" work
git -C work commit --quiet --allow-empty -m one
git -C work tag v1
git -C work tag doomed
for b in idea/one idea/two; do git -C work branch "$b"; done
git -C work push --quiet origin \
  '+refs/heads/*:refs/heads/*' '+refs/tags/*:refs/tags/*'
git -C mirror-pilot.git push --quiet ../mirror.git \
  '+refs/heads/*:refs/heads/*' '+refs/tags/*:refs/tags/*'
[ "$(heads | wc -l)" -eq 3 ]
wire mirror-pilot.git "$mhook"

# A real push to the primary — a topic branch alongside main,
# and a tag deleted. The hook queues each push and pushes
# nothing; the drain then publishes everything queued at once.
git -C work commit --quiet --allow-empty -m two
git -C work tag -d doomed >/dev/null
git -C work branch idea/three
git -C work push --quiet --follow-tags origin main idea/three
git -C work push --quiet --delete origin doomed
tip="$(git -C work rev-parse HEAD)"
[ "$(queued mirror-pilot.git)" -eq 2 ]
[ "$(git -C mirror.git rev-parse main)" != "$tip" ]
drain mirror-pilot
[ "$(queued mirror-pilot.git)" -eq 0 ]

# main and the tags are published; nothing else is, and the
# topic branches that were on the mirror are gone.
mirror_main_is
only_main
[ "$(tags)" = refs/tags/v1 ]

# A branch that appears on the mirror by any other route is
# unpublished on the next push.
git -C mirror.git branch sneaky main
git -C work commit --quiet --allow-empty -m three
git -C work push --quiet origin main
tip="$(git -C work rev-parse HEAD)"
drain mirror-pilot
mirror_main_is
only_main

# A move that cannot be deleted fails the drain, though the mirror
# push succeeded, so systemd retries it after a delay rather than
# the path unit restarting it at once. A directory in the queue is
# one rm -f cannot delete, whoever runs the check.
mkdir mirror-pilot.git/valley-publish-queue/stuck
touch mirror-pilot.git/valley-publish-queue/stuck/x
if drain mirror-pilot; then
  echo "mirror-e2e: a drain that could not delete a move succeeded" >&2
  exit 1
fi
[ "$(queued mirror-pilot.git)" -eq 1 ]
rm -r mirror-pilot.git/valley-publish-queue/stuck

# An unreachable mirror costs the push nothing: the hook only
# queues, so the push returns promptly and succeeds. The drain
# fails, and the move stays queued for its retry.
git init --quiet --bare dead-mirror.git
wire dead-mirror.git "$deadhook"
git clone --quiet "$PWD/dead-mirror.git" deadwork
git -C deadwork commit --quiet --allow-empty -m one
timeout 60 git -C deadwork push --quiet origin main
[ "$(git -C dead-mirror.git rev-parse main)" = "$(git -C deadwork rev-parse HEAD)" ]
if drain dead-mirror; then
  echo "mirror-e2e: a drain whose mirror push failed succeeded" >&2
  exit 1
fi
[ "$(queued dead-mirror.git)" -eq 1 ]

# Two moves of one project, published while one push stalls. git
# reads the local refs before it contacts the mirror, so a push
# that stalls between the two can force-push the old main it read
# over a newer one. The race mirror is an ssh URL, and this
# stand-in for ssh runs the remote git here. The first connection
# made while `hold` exists takes it and waits for `go`: that stalls
# one push in the window, every time.
racehook="$(grep -o '/nix/store/[^ ]*-valley-mirrors-race' "$initScriptPath" | head -n1)"
pusher="$(grep -o '/nix/store/[^ ]*-valley-mirror-push-race' "$drains/race" | head -n1)"
test -x "$racehook" && test -x "$pusher"
cat >fake-ssh <<EOF
#!$(command -v sh)
if mv "$PWD/hold" "$PWD/held" 2>/dev/null; then
  while [ ! -e "$PWD/go" ]; do sleep 0.1; done
fi
exec sh -c "\$2"
EOF
chmod +x fake-ssh
git init --quiet --bare race.git
mkdir race.git/valley-publish-queue
git init --quiet --bare race-mirror.git
git -C race.git config core.sshCommand "$PWD/fake-ssh"
git -C race.git config ssh.variant simple
git init --quiet racework
git -C racework commit --quiet --allow-empty -m old
git -C racework commit --quiet --allow-empty -m new
old="$(git -C racework rev-parse HEAD~1)"
new="$(git -C racework rev-parse HEAD)"
zero="$(printf '%040d' 0)"
# A fetch runs no hook, so the commits arrive unpublished.
git -C race.git fetch --quiet ../racework "+$new:refs/heads/main"

# Moving main the way a push does: the ref, then the hook.
move_main() {
  git -C race.git update-ref refs/heads/main "$2"
  (cd race.git && printf '%s %s refs/heads/main\n' "$1" "$2" | "$racehook")
}
race_main_is() { [ "$(git -C race-mirror.git rev-parse --verify --quiet main)" = "$1" ]; }
race_reset() {
  rm -f hold held go race.git/valley-publish-queue/*
  git -C race-mirror.git update-ref -d refs/heads/main 2>/dev/null || true
  git -C race.git update-ref refs/heads/main "$old"
}
# The pusher alone, as one of two publishers would run it.
pusher() { (cd race.git && "$pusher"); }

# The negative control: two pushers at once. The one that read
# the old main stalls, the other pushes the new main, and the
# first then rewinds the mirror to the old one.
race_reset
touch hold
pusher &
stalled=$!
wait_for test -e held
git -C race.git update-ref refs/heads/main "$new"
pusher
race_main_is "$new"
touch go
wait "$stalled"
race_main_is "$old"

# The single publisher, in the same ordering. The drain lists the
# queue, git reads the old main, and the push stalls. main moves
# on and its move is queued. The drain pushes the old main and
# deletes only the move it listed, so the newer move stays queued.
# On a host the path unit starts the next run as this one exits,
# never during it, and that run publishes the new main.
race_reset
move_main "$zero" "$old"
touch hold
drain race &
stalled=$!
wait_for test -e held
move_main "$old" "$new"
touch go
wait "$stalled"
race_main_is "$old"
[ "$(queued race.git)" -eq 1 ]
drain race
race_main_is "$new"
[ "$(queued race.git)" -eq 0 ]

touch "$out"
