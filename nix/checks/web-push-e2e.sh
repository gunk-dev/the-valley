# End-to-end test script for the web push path (see web-push-e2e.nix).
# Each background server is launched with start, which waits up to 30
# seconds for its listening address, and the EXIT trap stops all running
# servers when the script exits.
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
  timeout -k 10 "${within:-300}" valley-web-publish --instance-repo "$TMPDIR/srv/valley.git" \
    --instance valley-host --destination example.org --host "$TMPDIR/host.json" \
    --state "$TMPDIR/publisher" --principals "$TMPDIR/principals.json" --links "$TMPDIR/links/links" "$@"
}
# The link command, with the flags each verb reads; issued PRINCIPAL
# issues a link and prints its ID alone.
link() {
  valley-web-publish link "$@" --links "$TMPDIR/links/links" --state "$TMPDIR/publisher"
}
issue_link() {
  link issue "$@" --instance-repo "$TMPDIR/srv/valley.git" --destination example.org --host "$TMPDIR/host.json"
}
issued() { issue_link --principal "$1" 2> /dev/null | sed 's/.*#\(lk-[0-9a-f]*\)\..*/\1/'; }
bindings() { grep -c '^binding ' edge/v2/authorization || true; }

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
  [docs]=5b1e0c7a9f3d2e4c6a8b0d1f3e5c7a92
  [notes]=8e3b6f1d2a4c7e9b0d5f3a1c6e8b2d47
)
read_policy() {
  printf 'package read\n\nproject: "%s"\nidentity: "%s"\nreaders: {%s}\n' "$1" "${id[$1]}" "$2"
}
# The instance's registry: the operator governs it. At the edge, anyone
# may read the projects of the first scope given; alice, who holds no key,
# those of the second; and the operator those of the third. alice and the
# operator may read every project when their scope is not given.
registry() {
  local alice=${2:-'"all"'} operator=${3:-'"all"'}
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
		read: {boundary: "web", projects: $operator}
	}
}
principals: alice: {kind: "human", keys: [], grants: read: {boundary: "web", projects: $alice}}
anyone: grants: read: {boundary: "web", projects: $1}
EOF
}

# 1. The instance and four projects in the group tools, each with its own
# Markdown README, which links its read policy. app and site are public:
# anyone may read them, and app's policy/read lets alice and the operator
# read it too. docs is private: its policy/read lets alice and the
# operator alone read it. notes is private, and has no effective reader:
# its policy/read lets bob alone read it, and no grant names bob. app has
# the short alias a, site the former name homepage, and docs the short
# alias d.
for name in valley app site docs notes; do
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
projects=$(printf 'app\ndocs\nnotes\nsite\nvalley\n' | git -C srv/valley.git hash-object -w --stdin)
git -C srv/valley.git update-ref refs/the-valley/projects "$projects"
declare -A readers=(
  [app]='anyone: true, operator: true, alice: true'
  [site]='anyone: true'
  [docs]='alice: true, operator: true'
  [notes]='bob: true'
)
for name in app site docs notes; do
  mkdir -p "work/$name/policy/read"
  echo "$TMPDIR/srv/valley.git" > "work/$name/policy/valley"
  printf '# %s\n\n%s-readme-text, under [its read policy](policy/read/read.cue).\n' "$name" "$name" > "work/$name/README.md"
  read_policy "$name" "${readers[$name]}" > "work/$name/policy/read/read.cue"
  commit "$name" "the first commit"
done
knot=$(cd work/app && valley knot new outcome "Open the edge" --json | jq -r .knot.id)
declare -A private_knot
for name in docs notes; do
  private_knot[$name]=$(cd "work/$name" && valley knot new outcome "$name-knot-text" --json | jq -r .knot.id)
done

cat > host.cue << 'EOF'
package valley

groups: core: name: "tools"
projects: app: {group: "core", aliases: ["a"]}
projects: site: {group: "core", formerNames: ["homepage"]}
projects: docs: {group: "core", aliases: ["d"]}
projects: notes: {group: "core"}
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
# valley-web signs readers in at the origin https://example.org, and keeps
# their sessions in edge/browser, which the deployment makes for it alone.
# Before the receiver makes its store, valley-web cannot open it and
# exits, for its service to start it again.
mkdir -p edge
mkdir -m 700 edge/browser
if valley-web --state edge/store --listen 127.0.0.1:0 --origin https://example.org 2> early.log; then
  fail "valley-web started before the receiver made its store"
fi
receive() {
  start valley-web-receive receive.log valley-web-receive --state edge/store --listen 127.0.0.1:0 \
    --instance valley-host --destination example.org --verifiers publisher/verifier --token-file token
  receiver_pid=$pid receiver=http://$addr
}
serve() {
  start valley-web web.log valley-web --state edge/store --listen 127.0.0.1:0 --origin https://example.org
  web_pid=$pid web=$addr
  cat web.log >> web.logs
}
receive
publish --receiver "$receiver" --receiver-token token > pass.out
grep -q "the receiver stored authorization 2: app to alice, anyone, operator; docs to alice, operator; site to anyone$" pass.out ||
  fail "the pass did not say the receiver stored its authorization: $(cat pass.out)"
grep -q "the receiver activated sequence" pass.out || fail "the pass did not say the receiver activated it: $(cat pass.out)"
test -f edge/store && test -d edge/v2/generations && test -f edge/v2/authorization ||
  fail "the receiver did not make the store's descriptor, generations and authorization"
serve
serves /tools/app app-readme-text
# valley-web made its browser state when it started, under authorization
# 2, which binds no link, and the state holds no link yet.
[ "$(cat edge/browser/state)" = the-valley/web-reader-state/v1 ] ||
  fail "valley-web did not make an empty browser state: $(cat edge/browser/state)"
[ "$(stat -c %a edge/browser/state)" = 600 ] || fail "the browser state is mode $(stat -c %a edge/browser/state)"
# The README renders as Markdown: its heading has an id, and its relative
# link goes to the file's page at the same commit.
app_tip=$(git -C srv/app.git rev-parse main)
serves "/tools/app/source/tree/$app_tip/" '<h1 id="app">app <a class="anchor" href="#app">#</a></h1>'
grep -qF "<a href=\"/tools/app/source/blob/$app_tip/policy/read/read.cue\">its read policy</a>" "$TMPDIR/body" ||
  fail "app's README does not link its read policy at $app_tip: $(grep -F readme-text "$TMPDIR/body")"
serves /tools/site site-readme-text
# / lands on the source of the first project published, app, at its tip.
[ "$(get /)" = "302 /tools/app/source/tree/$app_tip/" ] || fail "/ answered $(get /)"
serves "/tools/app/knots/$knot" "Open the edge"
[ "$(get "/a/knots/$knot?x=1")" = "308 /tools/app/knots/$knot?x=1" ] ||
  fail "the alias a answered $(get "/a/knots/$knot?x=1")"
