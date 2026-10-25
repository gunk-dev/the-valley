# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
export HOME="$TMPDIR/home" XDG_STATE_HOME="$TMPDIR/state"
mkdir -p "$HOME"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name valley-check
git config --global user.email valley-check@localhost
git config --global init.defaultBranch main

fail() {
  echo "valley-self-update: $*" >&2
  exit 1
}

# The package prints the version nix stamped on it. The script run
# straight from a checkout says it has none.
[ "$(valley --version)" = "valley $version" ] \
  || fail "valley --version says '$(valley --version)', not 'valley $version'"
bash "$cliScript" --version | grep -q '^valley unstamped' \
  || fail "the bare script claims a stamp: $(bash "$cliScript" --version)"
valley help self-update | grep -q -- '--rollback' \
  || fail "valley help self-update does not describe --rollback"

# The repository self-update follows: a bare one, as git config names it.
upstream="$TMPDIR/upstream.git"
git init --quiet --bare "$upstream"
git clone --quiet "$upstream" "$TMPDIR/work" 2> /dev/null
push() {
  git -C "$TMPDIR/work" commit --quiet --allow-empty -m "$1"
  git -C "$TMPDIR/work" push --quiet origin HEAD:main
  git -C "$TMPDIR/work" rev-parse HEAD
}
git config --global valley.selfUpdate.repo "$upstream"

# A stand-in for nix, first on PATH, since a build has no nix to build
# with. Its build of a commit is a directory holding this package's share/
# and a valley that runs the real, unstamped script under the version
# stamp nix would give that commit. So the smoke test runs the real CLI
# over the policy the package ships. A commit whose message is `broken`
# builds a valley pointed at a schema that does not exist, so its smoke
# test fails at valley checks. The profile is a JSON file of elements.
export STAND_IN="$TMPDIR/nix" UPSTREAM="$upstream" VALLEY_SCRIPT="$valleyScript" VALLEY_PACKAGE="$valley"
mkdir -p "$STAND_IN/bin" "$STAND_IN/store"
echo '{}' > "$STAND_IN/profile.json"
printf '#!%s\n' "$(command -v bash)" > "$STAND_IN/bin/nix"
cat >> "$STAND_IN/bin/nix" <<'EOF'
set -euo pipefail
while [ "${1:-}" = --extra-experimental-features ]; do shift 2; done
profile="$STAND_IN/profile.json"
case "$1" in
  build)
    ref="${*: -1}"
    rev="${ref##*&rev=}"
    rev="${rev%%#*}"
    out="$STAND_IN/store/$rev-valley-0.1.0-${rev:0:7}"
    mkdir -p "$out/bin"
    ln -sfn "$VALLEY_PACKAGE/share" "$out/share"
    schema=""
    if [ "$(git --git-dir="$UPSTREAM" log -1 --format=%s "$rev")" = broken ]; then
      schema="export VALLEY_VERIFICATION_SCHEMA=/nonexistent/verification.cue"
    fi
    printf '#!/bin/sh\nexport VALLEY_VERSION=0.1.0-%s\n%s\nexec %s "$@"\n' \
      "${rev:0:7}" "$schema" "$VALLEY_SCRIPT/bin/valley" > "$out/bin/valley"
    chmod +x "$out/bin/valley"
    echo "$out"
    ;;
  profile)
    case "$2" in
      list) jq '{elements: .}' "$profile" ;;
      remove) jq --arg n "$3" 'del(.[$n])' "$profile" > "$profile.new" ;;
      add)
        if jq -e '.valley' "$profile" > /dev/null; then
          echo "error: a valley is installed already" >&2
          exit 1
        fi
        jq --arg p "$3" '. + {valley: {storePaths: [$p]}}' "$profile" > "$profile.new"
        ;;
    esac
    [ ! -e "$profile.new" ] || mv "$profile.new" "$profile"
    ;;
  *) exit 2 ;;
esac
EOF
chmod +x "$STAND_IN/bin/nix"
export PATH="$STAND_IN/bin:$PATH"
state="$XDG_STATE_HOME/valley/self-update.json"
installed() { jq -r '.valley.storePaths[0] // empty' "$STAND_IN/profile.json"; }

# A first run builds main's tip, smoke-tests it and installs it.
c1="$(push one)"
valley self-update > run.txt || fail "the first run failed: $(cat run.txt)"
grep -q "smoke test passed: valley 0.1.0-${c1:0:7}" run.txt || fail "no smoke test: $(cat run.txt)"
grep -q "installed ${c1:0:7} into the nix profile" run.txt || fail "nothing installed: $(cat run.txt)"
[ "$("$(installed)/bin/valley" --version)" = "valley 0.1.0-${c1:0:7}" ] \
  || fail "the profile holds $(installed)"
[ "$(jq -r .current.commit "$state")" = "$c1" ] || fail "state: $(cat "$state")"

# A second run finds it up to date, quietly unless -v. A repository's own
# git config names no other repository.
[ -z "$(valley self-update)" ] || fail "an up-to-date run printed something"
git -C "$TMPDIR/work" config valley.selfUpdate.repo /elsewhere.git
(cd "$TMPDIR/work" && valley self-update -v) | grep -q "main is at ${c1:0:7}: valley is up to date" \
  || fail "-v does not say valley is up to date"

# A commit whose build fails the smoke test is recorded, installs
# nothing, and is skipped until the branch moves.
c2="$(push broken)"
if valley self-update > run.txt 2>&1; then fail "a broken build installed: $(cat run.txt)"; fi
grep -q "smoke test: valley checks" run.txt || fail "not the smoke test: $(cat run.txt)"
[ "$(jq -r .failed.commit "$state")" = "$c2" ] || fail "no failure recorded: $(cat "$state")"
[ "$(jq -r .current.commit "$state")" = "$c1" ] || fail "state: $(cat "$state")"
valley self-update | grep -q "skipping ${c2:0:7}" || fail "the failed commit was not skipped"

# The home-manager module renders a timer, and a service whose script runs
# the first valley on its PATH with the repository and branch it was
# given.
jq -e '.timers."valley-self-update".Timer == {OnBootSec: "2min", OnUnitActiveSec: "5min"}' \
  "$unitsPath" > /dev/null || fail "the timer: $(cat "$unitsPath")"
jq -r '.services."valley-self-update".Service.Environment[0]' "$unitsPath" \
  | grep -q '^PATH=/home/someone/.nix-profile/bin:/etc/profiles/per-user/someone/bin:' \
  || fail "the service's PATH: $(cat "$unitsPath")"
mkdir -p "$TMPDIR/stub"
printf '#!/bin/sh\nprintf "%%s\\n" "$@"\n' > "$TMPDIR/stub/valley"
chmod +x "$TMPDIR/stub/valley"
PATH="$TMPDIR/stub:$PATH" "$(jq -r '.services."valley-self-update".Service.ExecStart' "$unitsPath")" > args.txt
printf '%s\n' self-update --repo git@valley-host:the-valley --branch main | cmp - args.txt \
  || fail "the service runs valley with: $(cat args.txt)"

touch "$out"
