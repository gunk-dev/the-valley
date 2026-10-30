# The signed fixture valley-web's tests read (web-published in
# nix/packages.nix): the real publisher's output, written with --out, over
# a small valley made with the packaged CLI and real git. Anyone may read
# app. alice alone may read site, and the operator alone docs, so the two
# named readers see different private projects. notes' policy/read lets
# bob alone read it, and no grant names bob, so it has no effective
# reader. site and docs each have an alias, a former name, a knot with a
# comment, an abandoned knot and a ready change. Three of app's knots are
# each blocked by an open knot, an abandoned knot and a missing knot of
# one other project: site, docs or notes. $out holds the export the
# publisher wrote, as published/, and the export key's verifier line, as
# verifier.
set -euo pipefail

export HOME="$TMPDIR/home"
export XDG_CACHE_HOME="$TMPDIR/cache"
export XDG_STATE_HOME="$TMPDIR/xdg-state"
export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL="$TMPDIR/gitconfig"
printf '[user]\n\tname = operator\n\temail = operator@example.org\n[init]\n\tdefaultBranch = main\n' > "$GIT_CONFIG_GLOBAL"
cd "$TMPDIR"

commit() {
  git -C "work/$1" add --all
  git -C "work/$1" commit --quiet -m "$1: $2"
  git -C "work/$1" push --quiet origin main
}

declare -A id=(
  [valley]=f83927d5a4ed068cdd02a8cdaa09f26d
  [app]=9da6809f038df92138c131cd16bed76d
  [site]=273d5668702918ea7ae079de374db96b
  [docs]=5b1e0c7a9f3d2e4c6a8b0d1f3e5c7a92
  [notes]=8e3b6f1d2a4c7e9b0d5f3a1c6e8b2d47
)
declare -A readers=(
  [valley]='operator: true'
  [app]='anyone: true'
  [site]='alice: true'
  [docs]='operator: true'
  [notes]='bob: true'
)
read_policy() {
  printf 'package read\n\nproject: "%s"\nidentity: "%s"\nreaders: {%s}\n' "$1" "${id[$1]}" "${readers[$1]}"
}

for name in valley app site docs notes; do
  git init --quiet --bare "srv/$name.git"
  git init --quiet "work/$name"
  git -C "work/$name" remote add origin "$TMPDIR/srv/$name.git"
  git -C "work/$name" config valley.principal operator
  mkdir -p "work/$name/policy/read"
  read_policy "$name" > "work/$name/policy/read/read.cue"
done

# The instance: the operator governs the registry, and the operator and
# alice, who holds no key, may read every project at the edge. Anyone may
# read app alone.
mkdir -p work/valley/policy/instance work/valley/identity
echo 'package verification' > work/valley/policy/instance/floor.cue
cat > work/valley/identity/registry.cue << EOF
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
principals: alice: {kind: "human", keys: [], grants: read: {boundary: "web", projects: "all"}}
anyone: grants: read: {boundary: "web", projects: {app: "${id[app]}"}}
EOF
commit valley "the instance"
projects=$(printf 'app\ndocs\nnotes\nsite\nvalley\n' | git -C srv/valley.git hash-object -w --stdin)
git -C srv/valley.git update-ref refs/the-valley/projects "$projects"

for name in app site docs notes; do
  echo "$TMPDIR/srv/valley.git" > "work/$name/policy/valley"
  printf '# %s\n\n%s-readme-text\n' "$name" "$name" > "work/$name/README.md"
  commit "$name" "the first commit"
done
# Each private project's knots, comment and ready change carry its name,
# so a page that shows any of them to another reader is caught.
declare -A waits=([site]=first [docs]=second [notes]=third)
declare -A change=([site]=ssssssssssssssssssssssssssssssss [docs]=tttttttttttttttttttttttttttttttt)
for name in site docs notes; do
  open=$(cd "work/$name" && valley knot new outcome "$name-knot-text" --json | jq -r .knot.id)
  ended=$(cd "work/$name" && valley knot new outcome "$name-abandoned-text" --json | jq -r .knot.id)
  (cd "work/$name" && valley knot set "$ended" status=abandoned > /dev/null)
  (cd "work/$name" && valley knot comment "$open" -m "$name-comment-text" > /dev/null)
  (cd work/app && valley knot new outcome "Waits on the ${waits[$name]} private project" \
    --blocked-by "$name:$open" --blocked-by "$name:$ended" --blocked-by "$name:oc-0000000" > /dev/null)
done
for name in site docs; do
  git -C "work/$name" checkout --quiet -b "$name-change-handle"
  echo "$name-change-text" > "work/$name/change.md"
  git -C "work/$name" add --all
  git -C "work/$name" commit --quiet -m "$name-change-subject"
  git -C "work/$name" push --quiet origin "$name-change-handle"
  (cd "work/$name" && valley ready --change "${change[$name]}" "$name-change-handle" > /dev/null)
  git -C "work/$name" checkout --quiet main
done

cat > host.cue << 'EOF'
package valley

groups: core: name: "tools"
projects: app: group: "core"
projects: site: {group: "core", aliases: ["portal"], formerNames: ["homepage"]}
projects: docs: {group: "core", aliases: ["handbook"], formerNames: ["manual"]}
projects: notes: group: "core"
projects: valley: {}
EOF
cue export --out json "$schema" host.cue > host.json
echo '{"operator": "operator"}' > principals.json

mkdir -p "$out"
valley-web-publish --instance-repo "$TMPDIR/srv/valley.git" --instance valley-host --destination example.org \
  --host "$TMPDIR/host.json" --state "$TMPDIR/publisher" --principals "$TMPDIR/principals.json" --out "$out/published"
cp publisher/verifier "$out/verifier"