[ "$(get /tools/homepage/source/)" = "308 /tools/site/source/" ] ||
  fail "the former name homepage answered $(get /tools/homepage/source/)"

# docs has two effective readers, so it reaches the edge with both in its
# authorization: the store holds its repository and its knots. notes has
# none, so nothing of it reaches the edge: no authorization names it, the
# store holds no repository of it, and no export holds its name or its
# knots.
for reader in alice operator; do
  grep -qx "reader docs $reader" edge/v2/authorization || fail "the authorization does not let $reader read docs"
done
grep -qx "project docs ${id[docs]}" edge/v2/authorization && ! grep -q "^reader docs anyone" edge/v2/authorization ||
  fail "the authorization does not name docs for its named readers alone: $(cat edge/v2/authorization)"
[ -d edge/v2/current/repos/docs.git ] || fail "the edge holds no repository of docs"
grep -qF docs-knot-text edge/v2/current/export.json || fail "the edge's export.json does not hold docs' knot"
if grep -q "notes" edge/v2/authorization; then fail "the authorization names notes, which has no effective reader"; fi
[ ! -e edge/v2/current/repos/notes.git ] || fail "the edge holds a repository of notes"
if grep -rqF -e notes-knot-text -e '"project":"notes"' edge; then fail "the edge holds the name or a knot of notes"; fi
# An anonymous request gets 404 for both private projects on every route:
# a project page, its source tree, blob, history and archives, a knot, and
# docs' short alias.
for name in docs notes; do
  ptip=$(git -C "srv/$name.git" rev-parse main)
  for path in "/tools/$name" "/tools/$name/" "/tools/$name/source/" "/tools/$name/source/tree/$ptip/" \
    "/tools/$name/source/blob/$ptip/README.md" "/tools/$name/source/log/$ptip" \
    "/tools/$name/source/archive/$ptip.tar.gz" "/tools/$name/source/archive/main.tar.gz" \
    "/tools/$name/knots/${private_knot[$name]}" /tools/$name/knots/oc-0000000; do
    gone "$path"
  done
done
gone /d
gone "/d/knots/${private_knot[docs]}"
serves /-/knots "Open the edge"
if grep -qF -e docs-knot-text -e notes-knot-text "$TMPDIR/body"; then fail "the knots page shows anyone a private knot"; fi

# A reader downloads app's source from the link on its tree page: a
# gzipped tar archive of main's tip that lists main's files and
# directories, each under one directory named for the project and the
# commit.
serves /tools/app app-readme-text
tip=$(git -C work/app rev-parse HEAD)
grep -qF "href=\"/tools/app/source/archive/$tip.tar.gz\">Download .tar.gz<" "$TMPDIR/body" ||
  fail "app's tree page does not link the archive of main's tip"
status=$(curl -sS --max-time 10 -o app.tar.gz -w '%{http_code} %header{content-type}' "http://$web/tools/app/source/archive/$tip.tar.gz")
[ "$status" = "200 application/gzip" ] || fail "valley-web answered app's archive with $status"
prefix=app-${tip:0:12}/
tar -tzf app.tar.gz | LC_ALL=C sort > listed
{
  echo "$prefix"
  git -C work/app ls-tree -r -t --format='%(objecttype) %(path)' HEAD |
    sed -e "s|^tree \(.*\)|$prefix\1/|" -e "s|^blob \(.*\)|$prefix\1|"
} | LC_ALL=C sort > want
cmp -s listed want || fail "app's archive lists $(cat listed), not $(cat want)"

# main.tar.gz is one address for the archive of main's tip, wherever main
# is: a temporary redirect to that archive, which curl -L follows to the
# same bytes.
[ "$(get /tools/app/source/archive/main.tar.gz)" = "302 /tools/app/source/archive/$tip.tar.gz" ] ||
  fail "app's main.tar.gz answered $(get /tools/app/source/archive/main.tar.gz)"
status=$(curl -sS --max-time 10 --max-redirs 3 -L -o main.tar.gz -w '%{http_code} %header{content-type}' "http://$web/tools/app/source/archive/main.tar.gz")
[ "$status" = "200 application/gzip" ] || fail "valley-web answered app's main.tar.gz with $status"
cmp -s main.tar.gz app.tar.gz || fail "app's main.tar.gz is not the archive of main's tip"

# Every response is private and never stored: a page, a redirect, an
# archive, a 404 and a HEAD alike, even one that asks to be answered 304.
# None carries an ETag. The knots page's policy admits its one script by
# its hash; every other route's admits none. No policy lets a script
# connect anywhere.
headers() { curl -sS --max-time 10 -o /dev/null -D - -H 'If-None-Match: *' "$@" | tr -d '\r'; }
for path in / /-/knots "/tools/app/source/tree/$app_tip/" "/tools/app/source/archive/$tip.tar.gz" \
  /tools/app/source/archive/main.tar.gz "/a/knots/$knot" /tools/nosuch/ /-/nosuch; do
  for method in GET HEAD; do
    if [ "$method" = HEAD ]; then got=$(headers -I "http://$web$path"); else got=$(headers "http://$web$path"); fi
    grep -qx 'Cache-Control: private, no-store' <<< "$got" || fail "$method $path is not private and unstored: $got"
    grep -qx 'Vary: Cookie' <<< "$got" || fail "$method $path does not vary by cookie: $got"
    if grep -qi '^etag:' <<< "$got" || grep -q '^HTTP/[0-9.]* 304' <<< "$got"; then
      fail "$method $path answered with an ETag or 304: $got"
    fi
    policy=$(sed -n 's/^Content-Security-Policy: //p' <<< "$got")
    case $path in
      /-/knots) script="script-src 'sha256-" ;;
      *) script="script-src 'none';" ;;
    esac
    [[ $policy == *"$script"* && $policy == *"connect-src 'none'"* ]] ||
      fail "$method $path answered with the policy $policy"
  done
done

