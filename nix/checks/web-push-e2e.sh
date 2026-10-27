# The push path end to end, over the shipped binaries (web-push-e2e.nix).
# Every server is started through start, which waits a bounded time for
# it to listen, and the trap stops every one still running on any exit.
set -euo pipefail

export HOME="$TMPDIR/home"
export XDG_CACHE_HOME="$TMPDIR/cache"
export XDG_STATE_HOME="$TMPDIR/xdg-state"
export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL="$TMPDIR/gitconfig"
printf '[user]\n\tname = operator\n\temail = operator@example.org\n[init]\n\tdefaultBranch = main\n' > "$GIT_CONFIG_GLOBAL"
cd "$TMPDIR"

fail() {
  echo "web-push-e2e: $*" >&2
  exit 1
}

# The servers still running, stopped on every exit.
pids=()
stop() {
  local pid=$1 rest=() p
  kill -TERM "$pid" 2> /dev/null || true
  for _ in $(seq 150); do
    kill -0 "$pid" 2> /dev/null || break
    sleep 0.1
  done
  kill -KILL "$pid" 2> /dev/null || true
  wait "$pid" 2> /dev/null || true
  for p in "${pids[@]}"; do
    if [ "$p" != "$pid" ]; then rest+=("$p"); fi
  done
  pids=("${rest[@]}")
}
cleanup() {
  for pid in "${pids[@]}"; do stop "$pid"; done
}
trap cleanup EXIT

# start NAME LOG COMMAND...: start a server that prints "NAME: listening
# on ADDR" on stderr once it listens, and wait at most 30 seconds for it.
# It sets pid and addr.
start() {
  local name=$1 log=$2
  shift 2
  "$@" 2> "$log" &
  pid=$!
  pids+=("$pid")
  for _ in $(seq 300); do
    addr=$(sed -n "s/^$name: listening on //p" "$log")
    if [ -n "$addr" ]; then return; fi
    kill -0 "$pid" 2> /dev/null || fail "$name exited before it listened: $(cat "$log")"
    sleep 0.1
  done
  fail "$name did not say where it listens within 30 seconds"
}

# get PATH: valley-web's status and Location for a GET of PATH, with its
# body in $TMPDIR/body. follow PATH: the same after following redirects.
get() {
  curl -sS --max-time 10 -o "$TMPDIR/body" -w '%{http_code} %header{location}' "http://$web$1"
}
follow() {
  curl -sS --max-time 10 --max-redirs 3 -L -o "$TMPDIR/body" -w '%{http_code}' "http://$web$1"
}
# serves PATH TEXT: valley-web serves PATH, after its redirect to the
# tree, with TEXT in the page. gone PATH: it answers 404.
serves() {
  local status
  status=$(follow "$1")
  [ "$status" = 200 ] || fail "valley-web answered $1 with $status, want 200"
  grep -qF "$2" "$TMPDIR/body" || fail "valley-web's page for $1 does not show $2"
}
gone() {
  local status
  status=$(get "$1")
  [ "$status" = "404 " ] || fail "valley-web answered $1 with $status, want 404"
}

# One pass of the publisher as the package wraps it, with its pinned git,
# cue, valley CLI and schemas.
publish() {
  timeout -k 10 300 valley-web-publish --instance-repo "$TMPDIR/srv/valley.git" \
    --instance valley-host --destination example.org --host "$TMPDIR/host.json" \
    --state "$TMPDIR/publisher" --principals "$TMPDIR/principals.json" "$@"
}

commit() {
  git -C "work/$1" add --all
  git -C "work/$1" commit --quiet -m "$1: $2"
  git -C "work/$1" push --quiet origin main
}

# The projects' publication identities, which their policy/read declares
# and the registry's grants name.
declare -A id=(
  [valley]=f83927d5a4ed068cdd02a8cdaa09f26d
  [app]=9da6809f038df92138c131cd16bed76d
  [site]=273d5668702918ea7ae079de374db96b
)
read_policy() {
  printf 'package read\n\nproject: "%s"\nidentity: "%s"\nreaders: {%s}\n' "$1" "${id[$1]}" "$2"
}
# The instance's registry: the operator governs it, and anyone may read
# the projects of the scope given at the edge.
registry() {
  cat << EOF
package identity

boundaries: {
	registry: kind: "registry"
	web: {kind: "edge-read", destination: "example.org"}
}
genesis: "operator"
principals: operator: {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	grants: {
		govern: boundary: "registry"
		read: {boundary: "web", projects: "all"}
	}
}
anyone: grants: read: {boundary: "web", projects: $1}
EOF
}

# 1. The instance and its two projects, each with its own README, and both
# readable by anyone, in the group tools. app has the short alias a, and
# site the former name homepage.
for name in valley app site; do
  git init --quiet --bare "srv/$name.git"
  git init --quiet "work/$name"
  git -C "work/$name" remote add origin "$TMPDIR/srv/$name.git"
  git -C "work/$name" config valley.principal operator
