# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
# The repo init list and mirror hook must be generated from
# the CUE export, and the git user's sshd Match block must
# be terminated so it cannot scope later config.
grep -q "the-valley" "$initScriptPath"
grep -q "valley-mirrors" "$initScriptPath"
grep -q "Match All" "$sshdConfigPath"

# The mirror publishes main and tags only — a heads glob
# would publish every topic branch awaiting review — with
# --prune for tag deletions and a sweep that unpublishes
# any other head (--prune cannot: it ignores non-glob
# refspecs). Never --mirror: it also deletes remote-only
# refs, and GitHub's read-only refs/pull/* fails every such
# push. Follow the hook chain from the init script to the
# rendered push script and pin the invocation there.
mirrorHook="$(grep -o '/nix/store/[^ ]*-valley-mirrors-[^ ]*' "$initScriptPath" | head -n1)"
mirrorPush="$(grep -o '/nix/store/[^ ]*-valley-mirror-push-[^ ]*' "$mirrorHook" | head -n1)"
grep -q -- 'push --prune' "$mirrorPush"
grep -qF -- '+refs/heads/main:refs/heads/main' "$mirrorPush"
grep -qF -- '+refs/tags/*:refs/tags/*' "$mirrorPush"
grep -qF -- 'ls-remote --heads' "$mirrorPush"
# shellcheck disable=SC2016  # the text pinned is the script's, not ours
grep -qF -- 'push "$url" --delete' "$mirrorPush"
if grep -qF -- '+refs/heads/*:refs/heads/*' "$mirrorPush"; then
  echo "module-eval: mirror push regressed to publishing every head" >&2
  exit 1
fi
if grep -q -- '--mirror' "$mirrorPush"; then
  echo "module-eval: mirror push regressed to --mirror" >&2
  exit 1
fi
# Deletions must never reach a namespace the mirror owns.
if grep -q 'refs/pull' "$mirrorPush"; then
  echo "module-eval: mirror push must not name refs/pull/*" >&2
  exit 1
fi

# The rendered restic units must back up the data directory
# to the consumer-supplied repository with the declared
# retention over a pinned host key, on the declared cadence.
grep -q "RESTIC_REPOSITORY_FILE=/run/agenix/valley-restic-repo" "$resticServicePath"
grep -q -- "--keep-daily 7 --keep-weekly 4 --keep-monthly 6" "$resticServicePath"
grep -q "UserKnownHostsFile=/var/lib/valley-backup/known_hosts" "$resticServicePath"
grep -q "BatchMode=yes" "$resticServicePath"
grep -q "OnCalendar=03:30" "$resticTimerPath"

# The backup paths render as a --files-from list: follow
# ExecStartPre to that list and pin the data directory.
preStart="$(sed -n 's/^ExecStartPre=//p' "$resticServicePath" | head -n1)"
staticPaths="$(grep -o '/nix/store/[^ ]*-staticPaths' "$preStart" | head -n1)"
grep -qx "/srv/git" "$staticPaths"

# A key's principal rides on its own authorized_keys entry
# — that is the only thing that tells one pusher from
# another — and a key written as a plain string is passed
# through untouched.
grep -q '^environment="VALLEY_PRINCIPAL=integrator" ssh-ed25519 ' "$protectedKeysPath"
grep -q '^ssh-ed25519 .* valley-check$' "$protectedKeysPath"

# The hook goes on the projects whose declaration has a
# protection block, and nowhere else.
grep -q "valley-protect-guarded" "$protectedInitPath"
grep -q "valley-protect-released" "$protectedInitPath"
if grep -q "valley-protect-open" "$protectedInitPath"; then
  echo "module-eval: a project nobody protected was wired with the hook" >&2
  exit 1
fi

# And what it enforces is what the declaration says, down
# to the rendered script: released names a pattern beside
# main, guarded takes the schema's default set. Follow the
# hook chain from the init script to each script.
guardedHook="$(grep -o '/nix/store/[^ ]*-valley-protect-guarded' "$protectedInitPath" | head -n1)"
releasedHook="$(grep -o '/nix/store/[^ ]*-valley-protect-released' "$protectedInitPath" | head -n1)"
grep -qF -- 'protected=( refs/heads/main )' "$guardedHook"
grep -qF -- "protected=( refs/heads/main 'refs/heads/release/*' )" "$releasedHook"
grep -qF -- 'writers=( integrator )' "$guardedHook"