# The pages say when the policy was last checked, apart from when the
# plan was collected. A pass that finds nothing changed sends no content,
# and still sends an authorization, signed again with a later sequence and
# a later check, so the time the pages show advances while export.json
# keeps its bytes.
checked_of() { sed -n 's/^checked //p' "$1"; }
sequence_of() { sed -n 's/^sequence //p' "$1"; }
shown() { date -u -d "$1" '+%Y-%m-%d %H:%M UTC'; }
serves /-/knots "Policy last checked $(shown "$(checked_of edge/v2/authorization)")"
grep -qF "Collected " "$TMPDIR/body" || fail "the knots page does not say when the plan was collected"
digest=$(sha256sum edge/v2/current/export.json | cut -d' ' -f1)
before=$(checked_of edge/v2/authorization)
sleep 1.1
publish --receiver "$receiver" --receiver-token token > pass.out
grep -q "nothing has changed since; nothing is sent" pass.out || fail "an unchanged pass sent content: $(cat pass.out)"
[ "$(sequence_of edge/v2/authorization)" = 3 ] || fail "an unchanged pass stored authorization $(sequence_of edge/v2/authorization), want 3"
[ "$(checked_of edge/v2/authorization)" != "$before" ] || fail "an unchanged pass did not advance the policy check from $before"
[ "$(sha256sum edge/v2/current/export.json | cut -d' ' -f1)" = "$digest" ] ||
  fail "an unchanged pass changed export.json"
serves /-/knots "Policy last checked $(shown "$(checked_of edge/v2/authorization)")"

