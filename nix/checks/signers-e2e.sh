# shellcheck shell=bash
# The signers publication, end to end. The publisher the module renders
# writes it into an instance repository, and fresh clones of a project
# that names that instance verify a landing with it. Both repositories run
# the fixture host's rendered pre-receive hook. The integrator is the real
# one, driving the nix stand-in of integrator/e2e, and it signs under its
# default name, as a host that sets no signing name has it do.
#
# Two things are written on disk rather than pushed, as the host writes
# them: valley-init's project list, and the publication itself. No push
# may write either. Each is marked where it is made.
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
set -euo pipefail

w="$TMPDIR/signers-e2e"
mkdir -p "$w"
export HOME="$w"
export GIT_CONFIG_NOSYSTEM=1
unset VALLEY_KNOWN_SIGNERS VALLEY_INTEGRATOR_SIGNER VALLEY_PRINCIPAL
git config --global user.name signers-e2e
git config --global user.email signers-e2e@localhost
git config --global init.defaultBranch main
git config --global advice.detachedHead false

fail() {
  printf 'signers-e2e: %s\n' "$*" >&2
  for f in "$w"/out "$w"/err; do
    if [ -s "$f" ]; then cat "$f" >&2; fi
  done
  exit 1
}

cp -r "$integratorE2e" "$w/stand-ins"
chmod -R +w "$w/stand-ins"
patchShebangs "$w/stand-ins" > /dev/null
export PATH="$w/stand-ins:$PATH" VALLEY_FAKE_STORE="$w/store"

mkdir -p "$w/keys"
for who in contributor integrator rotated; do install -m 600 "$keys/$who" "$w/keys/$who"; done
contributor=contributor.valley.invalid/attestations
# The name the controller signs under: the binary's default, which is this
# machine's name.
name="$(uname -n)/attestations"
# The file the controller is given as --known-signers. signers_for writes it
# for the integrator key given.
signers="$w/known-signers"
signers_for() {
  {
    attest key --key "$w/keys/contributor" --name "$contributor"
    attest key --key "$1" --name "$name"
  } > "$signers"
}
signers_for "$w/keys/integrator"

hook="$(grep -o '/nix/store/[^ ]*-valley-protect-open' "$initScriptPath" | head -n1)"
test -x "$hook"
instance="$w/instance.git"
served="$w/served.git"
for repo in "$instance" "$served"; do
  git init --quiet --bare "$repo"
  cp "$hook" "$repo/hooks/pre-receive"
  chmod +x "$repo/hooks/pre-receive"
done

# The publisher as the module renders it for a host that serves sealed as
# its instance repository and sets no signing name, pointed at this test's
# instance repository and signers file.
read -ra publish <<< "$publishCommand"
publish=("${publish[@]//\/srv\/git\/sealed.git/$instance}")
publish=("${publish[@]//\/var\/lib\/valley-instance\/known_signers/$signers}")
case " ${publish[*]} " in
  *" publish-signers --instance-repo $instance --known-signers $signers ") ;;
  *) fail "the rendered publisher is not the one this test drives: $publishCommand" ;;
esac

# The instance repository carries the floor, and the project names it at
# policy/valley.
mkdir -p "$w/floor"
printf 'package verification\n\nfloor: {\n\tchecks: {}\n\tclasses: {}\n\tunclassified: {}\n}\n' > "$w/floor/floor.cue"
git init --quiet "$w/seed"
mkdir -p "$w/seed/policy/instance"
cp "$w/floor/floor.cue" "$w/seed/policy/instance/"
git -C "$w/seed" add -A
git -C "$w/seed" commit --quiet -m 'the floor'
git -C "$w/seed" push --quiet "$instance" main
# Written on disk, as valley-init writes it: the host's project list.
projects="$(printf '%s\n' instance served | git -C "$instance" hash-object -w --stdin)"
git -C "$instance" update-ref refs/the-valley/projects "$projects"

git init --quiet "$w/fixture"
cd "$w/fixture"
git remote add origin "$served"
mkdir -p docs src checks policy/project
printf '%s\n' "$instance" > policy/valley
echo "the readme" > docs/readme.md
echo "the source" > src/main.txt
echo docs > checks/prose-format.inputs
echo src > checks/code-build.inputs
cat > policy/project/policy.cue << 'CUE'
package verification

