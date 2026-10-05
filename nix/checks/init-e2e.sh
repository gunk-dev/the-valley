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
# the path a host takes when the module is deployed over repositories that
# predate it.
#
#   guarded   real history, no hooks at all — nothing to converge from
#   released  a hand-written pre-receive identical to the hook released
#             declares it composes after the push policy
#   open      a stale managed hook: a store symlink to another hook
for name in guarded released open; do
  git init --quiet --bare "$data/$name.git"
done
git clone --quiet "$data/guarded.git" work
git -C work commit --quiet --allow-empty -m one
git -C work push --quiet origin main
before="$(git -C work rev-parse HEAD)"

composed="$(grep -o -- '--then /nix/store/[^ ]*' "$(declared released)" | cut -d' ' -f2)"
[ -n "$composed" ] || fail "released's managed hook names no composed hook"
cp "$composed" "$(hook released)"
chmod +x "$(hook released)"
ln -s "$(declared guarded)" "$(hook open)"

# A replacement ref written before any hook refused one. The hook cannot
# remove what is already there, so init reports it on every activation and
# leaves it in place: what it is evidence of is the operator's to read.
decoy="$(git -C "$data/guarded.git" commit-tree "$before^{tree}" -m decoy)"
git -C "$data/guarded.git" update-ref "refs/replace/$before" "$decoy"

bash init.sh 2> init.err || fail "the rendered init script failed over existing repositories: $(cat init.err)"
grep -q "guarded.git holds replacement refs" init.err ||
  fail "a replacement ref already in a repository was not reported"
grep -q "refs/replace/$before" init.err ||
  fail "the report did not name the replacement ref"
git -C "$data/guarded.git" rev-parse --verify --quiet "refs/replace/$before" > /dev/null ||
  fail "init deleted a replacement ref instead of reporting it"
git -C "$data/guarded.git" update-ref -d "refs/replace/$before"

# The declared hook is installed where there was none, and the history
# that was already there is untouched — an existing repository is wired,
# never re-initialized.
is_declared_hook guarded
[ "$(git -C "$data/guarded.git" rev-parse main)" = "$before" ] ||
  fail "guarded lost the history it already had"

# A hand-written hook that is byte for byte the one released composes is
# the one case init replaces: the managed hook runs it after the policy.
is_declared_hook released
grep -q "is the one services.valley.extraPreReceive.released composes" init.err ||
  fail "a composed hook was replaced without saying so"

# A project that declares no protection gets the managed hook all the
# same — the push policy is every project's — re-pointed from the stale
# store path it carried.
is_declared_hook open

# Each conflict fails init. It says which repository and why, leaves what
# it found in place, and still converges every repository it can.
conflict() {
  local what="$1" says="$2"
  if bash init.sh 2> conflict.err; then
    fail "init reported the host converged over $what"
  fi
  grep -qF -- "$says" conflict.err || fail "the refusal over $what did not say \"$says\": $(cat conflict.err)"
  grep -q 'refusing to report this host converged' conflict.err ||
    fail "init failed over $what without saying the host is not converged"
}
exit_zero="$(printf '#!/bin/sh\nexit 0\n')"

# A hook nobody declared, which would run instead of the policy.
rm "$(hook guarded)"
printf '%s\n' "$exit_zero" > "$(hook guarded)"
chmod +x "$(hook guarded)"
ln -sfn "$(declared guarded)" "$(hook open)"
conflict "a hand-written hook" "guarded: $(hook guarded) is a pre-receive hook this module did not write"
[ "$(cat "$(hook guarded)")" = "$exit_zero" ] || fail "init rewrote a hook it refused"
is_declared_hook open
rm "$(hook guarded)"

# A hand-written hook where a project composes one, that is not the one it
# composes.
rm "$(hook released)"
printf '%s\n' "$exit_zero" > "$(hook released)"
chmod +x "$(hook released)"
conflict "a hook that differs from the declared composition" "released: $(hook released) is a pre-receive hook this module did not write"
rm "$(hook released)"

# A hooks path, from the repository's config, from a file it includes, and
# from the git user's own config: each sends git to look for hooks
# somewhere the managed hook is not.
git -C "$data/guarded.git" config core.hooksPath "$TMPDIR/elsewhere"
conflict "a repository's core.hooksPath" "guarded: core.hooksPath is set for $data/guarded.git (to '$TMPDIR/elsewhere')"
git -C "$data/guarded.git" config --unset core.hooksPath
printf '[core]\n\thooksPath = %s\n' "$TMPDIR/included" > "$TMPDIR/hooks.inc"
git -C "$data/guarded.git" config include.path "$TMPDIR/hooks.inc"
conflict "an included core.hooksPath" "guarded: core.hooksPath is set for $data/guarded.git (to '$TMPDIR/included')"
git -C "$data/guarded.git" config --unset include.path
git config --global core.hooksPath "$TMPDIR/global"
conflict "the git user's core.hooksPath" "core.hooksPath is set for $data/guarded.git (to '$TMPDIR/global')"
git config --global --unset core.hooksPath
# An empty value is a value: git then looks for hooks relative to where it
# runs, which is not where the managed hook is.
git -C "$data/guarded.git" config core.hooksPath ""
conflict "an empty core.hooksPath" "core.hooksPath is set for $data/guarded.git (to '')"
git -C "$data/guarded.git" config --unset core.hooksPath

# While init is failing, the record the git user's shell lets pushes
# through on is gone: a failed convergence leaves no write path open.
[ ! -e "$data/.valley-converged" ] || fail "a failed init left the converged record in place"

# With every conflict gone, init converges, and running it again changes
# nothing: the script is level triggered, so what a repository carries now
# does not decide what it ends up with.
bash init.sh || fail "the rendered init script failed once every conflict was gone"
# Converged, and the record names this configuration.
id="$(awk -v r="$data/.valley-converged.tmp" '$1 == "printf" && $NF == r { print $3 }' init.sh)"
[ -n "$id" ] && [ "$(cat "$data/.valley-converged")" = "$id" ] ||
  fail "a converged init did not record the configuration it converged on"
bash init.sh || fail "the rendered init script failed on a second run"
for name in guarded released open; do
  is_declared_hook "$name"
done
ln -sfn "$(declared released)" "$(hook guarded)"
bash init.sh || fail "the rendered init script failed over a drifted hook"
is_declared_hook guarded

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
