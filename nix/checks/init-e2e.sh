# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
export HOME="$TMPDIR"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name valley-check
git config --global user.email valley-check@localhost
git config --global init.defaultBranch main
cd "$TMPDIR" || exit 1

# The rendered valley-init script, run as the host runs it. Relocating
# the data directory is the only edit — the sandbox cannot write /srv —
# and it leaves every store path and every decision the script makes
# exactly as shipped.
data="$TMPDIR/srv/git"
sed "s|/srv/git|$data|g" "$initScriptPath" > init.sh
mkdir -p "$data"

fail() {
  echo "init-e2e: $1" >&2
  exit 1
}
hook() { echo "$data/$1.git/hooks/pre-receive"; }
# The store path the declaration says a project's hook must be.
declared() {
  grep -o "/nix/store/[^ ]*-valley-protect-$1" "$initScriptPath" | head -n1
}
is_declared_hook() {
  local link
  link="$(readlink "$(hook "$1")")" || fail "$1 has no pre-receive symlink"
  [ "$link" = "$(declared "$1")" ] || fail "$1 points at $link, not its declared hook"
  [ -x "$(hook "$1")" ] || fail "$1 has a pre-receive that is not executable"
}

# Three bare repositories that already exist, each in a state the module
# has to converge from. None of them was created by valley-init: this is
# the path a host takes when protection is declared over repositories
# that predate the declaration.
#
#   guarded   real history, no hooks at all — nothing to converge from
#   released  a hand-written pre-receive, which is not the module's
#   open      a stale managed hook, for protection no longer declared
for name in guarded released open; do
  git init --quiet --bare "$data/$name.git"
done
git clone --quiet "$data/guarded.git" work
git -C work commit --quiet --allow-empty -m one
git -C work push --quiet origin main
before="$(git -C work rev-parse HEAD)"

printf '#!/bin/sh\nexit 0\n' > "$(hook released)"
chmod +x "$(hook released)"
ln -s "$(declared guarded)" "$(hook open)"

bash init.sh || fail "the rendered init script failed over existing repositories"

# The declared hook is installed where there was none, and the history
# that was already there is untouched — an existing repository is wired,
# never re-initialized.
is_declared_hook guarded
[ "$(git -C "$data/guarded.git" rev-parse main)" = "$before" ] ||
  fail "guarded lost the history it already had"

# A hook the module did not write is not the module's to replace, even
# where the declaration protects the project.
[ ! -L "$(hook released)" ] || fail "a hand-written hook was replaced by the managed one"
[ "$(cat "$(hook released)")" = "$(printf '#!/bin/sh\nexit 0\n')" ] ||
  fail "a hand-written hook was rewritten"

# A project that declares no protection ends with no managed hook,
# whatever it was carrying before.
[ ! -e "$(hook open)" ] || fail "open kept a managed hook it no longer declares"

# Running it again changes nothing, and running it over a hook that has
# drifted to some other store path re-points it: the script is level
# triggered, so what a repository carries now does not decide what it
# ends up with.
bash init.sh || fail "the rendered init script failed on a second run"
is_declared_hook guarded
ln -sfn "$(declared released)" "$(hook guarded)"
rm -f "$(hook released)"
bash init.sh || fail "the rendered init script failed over a drifted hook"
is_declared_hook guarded
is_declared_hook released

# The converged hook is live. Pushing as a principal is pushing with the
# tag on; pushing with an untagged key is pushing with it off — the same
# substitution for sshd that protect-e2e makes.
git -C work commit --quiet --allow-empty -m two
if env -u VALLEY_PRINCIPAL git -C work push --quiet origin main 2> denied.err; then
  fail "an untagged key wrote a protected ref of a pre-existing repository"
fi
grep -q '<untagged key>' denied.err
grep -q 'refs/heads/main' denied.err
[ "$(git -C "$data/guarded.git" rev-parse main)" = "$before" ] ||
  fail "guarded kept a ref through a rejected push"
VALLEY_PRINCIPAL=integrator git -C work push --quiet origin main
[ "$(git -C "$data/guarded.git" rev-parse main)" = "$(git -C work rev-parse HEAD)" ] ||
  fail "a declared writer could not write a protected ref"

# The same declaration with the controllers switched on. A controller
# writes the worktree metadata git keeps under $GIT_DIR/worktrees, and git
# creates that directory on the first `worktree add` as whichever user got
# there first. init has to get there first: the share sweep runs as the git
# user, and a root a controller owns is one the git user cannot chmod.
sed "s|/srv/git|$data|g" "$integratorInitScriptPath" > init-integrator.sh
bash init-integrator.sh 2> share.err ||
  fail "the rendered init script failed with controllers enabled"
for name in guarded released; do
  root="$data/$name.git/worktrees"
  [ -d "$root" ] || fail "$name has no worktrees root for a controller to write into"
  perms="$(stat -c %A "$root")"
  # Group-writable is asserted; setgid is not. This sandbox refuses to set
  # it — see below — and what the module asks for is pinned by module-eval
  # over the rendered script instead.
  [ "${perms:4:2}" = rw ] ||
    fail "$name's worktrees root is $perms — a controller cannot write into it"
done
[ ! -e "$data/open.git/worktrees" ] ||
  fail "a project no controller serves was given a worktrees root"

# A share the git user is not allowed to apply must warn and leave init
# standing. Every valley unit requires valley-init, so a failure here costs
# the host its git service over one repository's permissions, and a
# degraded share costs the integrator the paths it cannot write. The exit
# status above is the assertion; this pairs a refusal with its warning
# wherever one happened, rather than requiring one. As it stands the
# sandbox refuses the setgid bit, so the path is exercised on every run —
# but the check does not depend on that staying true.
if grep -q 'Operation not permitted' share.err; then
  grep -q 'valley-init:.*does not own' share.err ||
    fail "a refused chmod went through without a warning"
fi

touch "$out"