project: {
	checks: {
		"prose-format": {runner: "nix", attribute: "prose-format"}
		"code-build": {runner: "nix", attribute: "code-build"}
	}
	classes: {
		prose: {paths: "docs/**": true, requires: "prose-format": true}
		code: {paths: "src/**": true, requires: "code-build": true}
	}
	unclassified: {}
}
CUE
git add -A
git commit --quiet -m 'seed the project'
git push --quiet origin main

as() {
  local who="$1"
  shift
  git config valley.principal "$who"
  VALLEY_PRINCIPAL="$who" "$@"
}
ok() { "$@" > "$w/out" 2> "$w/err" || fail "$* failed"; }
id() { sed -n 's/^The change ID \([k-z]*\) is new\..*/\1/p' "$w/out"; }
# readied <branch>: alice's change, pushed and readied; prints the change ID.
readied() {
  as alice git push --quiet origin "$1"
  ok as alice valley ready
  id
}
# attested <branch> <check>: readied, with its evidence signed by the
# contributor's key and pushed with it through the hook.
attested() {
  as alice attest run --repo . --key "$w/keys/contributor" --name "$contributor" \
    --push origin --branch "$1" --check "$2" > "$w/out" 2> "$w/err" || fail "attest run on $1 failed"
  ok as alice valley ready
  id
}
# moves: main moves on, so a later landing is no fast-forward.
moves() {
  git checkout --quiet -B main origin/main
  echo "$1" >> moved.txt
  git add moved.txt
  git commit --quiet -m "main moves on: $1"
  git push --quiet origin main
  git fetch --quiet origin
}

git fetch --quiet origin
# quick lands by fast-forward. waiting is ready and never requested.
git checkout --quiet -b quick origin/main
echo "a quick note" > docs/quick.md
git add docs/quick.md
git commit --quiet -m 'a change that lands by fast-forward'
quick="$(readied quick)"
git push --quiet origin quick:main
git fetch --quiet origin
git checkout --quiet -b waiting origin/main
echo "waiting" > docs/waiting.md
git add docs/waiting.md
git commit --quiet -m 'a change nobody approves'
waiting="$(readied waiting)"
# lands is landed by the integrator after main moves, so by a commit of
# its own. above is a change on top of it.
git checkout --quiet -b lands origin/main
echo "a second source line" >> src/main.txt
git commit --quiet -am 'a change the integrator lands'
lands="$(attested lands code-build)"
git checkout --quiet -b above lands
echo "above" > src/above.txt
git add src/above.txt
git commit --quiet -m 'a change on top of lands'
above="$(readied above)"
moves one

integrate() {
  integrator reconcile --repo "$served" --key "$1" --known-signers "$signers" --instance "$w/floor" \
    --schema "$SCHEMA_VERIFICATION" --attest-schema "$SCHEMA_ATTESTATION" --event-schema "$SCHEMA_EVENTS" \
    > "$w/integrator.out" 2>&1 || fail "the integrator failed: $(cat "$w/integrator.out")"
  grep -q "^c-$2 -> refs/heads/main .*: land\$" "$w/integrator.out" \
    || fail "the integrator did not land c-$2: $(cat "$w/integrator.out")"
}
as requester git push --quiet origin "lands:refs/the-valley/integration-requests/main/c-$lands"
integrate "$w/keys/integrator" "$lands"
landed="$(git -C "$served" rev-parse main)"
[ "$(git -C "$served" rev-parse main^)" = "$(git rev-parse origin/main)" ] \
  || fail 'the integrator did not land lands by a commit on the moved main'

# client: a fresh clone of the project, with a cache of its own, and
# neither variable set. listing runs a command in it, with any variables
# given first, into out and err.
n=0
client() {
  n=$((n + 1))
  c="$w/client-$n"
  git clone --quiet "$served" "$c"
  git -C "$c" config valley.principal alice
  export XDG_CACHE_HOME="$w/cache-$n"
}
listing() { (cd "$c" && env "$@") > "$w/out" 2> "$w/err"; }
changes() { listing "$@" valley changes --all --json || fail "valley changes failed"; }
landing() {
  jq -r --arg id "$1" '.repos[] | select(.repo == "served") | .changes[] | select(.change_id == $id) | .versions[-1].landing' "$w/out"
}
expect() {
  local got
  got="$(landing "$1")"
  [ "$got" = "$2" ] || fail "$3: $1 reads $got, not $2"
}
# Every version's landing, one line each.
landings() {
  jq -r '.repos[] | .repo as $r | .changes[] | .change_id as $id | .versions[] | "\($r) \($id) v\(.n) \(.landing)"' "$w/out" | LC_ALL=C sort
}
said() { grep -qF -- "$1" "$w/err" || fail "stderr does not say: $1"; }
quiet() { if grep -qF -- "$1" "$w/err"; then fail "stderr says: $1"; fi; }

