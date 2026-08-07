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

touch "$out"
