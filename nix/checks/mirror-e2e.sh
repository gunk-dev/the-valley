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

# A primary repo wired exactly as valley-init wires it, with
# the real store paths followed from the rendered init script.
dispatch="$(grep -o '/nix/store/[^ ]*-valley-post-receive' "$initScriptPath" | head -n1)"
mhook="$(grep -o '/nix/store/[^ ]*-valley-mirrors-mirror-pilot' "$initScriptPath" | head -n1)"
deadhook="$(grep -o '/nix/store/[^ ]*-valley-mirrors-dead-mirror' "$initScriptPath" | head -n1)"
test -x "$dispatch" && test -x "$mhook" && test -x "$deadhook"
wire() {
  mkdir -p "$1/hooks/post-receive.d"
  ln -s "$dispatch" "$1/hooks/post-receive"
  ln -s "$2" "$1/hooks/post-receive.d/valley-mirrors"
}

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
# and a tag deleted. The hook replicates asynchronously, so
# wait on main's new tip before judging the rest.
git -C work commit --quiet --allow-empty -m two
git -C work tag -d doomed >/dev/null
git -C work branch idea/three
git -C work push --quiet --follow-tags origin main idea/three
git -C work push --quiet --delete origin doomed
tip="$(git -C work rev-parse HEAD)"
wait_for mirror_main_is

# main and the tags are published; nothing else is, and the
# topic branches that were on the mirror are gone. The sweep
# is a second push, so it converges just after main lands.
wait_for only_main
[ "$(tags)" = refs/tags/v1 ]

# A branch that appears on the mirror by any other route is
# unpublished on the next push.
git -C mirror.git branch sneaky main
git -C work commit --quiet --allow-empty -m three
git -C work push --quiet origin main
tip="$(git -C work rev-parse HEAD)"
wait_for mirror_main_is
wait_for only_main

# An unreachable mirror costs a log line, never the push: the
# hook must return promptly and the push must succeed.
git init --quiet --bare dead-mirror.git
wire dead-mirror.git "$deadhook"
git clone --quiet "$PWD/dead-mirror.git" deadwork
git -C deadwork commit --quiet --allow-empty -m one
timeout 60 git -C deadwork push --quiet origin main
[ "$(git -C dead-mirror.git rev-parse main)" = "$(git -C deadwork rev-parse HEAD)" ]

# Two publishers of one project. git reads the local refs before it
# contacts the mirror, so a publisher that stalls between the two can
# force-push the old main it read over a newer one. The race mirror is
# an ssh URL, and this stand-in for ssh runs the remote git here. The
# first connection made while `hold` exists takes it and waits for `go`:
# that stalls one publisher in the window, every time.
racehook="$(grep -o '/nix/store/[^ ]*-valley-mirrors-race' "$initScriptPath" | head -n1)"
pusher="$(grep -o '/nix/store/[^ ]*-valley-mirror-push-race' "$racehook" | head -n1)"
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
git init --quiet --bare race-mirror.git
git -C race.git config core.sshCommand "$PWD/fake-ssh"
git -C race.git config ssh.variant simple
git init --quiet racework
git -C racework commit --quiet --allow-empty -m old
git -C racework commit --quiet --allow-empty -m new
old="$(git -C racework rev-parse HEAD~1)"
new="$(git -C racework rev-parse HEAD)"
# A fetch runs no hook, so the commits arrive unpublished.
git -C race.git fetch --quiet ../racework "+$new:refs/heads/main"

set_main() { git -C race.git update-ref refs/heads/main "$1"; }
race_main_is() { [ "$(git -C race-mirror.git rev-parse --verify --quiet main)" = "$1" ]; }
race_reset() {
  rm -f hold held go
  git -C race-mirror.git update-ref -d refs/heads/main 2>/dev/null || true
  set_main "$old"
}
# The pusher alone, as an unserialized publisher runs it.
unlocked() { (cd race.git && "$pusher"); }
# The real hook, as post-receive runs it: detached, under the lock.
hooked() { (cd race.git && "$racehook" </dev/null 8>&-); }
lock_free() { flock -n race.git/valley-publish.flock true; }

# The negative control. Without the lock, the publisher that read the
# old main stalls, the other pushes the new main, and the first then
# rewinds the mirror to the old one.
race_reset
touch hold
unlocked &
stalled=$!
wait_for test -e held
set_main "$new"
unlocked
race_main_is "$new"
touch go
wait "$stalled"
race_main_is "$old"

# With the lock, the same ordering ends at the new main. The first
# publisher stalls holding the lock; the second, started after main
# moved, waits for it and pushes last.
race_reset
touch hold
hooked
wait_for test -e held
set_main "$new"
hooked
sleep 1
race_main_is ""
touch go
wait_for race_main_is "$new"
wait_for lock_free
race_main_is "$new"

# A publisher takes the lock before it reads main. Hold the lock here,
# start a publisher, and move main while it waits: it publishes the
# main it finds once it holds the lock.
race_reset
exec 8>>race.git/valley-publish.flock
flock 8
hooked
sleep 1
race_main_is ""
set_main "$new"
flock -u 8
exec 8>&-
wait_for race_main_is "$new"
wait_for lock_free
race_main_is "$new"

touch "$out"