# ----------------------------------------------------------------------
# With nothing published and nothing set, the landing reads uncertain and
# the command says why. Every other landing reads as it always did.
client
changes
expect "$lands" uncertain 'with nothing published and nothing set'
expect "$quick" landed 'a fast-forward'
expect "$waiting" not-landed 'a change nobody approved'
said 'could not be verified'
landings > "$w/unpublished"
listing valley status --json "$lands" || fail 'valley status failed'
[ "$(jq -r .landing "$w/out")" = uncertain ] || fail "status reads lands as $(jq -r .landing "$w/out")"
stack() { cut -f4 "$w/out" | tr '\n' ' '; }
listing valley-go stack "$above" || fail 'valley-go stack failed'
[ "$(stack)" = "$(git rev-parse lands) $(git rev-parse above) " ] \
  || fail "the stack under above is not lands and above: $(cat "$w/out")"
# The variables still verify it, as they did before there was a
# publication.
changes VALLEY_KNOWN_SIGNERS="$signers" VALLEY_INTEGRATOR_SIGNER="$name"
expect "$lands" landed-recorded 'with both variables set'

# ----------------------------------------------------------------------
# The publisher refuses a file that is not verifier keys, and publishes
# nothing.
echo 'not a verifier key' > "$w/garbage"
if integrator publish-signers --instance-repo "$instance" --known-signers "$w/garbage" > "$w/out" 2> "$w/err"; then
  fail 'the publisher published a file that is not verifier keys'
fi
said 'the known-signers file does not read'
[ -z "$(git -C "$instance" for-each-ref refs/the-valley/signers)" ] || fail 'a refused run published something'

# Written on disk, as the host's publisher writes it: one tree holding the
# controller's signers file and the name it signs under.
"${publish[@]}" > "$w/out" 2> "$w/err" || fail 'the publisher failed'
published="$(git -C "$instance" rev-parse refs/the-valley/signers)"
[ "$(git -C "$instance" cat-file -t "$published")" = tree ] || fail 'the publication is not a tree'
[ "$(git -C "$instance" ls-tree --name-only "$published" | tr '\n' ' ')" = "integrator-name known-signers " ] \
  || fail "the publication holds $(git -C "$instance" ls-tree --name-only "$published")"
[ "$(git -C "$instance" cat-file blob "$published:integrator-name")" = "$name" ] \
  || fail 'the publication does not name the signer the controller signs under'
git -C "$instance" cat-file blob "$published:known-signers" | cmp -s - "$signers" \
  || fail "the publication does not hold the controller's signers file"
"${publish[@]}" > "$w/out" 2> "$w/err" || fail 'the publisher failed the second time'
[ "$(git -C "$instance" rev-parse refs/the-valley/signers)" = "$published" ] \
  || fail 'publishing what is already published moved the ref'
grep -q 'already publishes' "$w/out" || fail 'the publisher did not say nothing changed'

# ----------------------------------------------------------------------
# A fresh client with nothing set reads the landing as landed-recorded,
# and says nothing about verifying. Every other landing reads as it did
# with nothing published.
client
changes
expect "$lands" landed-recorded 'on a fresh client with nothing set'
quiet 'could not be verified'
quiet 'no transfer statement is verified'
landings | sed "s/^served $lands v1 landed-recorded\$/served $lands v1 uncertain/" > "$w/published"
diff "$w/unpublished" "$w/published" > /dev/null \
  || fail "the publication changed another landing: $(diff "$w/unpublished" "$w/published")"
[ "$(jq -r --arg id "$lands" '.repos[] | .changes[] | select(.change_id == $id) | .versions[-1].landed_commit' "$w/out")" = "$landed" ] \
  || fail 'the landing does not name the commit it landed as'
listing valley status --json "$lands" || fail 'valley status failed'
[ "$(jq -r .landing "$w/out")" = landed-recorded ] || fail "status reads lands as $(jq -r .landing "$w/out")"
quiet 'could not be verified'
# lands is no layer under above any more, and rebase drops its commits:
# above's own commit goes onto main alone.
listing valley-go stack "$above" || fail 'valley-go stack failed'
[ "$(stack)" = "$(git rev-parse above) " ] \
  || fail "the stack under above still holds more than above: $(cat "$w/out")"
