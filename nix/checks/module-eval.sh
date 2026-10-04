# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
# The repo init list and mirror hook must be generated from
# the CUE export, and the git user's sshd Match block must
# be terminated so it cannot scope later config.
grep -q "the-valley" "$initScriptPath"
grep -q "valley-mirrors" "$initScriptPath"
grep -q "Match All" "$sshdConfigPath"
# The git user's keys come from the declared file and nothing
# else: not ~/.ssh/authorized_keys, not a command. Every key it
# accepts therefore carries a tag this module wrote.
grep -qx '  AuthorizedKeysFile /etc/ssh/authorized_keys.d/%u' "$sshdConfigPath"
grep -qx '  AuthorizedKeysCommand none' "$sshdConfigPath"

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

# The hook goes on every project the host serves, protected
# or not: a protection block only adds protected refs to the
# push policy, and "open" declares none.
for name in sealed guarded released open; do
  grep -q "valley-protect-$name" "$protectedInitPath"
done

# And what it enforces is what the declaration says, down
# to the file the hook reads: released names a pattern beside
# main and grants release tags, guarded takes the schema's
# default set, open protects nothing. Follow the hook chain
# from the init script to each script, and from each script to
# its push policy, which is the declaration's protected refs,
# writers and grants exported to JSON.
guardedHook="$(grep -o '/nix/store/[^ ]*-valley-protect-guarded' "$protectedInitPath" | head -n1)"
releasedHook="$(grep -o '/nix/store/[^ ]*-valley-protect-released' "$protectedInitPath" | head -n1)"
sealedHook="$(grep -o '/nix/store/[^ ]*-valley-protect-sealed' "$protectedInitPath" | head -n1)"
openHook="$(grep -o '/nix/store/[^ ]*-valley-protect-open' "$protectedInitPath" | head -n1)"
policy() { grep -o '/nix/store/[^ ]*-valley-push-policy-[^ ]*\.json' "$1" | head -n1; }
grep -qF -- '"refs":["refs/heads/main"]' "$(policy "$guardedHook")"
grep -qF -- '"writers":["integrator"]' "$(policy "$guardedHook")"
grep -qF -- '"refs":["refs/heads/main","refs/heads/release/*","refs/the-valley/integration-requests/*"]' "$(policy "$releasedHook")"
grep -qF -- '"grants":{"release-tags":{"refs":["refs/tags/release/*"],"writers":["integrator"]}}' "$(policy "$releasedHook")"
# The norm: a protected ref with no writer declared, and
# nothing granted. Both render empty rather than missing.
grep -qF -- '"writers":[]' "$(policy "$sealedHook")"
grep -qF -- '"grants":{}' "$(policy "$sealedHook")"
# A project with no protection block protects nothing, and
# is held to the rest of the policy all the same.
grep -qF -- '{"grants":{},"refs":[],"writers":[]}' "$(policy "$openHook")"

# A project that composes a hook after the policy hands it to
# valleyhook to run, and only that project does.
grep -q -- '--then /nix/store/[^ ]*-valley-check-released-pre-receive' "$releasedHook"
if grep -q -- '--then' "$guardedHook"; then
  echo "module-eval: a project that composes no hook was handed one" >&2
  exit 1
fi

# A hook the module did not write, or a hooks path that sends
# git elsewhere, fails init rather than being left in place:
# either would run instead of the policy.
grep -qF -- 'conflicts=1' "$protectedInitPath"
grep -qF -- 'config --get core.hooksPath' "$protectedInitPath"
grep -qF -- 'cmp -s "$phook" "$composed"' "$protectedInitPath"
grep -qF -- '/srv/git/.ssh/environment' "$protectedInitPath"
tail -n 5 "$protectedInitPath" > init-tail
grep -qF -- 'exit 1' init-tail

# The rules are valleyhook's, and the script is only how git
# reaches it: it hands over the principal sshd tagged the
# push with, and nothing it decides itself.
grep -qF -- 'valleyhook pre-receive' "$guardedHook"
# shellcheck disable=SC2016  # the text pinned is the script's, not ours
grep -qF -- '--principal="${VALLEY_PRINCIPAL:-}"' "$guardedHook"
if grep -qE '^ *(case|for|while|if) ' "$guardedHook"; then
  echo "module-eval: the rendered hook decides something itself — the policy belongs to valleyhook" >&2
  exit 1
fi

# The grants it reads. A grant declared by hand renders into
# a file in the format the compiler writes, and a host that
# compiles no registry reads that file alone.
grants="$(grep -o '/nix/store/[^ ]*-valley-grants' "$guardedHook" | head -n1)"
grep -qx 'request requester' "$grants"
if grep -q '/var/lib/valley-identity' "$guardedHook"; then
  echo "module-eval: a host that compiles no registry reads compiled grants" >&2
  exit 1
fi
# A host that compiles one reads the compiled grants beside
# the declared ones, which a compiled file can only add to.
identityHook="$(grep -o '/nix/store/[^ ]*-valley-protect-guarded' "$identityInitPath" | head -n1)"
grep -qF -- '--grants /var/lib/valley-identity/grants' "$identityHook"
grep -q -- '-valley-grants ' "$identityHook"

# Replacement refs a push wrote before the hook refused them
# are reported on every activation, in every repository.
grep -qF -- 'for-each-ref' "$protectedInitPath"
grep -qF -- 'refs/replace/' "$protectedInitPath"

# A controller is given the repository it serves and the
# identity it acts under, and nothing about policy: it reads
# the project layer from the target tip and the instance
# layer from its own side.
grep -q -- '--repo /srv/git/%i.git' "$integratorUnitPath"
grep -q -- '--key /run/agenix/valley-integrator-key' "$integratorUnitPath"
grep -q -- '--known-signers /var/lib/valley-instance/known_signers' "$integratorUnitPath"
# The floor's source, not a copy of it: the repository whose
# integrated tip carries it. It is named, and for every
# controller here but sealed's it is not the repository the
# controller serves.
grep -q -- '--instance-repo /srv/git/sealed.git' "$integratorUnitPath"
if grep -q -- '--project-policy' "$integratorUnitPath"; then
  echo "module-eval: the module must not tell a controller where the project's policy is" >&2
  exit 1
fi
# Nor where the floor is inside that repository. Both directories are the
# integrator's defaults — policy/instance and policy/project — so a host
# declaration that says nothing about policy gets the convention.
if grep -q -- '--instance-policy' "$integratorUnitPath"; then
  echo "module-eval: the module must not tell a controller where the floor is in the instance repository" >&2
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
grep -qF -- 'Environment="GIT_CONFIG_VALUE_1=/srv/git/sealed.git"' "$integratorUnitPath"

# It writes refs as itself, so the repositories it serves are
# group-shared — and only those. The sharing block names each
# repo it touches; the repo-creation loop above it names none.
grep -qxF -- "repo=/srv/git/guarded.git" "$integratorInitPath"
grep -qxF -- "repo=/srv/git/released.git" "$integratorInitPath"
grep -qF -- 'config core.sharedRepository group' "$integratorInitPath"
# The instance repository declares protection, so a controller serves
# it and it is shared like every other served repository.
grep -qxF -- "repo=/srv/git/sealed.git" "$integratorInitPath"
# "open" is protected by nobody, so no controller serves it and nothing
# shares it.
if grep -qxF -- "repo=/srv/git/open.git" "$integratorInitPath"; then
  echo "module-eval: a project nobody protected was made group-writable" >&2
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