# A controller is given the repository it serves and the
# identity it acts under, and nothing about policy: it reads
# the project layer from the target tip and the instance
# layer from its own side.
grep -q -- '--repo /srv/git/%i.git' "$integratorUnitPath"
grep -q -- '--key /run/agenix/valley-integrator-key' "$integratorUnitPath"
grep -q -- '--known-signers /var/lib/valley-instance/known_signers' "$integratorUnitPath"
# The floor's source, not a copy of it: the repository whose
# integrated tip carries it. It is named, and it is not the
# repository the controller serves — every controller here
# reads the floor out of "open", which no controller serves.
grep -q -- '--instance-repo /srv/git/open.git' "$integratorUnitPath"
if grep -q -- '--project-policy' "$integratorUnitPath"; then
  echo "module-eval: the module must not tell a controller where the project's policy is" >&2
  exit 1
fi

# It does not own the repositories, so git needs an ownership
# exception, and the rendered unit is where it has to appear:
# GIT_CONFIG_* is command scope, which is the only scope besides
# system and global that safe.directory is read from, and the
# environment reaches every git the controller drives. One
# repository, this instance's.
# A second exception for the instance repository, which carries the
# group's floor and is read by every controller — including the ones
# that serve some other project.
grep -qF -- 'Environment="GIT_CONFIG_COUNT=2"' "$integratorUnitPath"
grep -qF -- 'Environment="GIT_CONFIG_KEY_0=safe.directory"' "$integratorUnitPath"
grep -qF -- 'Environment="GIT_CONFIG_VALUE_0=/srv/git/%i.git"' "$integratorUnitPath"
grep -qF -- 'Environment="GIT_CONFIG_KEY_1=safe.directory"' "$integratorUnitPath"
grep -qF -- 'Environment="GIT_CONFIG_VALUE_1=/srv/git/open.git"' "$integratorUnitPath"

# It writes refs as itself, so the repositories it serves are
# group-shared — and only those. The sharing block names each
# repo it touches; the repo-creation loop above it names none.
grep -qxF -- "repo=/srv/git/guarded.git" "$integratorInitPath"
grep -qxF -- "repo=/srv/git/released.git" "$integratorInitPath"
grep -qF -- 'config core.sharedRepository group' "$integratorInitPath"
# "open" is protected by nobody, so no controller serves it — and on
# this host it is the instance repository, which every controller reads
# the floor from. Read is the whole of that grant: no group write, and
# no core.sharedRepository, which is a statement about who writes.
open_block="$(awk '/^repo=/ { inside = ($0 == "repo=/srv/git/open.git") } inside' "$integratorInitPath")"
if [ -z "$open_block" ]; then
  echo "module-eval: the instance repository must be readable by the git group even where no controller serves it" >&2
  exit 1
fi
if ! printf '%s\n' "$open_block" | grep -qF -- 'chmod -R g+rX "$repo"'; then
  echo "module-eval: the instance repository must be made group-readable" >&2
  exit 1
fi
if printf '%s\n' "$open_block" | grep -qE 'g\+rwX|sharedRepository'; then
  echo "module-eval: a project nobody protected was made group-writable — the floor is read, never written" >&2
  exit 1
fi

# git creates $GIT_DIR/worktrees on the first `worktree add`, owned by
# whoever got there first. init has to get there first, because the
# sweep below it runs as the git user and cannot chmod what a
# controller owns. Both the creation and its order are pinned: made
# after the repo-creation loop, before the recursive chmod that would
# otherwise be the first thing to meet it.
mkdir_line="$(grep -nF -- 'mkdir -p "$repo/worktrees"' "$integratorInitPath" | head -n1 | cut -d: -f1)"
chmod_line="$(grep -nF -- 'chmod -R g+rwX "$repo"' "$integratorInitPath" | head -n1 | cut -d: -f1)"
if [ -z "$mkdir_line" ]; then
  echo "module-eval: valley-init must create each served repository's worktrees root — a controller that creates it first owns a directory the git user cannot chmod" >&2
  exit 1
fi
if [ -z "$chmod_line" ] || [ "$mkdir_line" -ge "$chmod_line" ]; then
  echo "module-eval: the worktrees root must be created before the share sweep reaches it" >&2
  exit 1
fi
grep -qF -- 'chmod g+rwxs "$repo/worktrees"' "$integratorInitPath"

# A chmod the git user is not allowed to make must not fail valley-init:
# every valley unit requires it, so a failure there costs the host its
# git service rather than one repository's group share.
grep -qF -- 'share chmod -R g+rwX "$repo"' "$integratorInitPath"
if ! grep -qF -- 'echo "valley-init:' "$integratorInitPath"; then
  echo "module-eval: a share the git user cannot apply must warn, not fail — valley-init failing takes every valley unit with it" >&2
  exit 1
fi
touch "$out"