# A read link: issued only to a principal the registry grants an edge
# read that a project consents to, here alice, who holds no key. The host
# keeps its verifier, and the next pass carries it to the edge.
mkdir -m 750 links
if issue_link --principal bob > bob.out 2> bob.err; then fail "a link was issued to bob, whom the registry does not name"; fi
grep -q "grants bob no edge read" bob.err || fail "issuing to bob failed for another reason: $(cat bob.err)"
url=$(issue_link --principal alice --note laptop 2> issue.err)
[[ $url =~ ^https://example\.org/-/redeem#(lk-[0-9a-f]{16})\.([A-Za-z0-9_-]{43})$ ]] ||
  fail "issue printed ${#url} characters, not one link: $(cat issue.err)"
lk=${BASH_REMATCH[1]} secret=${BASH_REMATCH[2]}
link list > list.out
grep -Eq "^$lk +alice +active +pending" list.out || fail "an undelivered link is not pending: $(cat list.out)"
publish --receiver "$receiver" --receiver-token token > pass.out
verifier=$(printf '%s=' "$secret" | tr '_-' '/+' | base64 -d | sha256sum | cut -d' ' -f1)
grep -Eqx "binding $lk alice $verifier" edge/v2/authorization ||
  fail "the stored authorization does not bind $lk to alice by the secret's SHA-256: $(grep binding edge/v2/authorization)"
link list > list.out
grep -Eq "^$lk +alice +active +enforced +[0-9TZ:-]+ +laptop$" list.out || fail "a delivered link is not enforced: $(cat list.out)"
if grep -rqF "$secret" publisher edge links first ./*.out ./*.err ./*.log; then
  fail "the link's secret is in the publisher's state, the edge, the credential file, an export or a log"
fi

# An authorization the receiver stored already is refused as replayed,
# and one whose text was changed is refused for its signature, whatever
# its sequence.
post() {
  curl -sS --max-time 10 -o "$TMPDIR/reply" -w '%{http_code}' -H "Authorization: Bearer $(cat token)" \
    --data-binary "@$2" "$receiver$1"
}
[ "$(post /web-export/v2/authorization publisher/authorization)" = 409 ] && grep -q '"error":"replayed"' reply ||
  fail "a replayed authorization was not refused as replayed: $(cat reply)"
sed 's/^sequence .*/sequence 99/' publisher/authorization > forged
[ "$(post /web-export/v2/authorization forged)" = 403 ] && grep -q '"error":"bad-signature"' reply ||
  fail "an authorization whose text was changed was not refused for its signature: $(cat reply)"

# A publisher of the first protocol is refused with a clear error, and
# valley-web refuses the directory the first store layout took as its
# state.
[ "$(post /web-export/v1/manifest forged)" = 410 ] && grep -q '"error":"unsupported-version"' reply ||
  fail "a manifest of the first protocol was not refused as unsupported-version: $(cat reply)"
if valley-web --state edge --listen 127.0.0.1:0 2> old.log; then fail "valley-web served the store's directory"; fi
grep -q "is a directory" old.log || fail "valley-web refused the store's directory for another reason: $(cat old.log)"

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

# 3b. A policy check that cannot read docs keeps, of the readers the
# stored authorization gave docs, only those a grant still covers, and
# adds none. Here alice's grant stops covering docs while docs' repository
# cannot be read: the pass sends an authorization that keeps docs for the
# operator alone, on the basis of the authorization the receiver holds,
# and fails as incomplete.
registry "{app: \"${id[app]}\", site: \"${id[site]}\"}" "{app: \"${id[app]}\"}" > work/valley/identity/registry.cue
commit valley "alice reads app alone"
held=$(sequence_of edge/v2/authorization)
mv srv/docs.git docs.moved
if publish --receiver "$receiver" --receiver-token token --policy-only > kept.out 2> kept.err; then
  fail "a pass that could not read docs succeeded"
fi
mv docs.moved srv/docs.git
grep -q "the policy check is incomplete" kept.err || fail "the pass that could not read docs failed for another reason: $(cat kept.err)"
grep -qx "basis $held" edge/v2/authorization && grep -qx "reader docs operator" edge/v2/authorization ||
  fail "the incomplete check did not keep docs for the operator on basis $held: $(cat edge/v2/authorization)"
if grep -q "^reader docs alice" edge/v2/authorization; then fail "the incomplete check kept alice, whose grant no longer covers docs"; fi

# Removing one of docs' two readers keeps docs on the edge for the other.
# Removing the last withdraws it: the authorization no longer names it,
# and the receiver deletes its repository, while the signed export.json
# keeps its bytes.
publish --receiver "$receiver" --receiver-token token --policy-only > /dev/null
grep -qx "reader docs operator" edge/v2/authorization && ! grep -q "^reader docs alice" edge/v2/authorization ||
  fail "with alice's grant removed the authorization does not let the operator alone read docs: $(cat edge/v2/authorization)"
[ -d edge/v2/current/repos/docs.git ] || fail "removing one of docs' readers deleted its repository"
digest=$(sha256sum edge/v2/current/export.json | cut -d' ' -f1)
registry "{app: \"${id[app]}\", site: \"${id[site]}\"}" "{app: \"${id[app]}\"}" "{app: \"${id[app]}\"}" > work/valley/identity/registry.cue
commit valley "no one reads docs"
publish --receiver "$receiver" --receiver-token token --policy-only > /dev/null
if grep -q "^project docs " edge/v2/authorization; then fail "the authorization names docs once it has no reader"; fi
for _ in $(seq 300); do
  [ -e edge/v2/current/repos/docs.git ] || break
  sleep 0.1
done
[ ! -e edge/v2/current/repos/docs.git ] || fail "the receiver kept the repository of docs once it had no reader"
[ "$(sha256sum edge/v2/current/export.json | cut -d' ' -f1)" = "$digest" ] ||
  fail "withdrawing docs changed the signed export.json"
gone /d
# Granting both readers again sends docs' bundle again.
registry "{app: \"${id[app]}\", site: \"${id[site]}\"}" > work/valley/identity/registry.cue
commit valley "alice and the operator read every project"
publish --receiver "$receiver" --receiver-token token > pass.out
grep -q "^valley-web-publish: sent .*docs.*; the receiver activated" pass.out || fail "the pass did not send docs' bundle again: $(cat pass.out)"
[ -d edge/v2/current/repos/docs.git ] || fail "the edge holds no repository of docs once it is granted again"

# 4. Withdrawing anyone's grant for app takes app off the edge for
# anonymous requests on the next pass, its alias with it, and site stays.
# alice and the operator may still read app, so the edge keeps it for
# them.
registry "{site: \"${id[site]}\"}" > work/valley/identity/registry.cue
commit valley "withdraw app"
publish --receiver "$receiver" --receiver-token token > /dev/null
gone /tools/app
gone /a
serves /tools/site site-readme-text
grep -qx "project app ${id[app]}" edge/v2/authorization && ! grep -q "^reader app anyone" edge/v2/authorization ||
  fail "withdrawing anyone's grant for app did not keep it for its named readers alone: $(cat edge/v2/authorization)"
[ -d edge/v2/current/repos/app.git ] || fail "withdrawing anyone's grant for app deleted its repository, which its named readers read"

# 4a. Growth never blocks a restriction. One commit to the registry
# withdraws anyone's grant for site, grants app to anyone again, and
# grants 1800 new principals, whom app's and docs' policy/read name, and
# a link is issued to alice. The authorization that gives all of it is
# over the 256 KiB a receiver reads, so the pass sends its bounded
# fallback in its place: the stored authorization less every access the
# check no longer gives. site leaves the edge, its former name with it,
# and nothing is added: app stays off the edge for anonymous requests,
# and the new link is pending. The pass fails, naming the overflow. A
# policy/read file holds at most 64 KiB, so each project's new readers
# are in three files of 600.
for name in app docs; do
  for part in 0 1 2; do
    {
      printf 'package read\n\nreaders: {\n'
      for i in $(seq $((part * 600 + 1)) $((part * 600 + 600))); do printf '\tr%063d: true\n' "$i"; done
      printf '}\n'
    } > "work/$name/policy/read/more-$part.cue"
  done
  commit "$name" "1800 more readers"
done
{
  registry "{app: \"${id[app]}\"}"
  for i in $(seq 1800); do
    printf 'principals: r%063d: {kind: "human", keys: [], grants: read: {boundary: "web", projects: "all"}}\n' "$i"
  done
} > work/valley/identity/registry.cue
commit valley "withdraw site, and grant app and 1800 principals"
grown=$(issued alice)
held=$(sequence_of edge/v2/authorization)
if publish --receiver "$receiver" --receiver-token token > grown.out 2> grown.err; then
  fail "a pass whose authorization is over the limit succeeded"
fi
grep -q "the authorization is too large to send: authorization [0-9]* would be [0-9]* bytes signed" grown.err ||
  fail "the pass over the limit failed for another reason: $(cat grown.err)"
grep -q "from authorization $held, the last the receiver acknowledged" grown.err ||
  fail "the fallback is not signed from authorization $held: $(cat grown.err)"
grep -q "^valley-web-publish: pending: app to 1801 readers; docs to 1800 readers; 1 read links$" grown.out ||
  fail "the pass does not say what is pending: $(cat grown.out)"
[ "$(sequence_of edge/v2/authorization)" -gt "$held" ] || fail "the edge holds no newer authorization than $held"
[ "$(wc -c < edge/v2/authorization)" -le 262144 ] || fail "the edge stored an authorization over 256 KiB"
if grep -q "^project site \|^reader app anyone\|^reader [a-z]* r0" edge/v2/authorization; then
  fail "the fallback gives access the stored authorization did not, or keeps site: $(cat edge/v2/authorization)"
fi
grep -qx "reader app operator" edge/v2/authorization && grep -qx "reader docs alice" edge/v2/authorization ||
  fail "the fallback dropped access the check still gives: $(cat edge/v2/authorization)"
gone /tools/site
gone /homepage
gone /tools/app
if link list --check "$grown" > list.out 2> check.err; then fail "a link the fallback does not carry is not pending: $(cat list.out)"; fi
grep -Eq "^$grown +alice +active +pending" list.out || fail "the link issued with the overflow is not pending: $(cat list.out)"
link revoke "$grown" > /dev/null
for name in app docs; do
  rm "work/$name"/policy/read/more-*.cue
  commit "$name" "the readers again"
done

# 4b. A withdrawal by authorization alone, while content work is
# stalled. With app granted again, a new knot changes the export, and a
# pass reads it, site and docs included, then stalls at its upload: the
# test holds the receiver's activation lock, as a commit that checks a
# large repository would. Meanwhile anyone's grant for site is withdrawn,
# and so are both grants that cover docs, and a --policy-only pass takes
# both off the edge at once: valley-web, not restarted, answers the next
# request without site. Once the lock is released, the stalled upload,
# whose content holds site and docs and whose sequence is later, activates
# and restores neither's authorization. The receiver deletes both
# repositories, and the active export.json keeps its signed bytes.
registry "{app: \"${id[app]}\", site: \"${id[site]}\"}" > work/valley/identity/registry.cue
commit valley "grant app again"
publish --receiver "$receiver" --receiver-token token > /dev/null
serves /tools/app app-readme-text
(cd work/site && valley knot new outcome "Withdrawn while stalled" > /dev/null)
flock -o edge/v2/lock sleep 300 &
holder=$!
pids+=("$holder")
publish --receiver "$receiver" --receiver-token token > stalled.out 2> stalled.err &
stalled=$!
for _ in $(seq 600); do
  grep -q "^valley-web-publish: exported: app, docs, site;" stalled.out && break
  kill -0 "$stalled" 2> /dev/null || fail "the stalled pass ended before its upload: $(cat stalled.out stalled.err)"
  sleep 0.1
done
grep -q "^valley-web-publish: exported: app, docs, site;" stalled.out || fail "the stalled pass did not read the valley within a minute"
sleep 1
registry "{app: \"${id[app]}\"}" "{app: \"${id[app]}\"}" "{app: \"${id[app]}\"}" > work/valley/identity/registry.cue
commit valley "withdraw site and docs"
link revoke "$lk" > /dev/null
if link list --check "$lk" > /dev/null 2> check.err; then fail "an undelivered revocation is not pending"; fi
grep -q "$lk: revoked here; the edge may accept it until a pass delivers the revocation" check.err ||
  fail "list --check did not say the revocation is pending: $(cat check.err)"
within=40 publish --receiver "$receiver" --receiver-token token --policy-only > policy.out ||
  fail "a --policy-only pass failed while content was stalled"
grep -q "the receiver stored authorization [0-9]*: app to alice, anyone, operator$" policy.out ||
  fail "the --policy-only pass did not withdraw site and docs: $(cat policy.out)"
if grep -q "the receiver activated" policy.out; then fail "the --policy-only pass sent content"; fi
gone /tools/site
gone /tools/homepage
serves /tools/app app-readme-text
if grep -q "^binding $lk " edge/v2/authorization; then fail "the --policy-only pass did not revoke $lk"; fi
link list --check "$lk" > list.out || fail "a delivered revocation is still pending"
grep -Eq "^$lk +alice +revoked +enforced" list.out || fail "a delivered revocation is not enforced: $(cat list.out)"
kill -0 "$stalled" 2> /dev/null || fail "the stalled pass did not stall: $(cat stalled.out stalled.err)"
stop "$holder"
wait "$stalled" || fail "the stalled pass failed once its upload went on: $(cat stalled.err)"
grep -q "the receiver activated sequence" stalled.out || fail "the stalled pass did not activate its upload: $(cat stalled.out)"
for name in site docs; do
  grep -q "\"project\":\"$name\"" edge/v2/current/export.json || fail "the stalled upload's export does not hold $name"
  if grep -q "^project $name " edge/v2/authorization; then fail "the stalled upload restored the authorization of $name"; fi
done
gone /tools/site
digest=$(sha256sum edge/v2/current/export.json | cut -d' ' -f1)
for _ in $(seq 300); do
  [ -e edge/v2/current/repos/site.git ] || [ -e edge/v2/current/repos/docs.git ] || break
  sleep 0.1
done
for name in site docs; do
  [ ! -e "edge/v2/current/repos/$name.git" ] || fail "the receiver kept the repository of $name after its withdrawal"
done
[ -e edge/v2/current/repos/app.git ] || fail "the receiver deleted the repository of app"
[ "$(sha256sum edge/v2/current/export.json | cut -d' ' -f1)" = "$digest" ] ||
  fail "the withdrawal changed the signed export.json"

# Revoking a principal's links, and then every link, reaches the edge
# through a policy pass the same way.
issued alice > /dev/null
issued alice > /dev/null
kept=$(issued operator)
publish --receiver "$receiver" --receiver-token token --policy-only > /dev/null
[ "$(bindings)" = 3 ] || fail "the authorization carries $(bindings) bindings, want 3"
link revoke --principal alice > /dev/null
publish --receiver "$receiver" --receiver-token token --policy-only > /dev/null
[ "$(bindings)" = 1 ] && grep -Eq "^binding $kept operator " edge/v2/authorization ||
  fail "revoking alice's links left $(grep binding edge/v2/authorization)"
link revoke --all > /dev/null
publish --receiver "$receiver" --receiver-token token --policy-only > /dev/null
[ "$(bindings)" = 0 ] || fail "revoking every link left $(bindings) bindings"
link list --check > /dev/null || fail "a delivered revocation of every link is still pending"

policy() { publish --receiver "$receiver" --receiver-token token --policy-only "$@"; }

# A pass holds the links lock, the file lock beside the credential file,
# from just before it reads the file until it has signed. Every change to
# the file holds the same lock. Here a pass pauses holding it: the
# credential file is a FIFO, which the pass waits to read. A revoke
# meanwhile waits, and writes nothing, until the pass has signed, so the
# paused pass signs the link as active. The revocation is pending until
# the next pass, the one the wrapper starts after a revoke, signs no
# binding for the link and the receiver acknowledges it.
paused_lk=$(issued alice)
policy > /dev/null
grep -q "^binding $paused_lk " edge/v2/authorization || fail "the pass did not deliver $paused_lk"
# A revoke while a pass is checking the policy does not wait for the
# check: the pass holds only the authorization lock then.
mkdir pause
mkfifo pause/read.cue
VALLEY_READ_SCHEMA="$TMPDIR/pause/read.cue" policy > checking.out 2> checking.err &
checking=$!
for _ in $(seq 300); do
  flock -n publisher/authorization.lock true || break
  kill -0 "$checking" 2> /dev/null || fail "the pass ended before its policy check: $(cat checking.out checking.err)"
  sleep 0.1
done
if flock -n publisher/authorization.lock true; then fail "the pass did not take the authorization lock"; fi
early=$(issued alice)
link revoke "$early" > early.out 2> early.err || fail "a revoke while a pass checks the policy failed: $(cat early.err)"
if grep -q waiting early.err; then fail "a revoke waited for a pass that was checking the policy: $(cat early.err)"; fi
cp "$readSchema" pause/read.regular
timeout 30 bash -c 'exec 3> pause/read.cue && mv -f pause/read.regular pause/read.cue && cat "$1" >&3' _ "$readSchema" ||
  fail "the pass checking the policy did not read its schema"
wait "$checking" || fail "the pass checking the policy failed: $(cat checking.err)"
if grep -q "^binding $early " edge/v2/authorization; then fail "a pass signed $early, revoked while it checked the policy"; fi
# Now the pause inside the links lock. The FIFO is linked at the credential
# file's name, and keeps a second name for the test to write it through.
cp links/links pause/links.regular
cp links/links pause/links.content
mkfifo pause/links.fifo
ln -f pause/links.fifo links/links
policy > paused.out 2> paused.err &
paused=$!
for _ in $(seq 600); do
  flock -n links/lock true || break
  kill -0 "$paused" 2> /dev/null || fail "the paused pass ended before it read the credential file: $(cat paused.out paused.err)"
  sleep 0.1
done
if flock -n links/lock true; then fail "the paused pass did not take the links lock"; fi
link revoke "$paused_lk" > revoke.out 2> revoke.err &
revoking=$!
for _ in $(seq 300); do
  grep -q "waiting up to 1m0s for the policy pass or the change that holds the links lock" revoke.err && break
  kill -0 "$revoking" 2> /dev/null || fail "the revoke did not wait for the paused pass: $(cat revoke.out revoke.err)"
  sleep 0.1
done
grep -q "waiting up to 1m0s for the policy pass or the change that holds the links lock" revoke.err ||
  fail "the revoke did not say it waits for the paused pass: $(cat revoke.err)"
sleep 1
kill -0 "$revoking" 2> /dev/null || fail "the revoke ended while a pass held the links lock: $(cat revoke.out revoke.err)"
[ -p links/links ] || fail "the revoke wrote the credential file while a pass held the links lock"
# Let the pass go on: once it has opened the FIFO, the regular file takes
# its place for the revoke, and the pass reads the file through the FIFO.
timeout 30 bash -c 'exec 3> pause/links.fifo && mv -f pause/links.regular links/links && cat pause/links.content >&3' ||
  fail "the paused pass did not read the credential file"
wait "$paused" || fail "the paused pass failed: $(cat paused.err)"
wait "$revoking" || fail "the revoke failed: $(cat revoke.err)"
grep -q "^revoked $paused_lk " revoke.out || fail "the revoke did not revoke $paused_lk: $(cat revoke.out)"
grep -q "^binding $paused_lk " edge/v2/authorization || fail "the paused pass did not sign $paused_lk, active as it signed"
if link list --check "$paused_lk" > list.out 2> check.err; then fail "a revocation no pass has delivered is not pending: $(cat list.out)"; fi
grep -Eq "^$paused_lk +alice +revoked +pending" list.out || fail "a revocation no pass has delivered is not pending: $(cat list.out)"
policy > /dev/null
if grep -q "^binding $paused_lk " edge/v2/authorization; then fail "the pass after the revoke signed the revoked $paused_lk"; fi
link list --check "$paused_lk" > list.out || fail "a delivered revocation is still pending: $(cat list.out)"
grep -Eq "^$paused_lk +alice +revoked +enforced" list.out || fail "a delivered revocation is not enforced: $(cat list.out)"

# A lost acknowledgement: the receiver stored an authorization carrying a
# link, and the publisher never recorded it. A revocation after that is
# pending, never enforced, until a pass delivers it.
cp publisher/links-delivery delivery.before
cp publisher/authorization authorization.before
lost=$(issued alice)
policy > /dev/null
grep -q "^binding $lost " edge/v2/authorization || fail "the pass did not deliver $lost"
cp delivery.before publisher/links-delivery
cp authorization.before publisher/authorization
link revoke "$lost" > /dev/null
if link list --check "$lost" > list.out 2> check.err; then fail "a revocation after a lost acknowledgement is not pending: $(cat list.out)"; fi
grep -Eq "^$lost +alice +revoked +pending" list.out || fail "a revocation after a lost acknowledgement is not pending: $(cat list.out)"
policy > /dev/null
link list --check "$lost" > list.out || fail "a delivered revocation is still pending: $(cat list.out)"

# A missing credential file holds no links, and the pass succeeds. A
# corrupt one sends an authorization with no bindings, and fails the pass.
kept=$(issued alice)
mv links/links links.kept
policy > /dev/null || fail "a pass with no credential file failed"
[ "$(bindings)" = 0 ] || fail "a pass with no credential file sent $(bindings) bindings"
echo '{' > links/links
if policy > /dev/null 2> corrupt.err; then fail "a pass with a corrupt credential file succeeded"; fi
grep -q "is not a credential file" corrupt.err || fail "a pass with a corrupt credential file failed for another reason: $(cat corrupt.err)"
[ "$(bindings)" = 0 ] || fail "a corrupt credential file sent $(bindings) bindings"
mv links.kept links/links
policy > /dev/null
grep -q "^binding $kept " edge/v2/authorization || fail "the credential file put back did not deliver $kept"

# A credential file restored from an older copy holds a link revoked
# since. A pass signs the file as it is, so the link is active at the edge
# again. The next change writes past every revision a pass signed from,
# and revoke --all cuts every link off.
cp links/links links.older
link revoke "$kept" > /dev/null
policy > /dev/null
signed=$(jq .signed publisher/links-delivery)
cp links.older links/links
policy > /dev/null 2> older.err || fail "a pass from an older credential file failed: $(cat older.err)"
grep -q "^binding $kept " edge/v2/authorization || fail "a pass from an older credential file did not deliver the $kept it holds"
link revoke --all > /dev/null
[ "$(jq .revision links/links)" = $((signed + 1)) ] ||
  fail "revoke --all on an older credential file wrote revision $(jq .revision links/links), not $((signed + 1))"
policy > /dev/null
[ "$(bindings)" = 0 ] || fail "revoke --all on an older credential file left $(bindings) bindings"
link list --check > /dev/null || fail "after revoke --all a link is pending"

# A host restore: the credential file and the publisher's state come back
# from one older backup, after a pass delivered a revocation. The pass
# after the restore signs above the authorization the receiver holds, and
# the links are as they were at the backup: the revoked link is active
# again. revoke --all then cuts every link off.
restored=$(issued alice)
policy > /dev/null
cp -a publisher backup.publisher
cp links/links backup.links
link revoke "$restored" > /dev/null
policy > /dev/null
if grep -q "^binding $restored " edge/v2/authorization; then fail "the pass did not revoke $restored"; fi
rm -rf publisher
cp -a backup.publisher publisher
cp backup.links links/links
policy > restored.out 2> restored.err || fail "a pass after a host restore failed: $(cat restored.err)"
grep -q "refused [0-9]* as replayed" restored.err || fail "the restored counter was not refused as replayed: $(cat restored.err)"
grep -q "^binding $restored " edge/v2/authorization || fail "a host restore did not restore $restored as it was at the backup"
serves /tools/app app-readme-text
link list --check "$restored" > list.out || fail "the restored link is pending after a pass: $(cat list.out)"
link revoke --all > /dev/null
policy > /dev/null
[ "$(bindings)" = 0 ] || fail "revoke --all after a host restore left $(bindings) bindings"
link list --check > /dev/null || fail "after revoke --all a link is pending"

# Readers sign in, as a reader page's script asks: each request is a
# POST from the origin, of JSON, with X-Valley-Reader: 1. A link the host
# issues redeems once, into a session whose cookie each response renews,
# an archive's too. Of eight concurrent redemptions of one link, one wins.
# A session survives both servers' restarts, ends at logout, and ends at
# the pass that delivers its link's revocation. A second valley-web over
# the same browser state refuses to start. Neither a link's secret nor a
# session's token reaches a log or the browser state.
reader_post() {
  curl -sS --max-time 10 -o "$TMPDIR/reply" -D "$TMPDIR/reply.headers" -w '%{http_code}' \
    -H 'Origin: https://example.org' -H 'Content-Type: application/json' -H 'X-Valley-Reader: 1' \
    -H "Cookie: __Host-valley-reader=${3:-}" --data-binary "$2" "http://$web$1"
}
redeem() { reader_post /-/redeem "{\"credential\":\"$1\"}" "${2:-}"; }
token_of() { tr -d '\r' < "$TMPDIR/reply.headers" | sed -n 's/^Set-Cookie: __Host-valley-reader=\([^;]*\);.*/\1/p'; }
as_reader() {
  curl -sS --max-time 10 -o "$TMPDIR/body" -D "$TMPDIR/headers" -w '%{http_code}' \
    -H "Cookie: __Host-valley-reader=$1" "http://$web$2"
}
renewed() {
  tr -d '\r' < "$TMPDIR/headers" |
    grep -qx "Set-Cookie: __Host-valley-reader=$1; Secure; HttpOnly; Path=/; SameSite=Lax; Max-Age=2592000"
}
# redact FILE: the file for a failure's message, with each credential and
# session token in it cut to its credential ID.
redact() { sed -E 's/(lk-[0-9a-f]{16})\.[A-Za-z0-9_-]{43}/\1.<redacted>/g' "$1"; }
# new_link PRINCIPAL: a link issued and delivered; its credential.
new_link() {
  local url
  url=$(issue_link --principal "$1" 2> /dev/null)
  policy > /dev/null
  echo "${url#*#}" | tee -a secrets
}
cred=$(new_link alice)
[ "$(redeem "$cred")" = 204 ] || fail "a host-issued link did not redeem: $(cat reply)"
token=$(token_of)
echo "$token" >> secrets
[[ $token =~ ^${cred%%.*}\.[A-Za-z0-9_-]{43}$ ]] || fail "the redemption set no session token: $(redact reply.headers)"
[ "$(redeem "$cred")" = 403 ] && [ "$(cat reply)" = "This link cannot be used. Request another link." ] ||
  fail "a redeemed link redeemed again: $(cat reply)"
[ "$(as_reader "$token" /tools/app)" = 302 ] && renewed "$token" || fail "a session was not renewed on a redirect"
[ "$(as_reader "$token" /tools/nosuch/)" = 404 ] && renewed "$token" || fail "a session was not renewed on a 404"
[ "$(as_reader "$token" /-/knots)" = 200 ] && grep -qF '<a href="/-/logout">Sign out</a>' body ||
  fail "a session's knots page does not link to Sign out"
if get /-/knots > /dev/null && grep -qF 'Sign out' body; then fail "anyone's knots page links to Sign out"; fi
archive=$(get /tools/app/source/archive/main.tar.gz)
archive=${archive#302 }
curl -sS --max-time 10 -o anonymous.tar.gz "http://$web$archive"
[ "$(as_reader "$token" "$archive")" = 200 ] && renewed "$token" && cmp -s body anonymous.tar.gz ||
  fail "a session's download of app's archive differs, or was not renewed"
tr -d '\r' < headers | grep -qx 'Cache-Control: private, no-store' && tr -d '\r' < headers | grep -qx 'Content-Type: application/gzip' &&
  tr -d '\r' < headers | grep -q '^Content-Disposition: attachment' || fail "a session's archive lost its headers: $(redact headers)"
race=$(new_link alice)
racers=()
for i in $(seq 8); do
  curl -sS --max-time 10 -o /dev/null -w '%{http_code}\n' -H 'Origin: https://example.org' \
    -H 'Content-Type: application/json' -H 'X-Valley-Reader: 1' --data-binary "{\"credential\":\"$race\"}" \
    "http://$web/-/redeem" > "race.$i" &
  racers+=("$!")
done
wait "${racers[@]}"
[ "$(cat race.* | grep -c '^204$')" = 1 ] && [ "$(cat race.* | grep -c '^403$')" = 7 ] ||
  fail "eight concurrent redemptions of one link answered $(cat race.* | tr '\n' ' ')"
stop "$web_pid"
stop "$receiver_pid"
receive
serve
[ "$(as_reader "$token" /tools/app)" = 302 ] && renewed "$token" || fail "a session did not survive both servers' restarts"
if valley-web --state edge/store --listen 127.0.0.1:0 --origin https://example.org 2> second.log; then
  fail "a second valley-web started over the same browser state"
fi
grep -q "another process owns the browser state" second.log || fail "a second valley-web failed for another reason: $(cat second.log)"
[ "$(reader_post /-/logout '{}' "$token")" = 204 ] &&
  tr -d '\r' < reply.headers | grep -qx 'Set-Cookie: __Host-valley-reader=; Secure; HttpOnly; Path=/; SameSite=Lax; Max-Age=0' ||
  fail "logout did not clear the cookie: $(redact reply.headers)"
[ "$(as_reader "$token" /tools/app)" = 302 ] && ! renewed "$token" || fail "a signed-out session is still renewed"
[ "$(reader_post /-/logout '{}' "$token")" = 204 ] || fail "a second logout failed"
revoked=$(new_link alice)
[ "$(redeem "$revoked")" = 204 ] || fail "a third link did not redeem"
revoked_token=$(token_of)
echo "$revoked_token" >> secrets
link revoke "${revoked%%.*}" > /dev/null
policy > /dev/null
[ "$(as_reader "$revoked_token" /tools/app)" = 302 ] && ! renewed "$revoked_token" ||
  fail "the session of a revoked link is still renewed"

# A signed-in reader reads a project that only named readers may read.
# Step 4b took docs off the edge, so a commit grants the operator docs
# again, and a pass sends docs' bundle back. A session of a link issued to
# the operator reads docs' tree page with its README, docs' knot, its
# archive, and the knots page, which lists docs' knot. Anonymous requests
# for the same pages get 404, and anyone's knots page shows no knot of
# docs. Once a pass delivers the link's revocation, the session gets 404
# for docs' tree page, while the operator may still read docs.
registry "{app: \"${id[app]}\"}" "{app: \"${id[app]}\"}" "{app: \"${id[app]}\", docs: \"${id[docs]}\"}" \
  > work/valley/identity/registry.cue
commit valley "the operator reads docs"
publish --receiver "$receiver" --receiver-token token > pass.out
grep -q "^valley-web-publish: sent .*docs.*; the receiver activated" pass.out || fail "the pass did not send docs' bundle again: $(cat pass.out)"
docs_link=$(new_link operator)
docs_id=${docs_link%%.*}
grep -qx "reader docs operator" edge/v2/authorization && ! grep -q "^reader docs \(alice\|anyone\)$" edge/v2/authorization &&
  grep -q "^binding $docs_id operator " edge/v2/authorization ||
  fail "the authorization does not let the operator alone read docs, or does not bind $docs_id to the operator: $(cat edge/v2/authorization)"
[ "$(redeem "$docs_link")" = 204 ] || fail "the operator's link did not redeem: $(redact reply)"
docs_token=$(token_of)
echo "$docs_token" >> secrets
docs_tip=$(git -C srv/docs.git rev-parse main)
docs_tree=/tools/docs/source/tree/$docs_tip/
docs_knot=/tools/docs/knots/${private_knot[docs]}
docs_archive=/tools/docs/source/archive/$docs_tip.tar.gz
[ "$(as_reader "$docs_token" "$docs_tree")" = 200 ] && grep -qF docs-readme-text body ||
  fail "the operator's session was not served docs' tree page with its README"
[ "$(as_reader "$docs_token" "$docs_knot")" = 200 ] && grep -qF docs-knot-text body ||
  fail "the operator's session was not served docs' knot"
[ "$(as_reader "$docs_token" "$docs_archive")" = 200 ] || fail "the operator's session was not served docs' archive"
tar -tzf body > docs.listed || fail "the operator's session's archive of docs is not a gzipped tar archive"
grep -qx "docs-${docs_tip:0:12}/README.md" docs.listed || fail "the operator's session's archive of docs does not hold its README"
[ "$(as_reader "$docs_token" /-/knots)" = 200 ] && grep -qF docs-knot-text body ||
  fail "the operator's session's knots page does not list docs' knot"
for path in "$docs_tree" "$docs_knot" "$docs_archive"; do gone "$path"; done
[ "$(get /-/knots)" = "200 " ] || fail "anyone's knots page answered $(get /-/knots)"
if grep -qF docs-knot-text body; then fail "anyone's knots page lists docs' knot"; fi
link revoke "$docs_id" > /dev/null
policy > /dev/null
grep -qx "reader docs operator" edge/v2/authorization && ! grep -q "^binding $docs_id " edge/v2/authorization ||
  fail "the pass did not revoke $docs_id while the operator still reads docs: $(cat edge/v2/authorization)"
[ "$(as_reader "$docs_token" "$docs_tree")" = 404 ] || fail "the session of a revoked link was served docs' tree page"

# A lost browser state is never made again under an authorization that
# binds a link: redemption and requests with a cookie answer 503, and
# anyone is served. The guide's recovery revokes every link, delivers an
# authorization that binds none, which the next request makes the state
# under, and issues new links.
pending=$(new_link alice)
stop "$web_pid"
mv edge/browser/state lost.state
serve
[ "$(redeem "$pending")" = 503 ] || fail "with its browser state lost, a redemption answered $(cat reply)"
[ "$(as_reader "$revoked_token" /tools/app)" = 503 ] || fail "with its browser state lost, a request with a cookie was served"
[ "$(get /tools/app | cut -d' ' -f1)" = 302 ] || fail "with its browser state lost, anyone was not served"
[ ! -e edge/browser/state ] || fail "valley-web made a browser state under an authorization that binds a link"
link revoke --all > /dev/null
policy > /dev/null
get /-/knots > /dev/null
[ "$(cat edge/browser/state)" = the-valley/web-reader-state/v1 ] || fail "no empty browser state was made once no link was bound"
[ "$(redeem "$(new_link alice)")" = 204 ] || fail "a new link did not redeem after the recovery: $(cat reply)"
recovered=$(token_of)
echo "$recovered" >> secrets
# A browser state that cannot be read is never replaced: anyone is served,
# and a request with a cookie answers 503, until the operator delivers an
# authorization that binds no link, removes the state while valley-web is
# stopped, and starts it.
stop "$web_pid"
echo garbage > edge/browser/state
serve
grep -q "the browser state is unavailable" web.log || fail "valley-web did not say its browser state is unavailable: $(cat web.log)"
[ "$(as_reader "$recovered" /tools/app)" = 503 ] || fail "with its browser state unreadable, a request with a cookie was served"
[ "$(get /-/knots | cut -c1-3)" = 200 ] || fail "with its browser state unreadable, anyone was not served"
[ "$(cat edge/browser/state)" = garbage ] || fail "valley-web replaced a browser state it could not read"
link revoke --all > /dev/null
policy > /dev/null
stop "$web_pid"
rm edge/browser/state
serve
[ "$(cat edge/browser/state)" = the-valley/web-reader-state/v1 ] || fail "valley-web did not make the browser state again"
if grep -rqFf secrets --exclude=secrets --exclude=reply.headers --exclude=headers --exclude='race.*' \
  ./*.log web.logs edge/browser links publisher; then
  fail "a link's secret or a session's token is in a log, the browser state, the credential file or the publisher's state"
fi

# A store whose authorization is removed serves no project, though
# valley-web has the export indexed already; once it is back, it serves.
mv edge/v2/authorization authorization.kept
[ "$(get /tools/app)" = "503 " ] || fail "with no authorization valley-web answered /tools/app with $(get /tools/app)"
grep -qF "Nothing published yet" "$TMPDIR/body" || fail "with no authorization the page does not say nothing is published"
if grep -qF app-readme-text "$TMPDIR/body"; then fail "with no authorization valley-web served app's README"; fi
[ "$(get /)" = "503 " ] || fail "with no authorization valley-web answered / with $(get /)"
mv authorization.kept edge/v2/authorization
serves /tools/app app-readme-text

# Granting site again, and withdrawing app as step 4 left it, serves site
# once more: the receiver's reply says it no longer holds site's
# repository, so the pass sends site's bundle again.
registry "{site: \"${id[site]}\"}" > work/valley/identity/registry.cue
commit valley "withdraw app again"
publish --receiver "$receiver" --receiver-token token > pass.out
grep -q "^valley-web-publish: sent .*site; the receiver activated" pass.out || fail "the pass did not send site's bundle again: $(cat pass.out)"
serves /tools/site site-readme-text
gone /tools/app

# 5. Both servers restart onto the same content.
stop "$receiver_pid"
stop "$web_pid"
receive
serve
gone /tools/app
serves /tools/site site-readme-text

# 6. With the receiver stopped, valley-web keeps serving. A pass that
# withdraws site fails because it cannot send its authorization, not
# because it failed earlier or hung until timeout ended it, and changes
# nothing served. Once the receiver is back, the next pass checks the
# policy again, signs a new authorization with a later sequence, and takes
# site off the edge.
stop "$receiver_pid"
serves /tools/site site-readme-text
read_policy site '' > work/site/policy/read/read.cue
commit site "withdraw it"
status=0
publish --receiver "$receiver" --receiver-token token > pass.out 2> pass.err || status=$?
case $status in
  0) fail "a pass to a stopped receiver succeeded" ;;
  124 | 137) fail "a pass to a stopped receiver hung until timeout ended it (status $status)" ;;
esac
grep -q "sending authorization [0-9]* failed [0-9]* times: .*dial tcp ${receiver#http://}: connect: connection refused" pass.err ||
  fail "a pass to a stopped receiver failed with status $status, but not because it could not connect: $(cat pass.err)"
serves /tools/site site-readme-text
unsent=$(sequence_of publisher/authorization)
receive
publish --receiver "$receiver" --receiver-token token > /dev/null
[ "$(sequence_of edge/v2/authorization)" -gt "$unsent" ] ||
  fail "the pass after the receiver came back stored authorization $(sequence_of edge/v2/authorization), not one signed after $unsent"
gone /tools/site
gone /tools/homepage
gone /tools/app

touch "$out"