done
mkdir -p work/valley/policy/instance work/valley/policy/read work/valley/identity
echo 'package verification' > work/valley/policy/instance/floor.cue
read_policy valley 'operator: true' > work/valley/policy/read/read.cue
registry "{app: \"${id[app]}\", site: \"${id[site]}\"}" > work/valley/identity/registry.cue
commit valley "the instance"
projects=$(printf 'app\nsite\nvalley\n' | git -C srv/valley.git hash-object -w --stdin)
git -C srv/valley.git update-ref refs/the-valley/projects "$projects"
for name in app site; do
  mkdir -p "work/$name/policy/read"
  echo "$TMPDIR/srv/valley.git" > "work/$name/policy/valley"
  printf '# %s\n\n%s-readme-text\n' "$name" "$name" > "work/$name/README.md"
  read_policy "$name" 'anyone: true, operator: true' > "work/$name/policy/read/read.cue"
  commit "$name" "the first commit"
done
knot=$(cd work/app && valley knot new outcome "Open the edge" --json | jq -r .knot.id)

cat > host.cue << 'EOF'
package valley

groups: core: name: "tools"
projects: app: {group: "core", aliases: ["a"]}
projects: site: {group: "core", formerNames: ["homepage"]}
projects: valley: {}
EOF
cue export --out json "$schema" host.cue > host.json
echo '{"operator": "operator"}' > principals.json

# 2. A pass to a directory makes the export key, whose verifier line the
# receiver pins from its first start. Then the receiver and valley-web
# start over one state directory, and a pass uploads.
publish --out first > /dev/null
test -s publisher/verifier || fail "the first pass made no verifier line"
echo edge-token-0123456789abcdef0123456789 > token
echo another-token-0123456789abcdef0123456 > wrong
receive() {
  start valley-web-receive receive.log valley-web-receive --state edge --listen 127.0.0.1:0 \
    --instance valley-host --destination example.org --verifiers publisher/verifier --token-file token
  receiver_pid=$pid receiver=http://$addr
}
serve() {
  start valley-web web.log valley-web --state edge --listen 127.0.0.1:0
  web_pid=$pid web=$addr
}
receive
publish --receiver "$receiver" --receiver-token token > pass.out
grep -q "the receiver activated sequence" pass.out || fail "the pass did not say the receiver activated it: $(cat pass.out)"
serve
serves /tools/app app-readme-text
serves /tools/site site-readme-text
serves "/tools/app/knots/$knot" "Open the edge"
[ "$(get "/a/knots/$knot?x=1")" = "308 /tools/app/knots/$knot?x=1" ] ||
  fail "the alias a answered $(get "/a/knots/$knot?x=1")"
[ "$(get /tools/homepage/source/)" = "308 /tools/site/source/" ] ||
  fail "the former name homepage answered $(get /tools/homepage/source/)"

# 3. A comment changes the export, so the next pass uploads. With another
# token it is refused as unauthenticated, and the edge keeps serving what
# it had.
(cd work/app && valley knot comment "$knot" -m "edge-comment-text" > /dev/null)
if publish --receiver "$receiver" --receiver-token wrong > pass.out 2> pass.err; then
  fail "a pass with another token succeeded"
fi
grep -q unauthenticated pass.err || fail "a pass with another token failed for another reason: $(cat pass.err)"
serves "/tools/app/knots/$knot" "Open the edge"
if grep -qF edge-comment-text "$TMPDIR/body"; then fail "the refused pass's comment is served"; fi

# 4. Withdrawing anyone's grant for app takes app off the edge on the
# next pass, its alias with it, and site stays.
registry "{site: \"${id[site]}\"}" > work/valley/identity/registry.cue
commit valley "withdraw app"
publish --receiver "$receiver" --receiver-token token > /dev/null
gone /tools/app
gone /a
serves /tools/site site-readme-text

# 5. Both servers restart onto the same content.
stop "$receiver_pid"
stop "$web_pid"
receive
serve
gone /tools/app
serves /tools/site site-readme-text

# 6. With the receiver stopped, valley-web keeps serving. A pass that
# withdraws site fails because its upload cannot connect, not because it
# failed earlier or hung until timeout ended it, and changes nothing
# served. Once the receiver is back, the next pass takes site off the edge.
stop "$receiver_pid"
serves /tools/site site-readme-text
read_policy site 'operator: true' > work/site/policy/read/read.cue
commit site "withdraw it"
status=0
publish --receiver "$receiver" --receiver-token token > pass.out 2> pass.err || status=$?
case $status in
  0) fail "a pass to a stopped receiver succeeded" ;;
  124 | 137) fail "a pass to a stopped receiver hung until timeout ended it (status $status)" ;;
esac
grep -q "the upload failed [0-9]* times: .*dial tcp ${receiver#http://}: connect: connection refused" pass.err ||
  fail "a pass to a stopped receiver failed with status $status, but not because its upload could not connect: $(cat pass.err)"
serves /tools/site site-readme-text
receive
publish --receiver "$receiver" --receiver-token token > /dev/null
gone /tools/site
gone /tools/homepage

touch "$out"