listing VALLEY_PRINCIPAL=alice valley rebase "$above" || fail 'valley rebase failed'
[ "$(git -C "$served" rev-parse above^)" = "$landed" ] || fail 'above was not rebased onto the landing'
[ "$(git -C "$served" diff --name-only main above)" = src/above.txt ] \
  || fail "above carries more than its own commit: $(git -C "$served" diff --name-only main above)"

# ----------------------------------------------------------------------
# Overrides. A variable beats the publication: a file that reads, under a
# name it holds a key for, is used, and verifies nothing here.
attest key --key "$w/keys/rotated" --name other.valley.invalid/attestations > "$w/other-signers"
other=(VALLEY_KNOWN_SIGNERS="$w/other-signers" VALLEY_INTEGRATOR_SIGNER=other.valley.invalid/attestations)
changes "${other[@]}"
expect "$lands" uncertain 'with both variables naming another signer'
quiet 'no transfer statement is verified'
# A flag beats a variable.
listing "${other[@]}" valley changes --all --json --known-signers "$signers" --integrator-name "$name" \
  || fail 'changes failed with the flags'
expect "$lands" landed-recorded 'with the flags set over the variables'
listing "${other[@]}" valley status --json --known-signers="$signers" --integrator-name="$name" "$lands" \
  || fail 'status failed with the flags'
[ "$(jq -r .landing "$w/out")" = landed-recorded ] || fail "status with the flags reads lands as $(jq -r .landing "$w/out")"
# Each value comes from its own first source: a name from the variable,
# with the published file, which holds no key under it, is reported.
changes VALLEY_INTEGRATOR_SIGNER=other.valley.invalid/attestations
expect "$lands" uncertain 'with the published file and another name'
said "the known-signers file published at refs/the-valley/signers in $instance holds no key under the integrator name other.valley.invalid/attestations (from VALLEY_INTEGRATOR_SIGNER), so no transfer statement is verified"
# An override that does not read is reported, and the publication is not
# used in its place.
changes VALLEY_KNOWN_SIGNERS="$w/missing"
expect "$lands" uncertain 'with a variable naming no file'
said "the known-signers file $w/missing (from VALLEY_KNOWN_SIGNERS) cannot be read"
listing valley changes --all --json --known-signers "$w/garbage" || fail 'changes failed with a garbage file'
expect "$lands" uncertain 'with a flag naming a file that is not verifier keys'
said "the known-signers file $w/garbage (from --known-signers) does not read as verifier keys"
if listing valley changes --all --json --integrator-name; then fail 'a flag with no value was taken'; fi
said '--integrator-name needs a value'

# ----------------------------------------------------------------------
# No push replaces the publication or deletes it, forced or not, whatever
# the pusher holds: requester holds the request and restack grants.
cd "$c"
for refspec in "+HEAD^{tree}:refs/the-valley/signers" "+HEAD:refs/the-valley/signers" ":refs/the-valley/signers"; do
  if VALLEY_PRINCIPAL=requester git push "$instance" "$refspec" > "$w/out" 2> "$w/err"; then
    fail "the push of $refspec was taken"
  fi
  said 'refs/the-valley/signers is written only by the machinery on the host, and no push may write it'
  [ "$(git -C "$instance" rev-parse refs/the-valley/signers)" = "$published" ] \
    || fail "the push of $refspec moved the publication"
done
cd "$w/fixture"

# ----------------------------------------------------------------------
# The integrator's key is rotated: the signers file now holds the new key
# under the same name, and the next landing is signed with it. Until the
# publisher runs again, a fresh client cannot verify that landing. After
# it, the client reads it landed-recorded.
signers_for "$w/keys/rotated"
git fetch --quiet origin
git checkout --quiet -b second origin/main
echo "a third source line" >> src/main.txt
git commit --quiet -am 'a change landed after the rotation'
second="$(attested second code-build)"
moves two
as requester git push --quiet origin "second:refs/the-valley/integration-requests/main/c-$second"
integrate "$w/keys/rotated" "$second"
client
changes
expect "$second" uncertain 'before the rotated signers are published'
"${publish[@]}" > "$w/out" 2> "$w/err" || fail 'the publisher failed after the rotation'
rotated="$(git -C "$instance" rev-parse refs/the-valley/signers)"
[ "$rotated" != "$published" ] || fail 'the rotated signers were not published'
git -C "$instance" cat-file blob "$rotated:known-signers" | cmp -s - "$signers" \
  || fail 'the publication does not hold the rotated signers file'
client
changes
expect "$second" landed-recorded 'after the rotated signers are published'

touch "$out"
