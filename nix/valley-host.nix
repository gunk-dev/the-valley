# The valley host installer: declarative bare-git hosting over SSH, driven
# by a CUE host declaration (../schema/valley.cue).
#
# Layering: the CUE file owns the domain model — which projects exist, their
# stores, their push mirrors, whether the host's data has offsite backup and
# with what cadence and retention. This module owns machine integration
# only: data directory, unix user, SSH keys, backup credentials. At build
# time the CUE config is vetted against the shipped schema and exported to
# JSON; an invalid config fails the system build with cue's error. Nix
# never redefines the schema.
#
# Repos are only ever created, never deleted or overwritten — removing a
# project (or disabling its git store) leaves the data on disk untouched.
#
# Identity is deliberately thin and host-level: one git user, git-shell,
# key-only, Tailscale ACLs in front. Per-project *access* is not honestly
# enforceable with this mechanism, so it is deliberately not an option
# (archive/.the-valley/decisions/dcr-0f5d9b1-cue-config-host-module.md). Per-project
# *write protection* is, because the pre-receive hook below is a real
# enforcement boundary and each key carries a principal name it can read
# (archive/.the-valley/decisions/dcr-b87f6e8-identity-is-a-governed-registry.md) —
# so it is declared, in the project's `protection` block, and this module
# only binds principal names to keys and grants, and installs the hook.
# Those bindings are declared by hand or compiled from the instance's
# identity registry, and the compiler is off until a consumer turns it on.
#
# Mirror credentials are the consumer's concern: the module assumes the git
# user's SSH identity and known_hosts are provisioned by the host (e.g.
# cosmo, via its secrets). Every mirror push runs as the git user, whether
# a push or an integrator landing moved the ref. Nothing here plumbs
# secrets: the backup options below take *paths* to consumer-provisioned
# secret files; the contents never pass through this module or the store.
#
# One service runs under its own unix user rather than the git one: the
# integrator. That is the first half of the split bd-500adf7 asks for.
{
  config,
  pkgs,
  lib,
  ...
}:

let
  cfg = config.services.valley;

  schema = ../schema/valley.cue;

  # Build-time vet + export of the host declaration. the vet checks the
  # declaration against the schema (whose top level applies #Host), so unknown
  # fields and unsafe project names fail the build, with cue's error.
  configJSON =
    pkgs.runCommand "valley-config.json"
      {
        nativeBuildInputs = [ pkgs.cue ];
      }
      ''
        cue vet -c ${schema} ${cfg.config}
        cue export ${schema} ${cfg.config} > $out
      '';

  host = lib.importJSON configJSON;

  # Projects whose git store is enabled get a bare repository.
  gitProjects = lib.filterAttrs (_: p: p.git.enable) host.projects;
  repoNames = lib.attrNames gitProjects;

  # The declared durability policy, if any. `backup` is optional in the
  # schema: a declaration without it (or with enable = false) renders zero
  # backup machinery, exactly as before the field existed.
  backupPolicy = host.backup or null;
  backupEnabled = backupPolicy != null && backupPolicy.enable;

  # The declared cadence names policy; its wall-clock rendering is this
  # installer's choice. A lookup rather than a literal, so a cadence this
  # module does not know fails eval loudly instead of mis-scheduling.
  backupTimer = {
    # Late night with a spread; Persistent runs a missed window at boot.
    nightly = {
      OnCalendar = "03:30";
      RandomizedDelaySec = "30m";
      Persistent = true;
    };
  };

  # The projects the integrator serves: exactly the ones the declaration
  # protects. A protected ref is one only a declared writer may push, and
  # the integrator is what lands everyone else's changes onto it.
  integratedProjects = lib.filterAttrs (_: p: p ? protection) gitProjects;

  # The integrator this flake builds, in its shipping form: attest, the
  # deriver, cue, git and natscli pinned inside the wrapper, so every host
  # judges with the same tools (nix/packages.nix).
  integratorPackage = (import ./packages.nix { inherit pkgs lib; }).integrator;

  # The whole of a controller's configuration: which repository it serves,
  # who it signs as, and whose evidence it accepts. No policy — the
  # integrator reads the project layer from the target tip and the instance
  # layer from its own side (bd-eaefe82) — and no project name, which the
  # binary derives from the bare repo's own directory, the same way the
  # post-receive hook does.
  integratorCommand = lib.concatStringsSep " " (
    [
      "${integratorPackage}/bin/integrator watch"
      "--repo ${cfg.dataDir}/%i.git"
      "--key ${toString cfg.integrator.signingKeyFile}"
      "--known-signers ${knownSigners}"
      "--interval ${cfg.integrator.interval}"
    ]
    ++ lib.optional (cfg.integrator.signingName != null) "--name ${cfg.integrator.signingName}"
    ++ (
      if cfg.integrator.instanceProject != null then
        [ "--instance-repo ${instanceRepo}" ]
      else
        [ "--instance ${toString cfg.integrator.instancePolicy}" ]
    )
    ++ lib.optional cfg.bus.enable "--bus ${busUrl}"
    ++ [ "--publish-queue ${cfg.dataDir}/%i.git/${publishQueueName}" ]
  );

  # The one filesystem grant the integrator's user gets. It writes refs,
  # objects, and the worktree metadata `git worktree add` keeps inside the
  # repository, so read access is not enough: the repos it serves become
  # group-shared, and its primary group is the git group. Re-applied on
  # every activation, because a repo can predate the service.
  # git creates $GIT_DIR/worktrees on the first `worktree add`, owned by
  # whichever user got there first. Left alone that user is the integrator,
  # and this sweep runs as the git user, which cannot chmod a directory it
  # does not own. So init makes that directory itself, ahead of any
  # controller: owned by the git user, group-writable, and setgid, so what a
  # controller creates inside it lands in the git group and stays reachable.
  integratorShareCommands =
    ''
      # A chmod the git user is not allowed to make is a path some other
      # user owns; chmod -R applies what it can and then says so. Warn and
      # carry on, and the trade is worth stating. Failing here fails
      # valley-init, which every valley unit requires, so the alternative to
      # a partly shared repository is no git service on the host at all: no
      # controllers, and no ssh access to any project. A degraded share
      # costs the integrator the paths it cannot write, is visible in the
      # journal, and is fixed by one chown. That is the recoverable half of
      # the trade, and it is the half to take. The warning is what keeps the
      # degraded state from being a silent one.
      share() {
        "$@" || echo "valley-init: $* failed — a path the git user does not own is left as it was, and the integrator may be unable to write it" >&2
      }
    ''
    + lib.concatMapStrings (name: ''
      repo=${lib.escapeShellArg "${cfg.dataDir}/${name}.git"}
      git -C "$repo" config core.sharedRepository group
      # Before the sweep below, so the sweep never meets a root it cannot
      # touch. On a host where a controller already made it, this is the
      # chmod that warns, and the repository is served either way.
      mkdir -p "$repo/worktrees"
      share chmod g+rwxs "$repo/worktrees"
      # The publish queue is the git user's for the same reason: the
      # integrator writes into it and the publish unit deletes from it.
      mkdir -p "$repo/${publishQueueName}"
      share chmod g+rwxs "$repo/${publishQueueName}"
      share chmod -R g+rwX "$repo"
      share find "$repo" -type d -exec chmod g+s {} +
    '') (lib.attrNames integratedProjects);

  # The integrator's own directory. Four unit settings have to agree about
  # it — its state directory, its working directory, the scratch root TMPDIR
  # names, and the read-write path that makes all three usable — so they name
  # it once here.
  # The bare repository the group's floor is read from: the instance
  # project's, served by this host. Null when the host serves no instance
  # repository and the layer is supplied as a directory instead.
  instanceRepo =
    if cfg.integrator.instanceProject != null then
      "${cfg.dataDir}/${cfg.integrator.instanceProject}.git"
    else
      null;

  integratorStateName = "valley-integrator";
  integratorStateDir = "/var/lib/${integratorStateName}";

  # The identity registry compiler this flake builds. It renders the
  # registry at the instance repository's integrated tip into what the gates
  # check (dcr-b87f6e8): the known-signers file the integrator and any
  # reader of an attestation use, and a tagged authorized_keys for the git
  # user.
  identityPackage = (import ./packages.nix { inherit pkgs lib; }).identity;

  identityStateName = "valley-identity";
  identityStateDir = "/var/lib/${identityStateName}";
  identityKnownSigners = "${identityStateDir}/known-signers";

  # The grants the pre-receive hook checks, as the registry names them.
  identityGrants = "${identityStateDir}/grants";

  # The compiled authorized keys. sshd reads them for the git user alone:
  # the git user's Match block below names the files its keys come from.
  identityAuthorizedKeys = "${identityStateDir}/authorized_keys.${cfg.user}";

  identityCommand = lib.concatStringsSep " " [
    "${identityPackage}/bin/identity compile"
    "--repo ${toString instanceRepo}"
    "--ref ${cfg.identity.ref}"
    "--dir ${cfg.identity.directory}"
    "--known-signers ${identityKnownSigners}"
    "--authorized-keys ${identityAuthorizedKeys}"
    "--grants ${identityGrants}"
    "--declared-keys ${declaredKeysFile}"
  ];

  # Whose evidence a controller accepts: the compiled artifact once the
  # registry machinery is on, and the file the consumer supplied otherwise.
  knownSigners =
    if cfg.identity.enable then identityKnownSigners else toString cfg.integrator.knownSignersFile;

  # The paths the consumer must supply once the integrator is enabled,
  # with what each names — for the assertion message. The signers file is
  # among them only while the registry does not compile one.
  integratorPathOptions = {
    signingKeyFile = "the key its transfer statements are signed with";
  }
  // lib.optionalAttrs (!cfg.identity.enable) {
    knownSignersFile = "the signers whose evidence it accepts";
  };

  # The secret-path options the consumer must supply once the declaration
  # enables backup, with what each names — for the assertion message.
  backupSecretOptions = {
    repositoryFile = "the restic repository URL";
    passwordFile = "the repository encryption password";
    sshKeyFile = "the SSH identity for the sftp target";
    knownHostsFile = "the pinned host key of the sftp target";
  };

  # Managed post-receive dispatcher. Each repo's post-receive is a symlink to
  # this script, which chains every executable dropped into the repo's
  # hooks/post-receive.d/ directory.
  postReceiveDispatch = pkgs.writeShellScript "valley-post-receive" ''
    # Managed by services.valley — do not edit.
    # Drop executable hooks into post-receive.d/ next to this symlink.
    set -eu
    hook_dir="$(dirname "$0")/post-receive.d"
    [ -d "$hook_dir" ] || exit 0
    updates="$(cat)"
    [ -n "$updates" ] || exit 0
    for hook in "$hook_dir"/*; do
      [ -x "$hook" ] || continue
      printf '%s\n' "$updates" | "$hook" "$@"
    done
  '';

  # Best-effort push replication. Publication mirror: main and tags only.
  # A push runs it from post-receive, through the hook below; an integrator
  # landing runs it from the publish unit further down.
  # Topic branches are review-queue state, not published — the mirror exists
  # so consumers can fetch what has been integrated; durability is restic's
  # job, not the mirror's. Pushes run detached (setsid) so a dead mirror can
  # only ever cost a log line — never block or fail the primary push.
  # --prune propagates tag deletions (glob refspec); it is a no-op for heads
  # now that the heads refspec names one ref, so a separate best-effort sweep
  # deletes every mirror head but main — which also strips anything that
  # appears there by other means. The sweep reads only refs/heads (ls-remote
  # --heads), never refs/pull/*, and is logged apart from the replication
  # push so it cannot change that push's reported outcome. --mirror is
  # rejected outright: it also tries to delete remote-only namespaces, and on
  # GitHub the read-only refs/pull/* makes that fail every push, masking real
  # replication failures.
  mirrorPusher =
    name: mirrors:
    pkgs.writeShellScript "valley-mirror-push-${name}" ''
      for url in ${lib.escapeShellArgs mirrors}; do
        if ${pkgs.git}/bin/git push --prune "$url" '+refs/heads/main:refs/heads/main' '+refs/tags/*:refs/tags/*' >/dev/null 2>&1; then
          ${pkgs.util-linux}/bin/logger -t valley-mirror "${name}: pushed to $url" || true
        else
          ${pkgs.util-linux}/bin/logger -t valley-mirror "${name}: push to $url FAILED" || true
        fi

        # Unpublish anything but main. Refnames cannot contain whitespace, so
        # accumulating them space-separated and splitting on expansion is safe.
        stale=""
        while read -r _ ref; do
          [ "$ref" = refs/heads/main ] || stale="$stale $ref"
        done < <(${pkgs.git}/bin/git ls-remote --heads "$url" 2>/dev/null)
        if [ -n "$stale" ]; then
          if ${pkgs.git}/bin/git push "$url" --delete $stale >/dev/null 2>&1; then
            ${pkgs.util-linux}/bin/logger -t valley-mirror "${name}: unpublished$stale from $url" || true
          else
            ${pkgs.util-linux}/bin/logger -t valley-mirror "${name}: unpublish from $url failed (ignored)" || true
          fi
        fi
      done
    '';

  mirrorHook =
    name: mirrors:
    pkgs.writeShellScript "valley-mirrors-${name}" ''
      # Managed by services.valley — best-effort push mirrors for ${name}.
      cat >/dev/null   # updated refs unused: the push replicates main and tags
      ${pkgs.util-linux}/bin/setsid -f ${mirrorPusher name mirrors} </dev/null >/dev/null 2>&1
      exit 0
    '';

  # The event bus (archive/design/roadmap.md, Phase 1): NATS JetStream, onto which
  # the ref-updated hook below projects this host's git activity. The bus is
  # never load-bearing for durable state — per-repo events are durable in
  # git itself, and `valley replay` rebuilds the stream from a repo's refs —
  # so losing its storage costs one replay, nothing more.
  busUrl = "nats://${cfg.bus.listen}";

  busServerConfig = pkgs.writeText "valley-bus.conf" ''
    listen: ${cfg.bus.listen}
    jetstream {
      store_dir: "${cfg.bus.storeDir}"
      # The store directory defaults to living on the repo volume; the cap
      # keeps a runaway or malicious local publisher from filling it.
      max_file_store: 2GiB
    }
  '';

  # One ref-updated event per updated ref, read from the post-receive stdin
  # this script inherits. Every payload field is derivable from git alone —
  # no wall-clock time, no hostname — so replaying a repo's refs reproduces
  # the same events (the roadmap's determinism criterion). `valley replay`
  # builds the identical payload; change one only with the other.
  busPublisher = pkgs.writeShellScript "valley-bus-publish" ''
    repo="$(basename "$PWD" .git)"
    while read -r old new ref; do
      [ -n "$ref" ] || continue
      # " is the only JSON-significant character a refname can contain
      # (git forbids \ and control characters); names and ids are safe.
      event="$(printf '{"event":"ref-updated","repo":"%s","ref":"%s","old":"%s","new":"%s"}' \
        "$repo" "''${ref//\"/\\\"}" "$old" "$new")"
      if ${pkgs.natscli}/bin/nats --server ${busUrl} pub \
        "valley.git.$repo.ref-updated" "$event" </dev/null >/dev/null 2>&1; then
        ${pkgs.util-linux}/bin/logger -t valley-bus "$repo: published ref-updated $ref" || true
      else
        ${pkgs.util-linux}/bin/logger -t valley-bus "$repo: publish of ref-updated $ref FAILED" || true
      fi
    done
  '';

  # How an integrator landing reaches the mirrors and the bus.
  #
  # A push reaches them through post-receive. The integrator moves refs
  # with update-ref, which runs no post-receive hook, and it runs as its own
  # user, which does not hold the git user's mirror credentials and must not.
  # So the integrator does not publish its landings. It writes each move it
  # makes to a target, as the line post-receive would read, into a queue
  # directory in the repository: the publish queue. A path unit watches the
  # queue, and when it holds anything, starts a oneshot running as the git
  # user. That drain hands every queued line to the bus publisher above,
  # deletes the lines, and runs the mirror pusher once. The pusher and the
  # publisher are the ones post-receive runs.
  #
  # The alternatives fall to the user split. Calling the post-receive
  # dispatcher from the integrator would push as the integrator's user, and
  # so would a reference-transaction hook, which runs as whoever ran git; it
  # would also fire inside every push and for every bookkeeping ref the
  # integrator writes. Starting a unit directly would take a polkit grant
  # and carry no record of what moved. The queue costs one directory, the
  # path unit is woken by inotify rather than a poll, and the queue is
  # durable, so a move queued while the host goes down is published after.
  #
  # Each move is published once. A push never writes the queue, and the
  # integrator never runs post-receive, so no move takes both paths. A drain
  # that dies between publishing and deleting republishes on its next run,
  # which repeats a ref-updated event and an idempotent mirror push.
  publishQueueName = "valley-publish-queue";

  # The drain, per project. It runs synchronously: the unit is already
  # detached from the landing, and a detached child would be killed with the
  # oneshot's cgroup when it exits.
  publishDrain =
    name: p:
    pkgs.writeShellScript "valley-publish-${name}" ''
      # Managed by services.valley — publish what the integrator moved on ${name}.
      shopt -s nullglob
      moves=( ${lib.escapeShellArg "${cfg.dataDir}/${name}.git/${publishQueueName}"}/* )
      [ "''${#moves[@]}" -gt 0 ] || exit 0
      ${lib.optionalString cfg.bus.enable ''cat -- "''${moves[@]}" | ${busPublisher}''}
      rm -f -- "''${moves[@]}"
      ${lib.optionalString (p.mirrors != [ ]) "${mirrorPusher name p.mirrors}"}
    '';

  # Every drain, by project name, so the template unit runs its instance's.
  publishDrains = pkgs.linkFarm "valley-publish-drains" (
    lib.mapAttrsToList (name: p: {
      inherit name;
      path = publishDrain name p;
    }) integratedProjects
  );

  # The post-receive.d hook, with the mirror hook's failure semantics: the
  # publisher runs detached (setsid, inheriting the updates on stdin), so a
  # bus problem can only ever cost a log line — never block or fail the
  # push. git is the source of truth; the bus is the replaceable component.
  busEventHook = pkgs.writeShellScript "valley-bus-events" ''
    # Managed by services.valley — project ref updates onto the event bus.
    ${pkgs.util-linux}/bin/setsid -f ${busPublisher} >/dev/null 2>&1
    exit 0
  '';

  # Bus-hook wiring, one symlink per repo. Same rules as the mirror hooks:
  # only ever installs, updates, or removes a store symlink — a hand-written
  # hook of the same name is left alone.
  busHookCommands = lib.concatMapStrings (
    name:
    let
      bhook = lib.escapeShellArg "${cfg.dataDir}/${name}.git/hooks/post-receive.d/valley-bus";
    in
    if cfg.bus.enable then
      ''
        bhook=${bhook}
        if [ -L "$bhook" ]; then
          case "$(readlink "$bhook")" in
            /nix/store/*) ln -sfn ${busEventHook} "$bhook" ;;
          esac
        elif [ ! -e "$bhook" ]; then
          ln -s ${busEventHook} "$bhook"
        fi
      ''
    else
      ''
        bhook=${bhook}
        if [ -L "$bhook" ]; then
          case "$(readlink "$bhook")" in
            /nix/store/*) rm -f "$bhook" ;;
          esac
        fi
      ''
  ) repoNames;

  # Per-project mirror-hook wiring. Only ever installs, updates, or removes
  # a store symlink — a hand-written hook of the same name is left alone.
  mirrorHookCommands = lib.concatStrings (
    lib.mapAttrsToList (
      name: p:
      let
        mhook = lib.escapeShellArg "${cfg.dataDir}/${name}.git/hooks/post-receive.d/valley-mirrors";
      in
      if p.mirrors != [ ] then
        ''
          mhook=${mhook}
          if [ -L "$mhook" ]; then
            case "$(readlink "$mhook")" in
              /nix/store/*) ln -sfn ${mirrorHook name p.mirrors} "$mhook" ;;
            esac
          elif [ ! -e "$mhook" ]; then
            ln -s ${mirrorHook name p.mirrors} "$mhook"
          fi
        ''
      else
        ''
          mhook=${mhook}
          if [ -L "$mhook" ]; then
            case "$(readlink "$mhook")" in
              /nix/store/*) rm -f "$mhook" ;;
            esac
          fi
        ''
    ) gitProjects
  );

  # The environment variable carrying the pushing key's principal name. The
  # git user's shell sets it from the tag on the authenticated key's own
  # authorized_keys entry, and the pre-receive hook reads it.
  principalEnv = "VALLEY_PRINCIPAL";

  authorizedKeyLine =
    k: if lib.isString k then k else ''environment="${principalEnv}=${k.principal}" ${k.key}'';

  # What valley-init does to converge the host: create missing repositories
  # and (re)wire the managed hooks. Its last step refuses to report the host
  # converged over anything that would run instead of the push policy.
  valleyInitBody = ''
    conflicts=0

    repos=( ${lib.escapeShellArgs repoNames} )
    for name in "''${repos[@]}"; do
      repo="${cfg.dataDir}/$name.git"
      # Check HEAD rather than the directory itself so a pre-existing
      # empty directory still gets initialized.
      if [ ! -e "$repo/HEAD" ]; then
        git init --bare --initial-branch=main "$repo"
      fi

      # Replacement refs make one object stand in for another wherever
      # git looks it up. No push may write one, and every reader that
      # makes a decision disables them, but a ref written before the
      # hook refused them is still there. It is reported on every
      # activation and left in place: what it is evidence of is the
      # operator's to read before anyone deletes it.
      replaced="$(git -C "$repo" for-each-ref --format='  %(refname) -> %(objectname)' refs/replace/)" || replaced=""
      if [ -n "$replaced" ]; then
        printf 'valley-init: %s holds replacement refs, which no push may write; inspect each and delete it with git update-ref -d:\n%s\n' "$repo" "$replaced" >&2
      fi

      # Hook scaffolding: post-receive dispatches to post-receive.d/.
      # Only manage the hook if it is absent or already ours (a store
      # symlink) — a hand-written hook is left alone.
      mkdir -p "$repo/hooks/post-receive.d"
      hook="$repo/hooks/post-receive"
      if [ -L "$hook" ]; then
        case "$(readlink "$hook")" in
          /nix/store/*) ln -sfn ${postReceiveDispatch} "$hook" ;;
        esac
      elif [ ! -e "$hook" ]; then
        ln -s ${postReceiveDispatch} "$hook"
      fi
    done

    # Per-project push-mirror hooks.
    ${mirrorHookCommands}

    # The one structural invariant, on every project the host serves.
    ${protectHookCommands}

    # The ref-updated publisher hook, on every repo when the bus is on.
    ${busHookCommands}

    # Group-shared repositories, on what the integrator serves.
    ${lib.optionalString cfg.integrator.enable integratorShareCommands}

    if [ "$conflicts" -ne 0 ]; then
      echo "valley-init: refusing to report this host converged: the push policy is not what every repository runs" >&2
      exit 1
    fi
  '';

  # The configuration a convergence is of: the init script itself, which
  # names every hook and every policy file it wires. valley-init records it
  # when it converges, and the git user's shell lets a push through only
  # while the record names the configuration the shell was rendered with.
  convergenceId = builtins.substring 0 32 (builtins.hashString "sha256" valleyInitBody);

  # The record, in the git user's home: valley-init writes it, and nothing
  # a session reaches can. A dot file never collides with a repository,
  # because project names begin with an alphanumeric.
  convergedRecord = "${cfg.dataDir}/.valley-converged";

  # The operator's hold on pushes: while this file exists, the git user's
  # shell refuses every push, whatever valley-init has recorded. valley-init
  # never touches it, so a hold placed before a deploy outlasts the
  # convergence and lasts until the operator removes it — the window a
  # deploy's audits need.
  pushHold = "${cfg.dataDir}/.valley-hold";

  # The git user's login shell (valleyhook shell). sshd runs it for every
  # session, and it names the key files sshd authorizes the git user's keys
  # from, in sshd's order, so it can find the authenticated key's tag.
  gitShell = pkgs.writeShellScriptBin "valley-git-shell" ''
    exec ${valleyhookPackage}/bin/valleyhook shell \
      --converged ${convergedRecord} \
      --expect ${convergenceId} \
      --hold ${pushHold} \
      --authorized-keys /etc/ssh/authorized_keys.d/${cfg.user} ${
        lib.optionalString cfg.identity.enable "--authorized-keys ${identityAuthorizedKeys}"
      } \
      -- "$@"
  '';

  # The program every project's pre-receive hook runs: the whole of the ref
  # policy (valleyhook/), built by this flake.
  valleyhookPackage = (import ./packages.nix { inherit pkgs lib; }).valleyhook;

  # The grants declared by hand, in the format the compiler writes, so the
  # hook reads one format whichever wrote it.
  declaredGrants = pkgs.writeText "valley-grants" (
    ''
      # Declared in services.valley.grants — do not edit.
    ''
    + lib.concatMapStrings (principal: "request ${principal}\n") cfg.grants.request
  );

  # Every file the hook reads grants from. The compiled file is added to the
  # declared one rather than replacing it, the same way the compiled keys
  # are added to the declared keys: a compilation can only ever grant more.
  grantFiles = [ declaredGrants ] ++ lib.optional cfg.identity.enable identityGrants;

  # The keys a pushed attestation's signature is checked against: the ones
  # a controller accepts evidence from. An attestation no controller here
  # would accept cannot take a name in the create-only namespace, and a host
  # that names no such keys accepts no attestation at all.
  hookVerifiers = lib.optional (knownSigners != "") knownSigners;

  # What a project declares about pushes, as valleyhook reads it: the
  # protected refs and their writers, empty for a project that declares no
  # protection, and the project's named grants.
  pushPolicy =
    name: p:
    pkgs.writeText "valley-push-policy-${name}.json" (
      builtins.toJSON {
        refs = p.protection.refs or [ ];
        writers = p.protection.writers or [ ];
        grants = p.grants or { };
      }
    );

  # The pre-receive hook: the one structural git invariant
  # (archive/design/architecture.md, archive/design/contribute.md), which is what a push may
  # write. It is installed on every project the host serves. Replacement
  # refs, notes and the valley's own namespace take no push; a symbolic
  # ref takes none; topic branches are open; an attestation ref may only be
  # created, and only holding what its name says; an integration request
  # takes the request grant; every other ref, tags included, takes a named
  # grant of the project; and a protected ref also takes a declared writer.
  # All policy beyond this lives in the integrator.
  #
  # The rules are valleyhook's, in Go, and this script is only how git
  # reaches it. It hands over what the policy is a function of: the pushing
  # principal, the project's declared push policy, the grants, the keys an
  # attestation is checked against, and the hook a project composes after
  # it, if it declares one.
  #
  # A declaration usually names no writer at all, and then the protected
  # refs take no push from anyone. That is the norm, not a
  # misconfiguration: the integrator writes the ref locally, and a local
  # write is not a push.
  #
  # Pushes arrive over SSH as one shared git user, so the unix account
  # behind a push says nothing about who pushed. The principal comes from
  # the key instead: services.valley.authorizedKeys tags each key's
  # authorized_keys entry, the git user's shell reads the tag off the entry
  # of the key sshd authenticated and puts it in the environment of the
  # receive-pack this hook runs under, and an untagged key has no principal
  # at all. The client cannot supply the tag itself: the shell drops any it
  # arrives with, and the assertions below hold sshd to offering no client
  # environment that could carry it.
  #
  # This governs pushes, which is every write that crosses the host
  # boundary. It does not govern writes made on the host: the integrator
  # updates refs locally, as could anyone holding the git user's shell.
  # Local access is the host's own boundary, not this hook's.
  protectHook =
    name: p:
    pkgs.writeShellScript "valley-protect-${name}" ''
      # Managed by services.valley — do not edit.
      exec ${valleyhookPackage}/bin/valleyhook pre-receive \
        --project ${lib.escapeShellArg name} \
        --policy ${pushPolicy name p} \
        ${lib.concatMapStringsSep " " (f: "--grants ${f}") grantFiles} \
        ${lib.concatMapStringsSep " " (f: "--known-signers ${f}") hookVerifiers} \
        ${lib.optionalString (cfg.extraPreReceive ? ${name}) "--then ${cfg.extraPreReceive.${name}}"} \
        --principal="''${${principalEnv}:-}"
    '';

  # Per-project pre-receive wiring, on every project the host serves. A
  # hook that is not this module's is a hook that would run instead of the
  # push policy, so valley-init refuses to leave one in place: it says so,
  # and the activation fails once everything else is wired. The one
  # exception is a hook identical to the one the project composes after the
  # policy (services.valley.extraPreReceive), which is checked byte for byte
  # and then replaced, since the managed hook runs it. A store symlink is
  # this module's, whichever store path it names, and is re-pointed.
  #
  # core.hooksPath moves where git looks for hooks at all, from any scope
  # the git user reads — the repository's config, the user's, the system's,
  # or a file one of them includes. A repository whose hooks path is set at
  # all, to an empty value included, is refused the same way, with no
  # exception.
  protectHookCommands = lib.concatStrings (
    lib.mapAttrsToList (name: p: ''
      served=${lib.escapeShellArg "${cfg.dataDir}/${name}.git"}
      phook="$served/hooks/pre-receive"
      managed=${protectHook name p}
      composed=${lib.escapeShellArg (toString (cfg.extraPreReceive.${name} or ""))}
      if [ -L "$phook" ] && [[ "$(readlink "$phook")" == /nix/store/* ]]; then
        ln -sfn "$managed" "$phook"
      elif [ ! -e "$phook" ] && [ ! -L "$phook" ]; then
        ln -s "$managed" "$phook"
      elif [ -n "$composed" ] && [ -f "$phook" ] && cmp -s "$phook" "$composed"; then
        echo "valley-init: ${name}: the pre-receive hook in $served is the one services.valley.extraPreReceive.${name} composes after the push policy, so the managed hook replaces it and runs it" >&2
        ln -sfn "$managed" "$phook"
      else
        echo "valley-init: ${name}: $phook is a pre-receive hook this module did not write, and git would run it instead of the push policy. Declare it as services.valley.extraPreReceive.${name} to run it after the policy, or remove it." >&2
        conflicts=1
      fi
      # Exit status 1 is the one answer that means unset. A value, an
      # empty one included — git then looks for hooks relative to where it
      # runs — and a configuration git cannot read are all refused.
      hooks_path="$(git -C "$served" config --get core.hooksPath)" && status=0 || status=$?
      if [ "$status" -ne 1 ]; then
        echo "valley-init: ${name}: core.hooksPath is set for $served (to '$hooks_path'), so git would look for hooks there and the push policy would not run. Remove the setting, wherever it is." >&2
        conflicts=1
      fi
    '') gitProjects
  );

  # sshd's environment patterns, for the assertions that hold it to
  # accepting no client environment the push boundary reads. `*` and `?` are
  # the only wildcards an AcceptEnv pattern has, and a pattern is matched
  # as written: variable names are case-sensitive.
  sshPatternMatches =
    pattern: name:
    builtins.match (lib.concatMapStrings (
      c:
      if c == "*" then
        ".*"
      else if c == "?" then
        "."
      else if builtins.match "[A-Za-z0-9_]" c != null then
        c
      else
        "\\${c}"
    ) (lib.stringToCharacters pattern)) name != null;

  # Whether some name beginning with prefix matches pattern: the pattern
  # walked over the prefix, with the rest of the name free.
  sshPatternMatchesSomeName =
    pattern: prefix:
    if prefix == "" then
      true
    else if pattern == "" then
      false
    else
      let
        c = builtins.substring 0 1 pattern;
        rest = builtins.substring 1 (-1) pattern;
        next = builtins.substring 1 (-1) prefix;
      in
      if c == "*" then
        sshPatternMatchesSomeName rest prefix || sshPatternMatchesSomeName pattern next
      else if c == "?" || c == builtins.substring 0 1 prefix then
        sshPatternMatchesSomeName rest next
      else
        false;

  # A variable the push boundary reads, and no client may supply: the
  # principal, and anything git reads its configuration, hooks or objects
  # from.
  boundaryVariable = pattern: sshPatternMatches pattern principalEnv || sshPatternMatchesSomeName pattern "GIT_";

  # A refname glob as the declaration writes one, where `*` crosses path
  # separators (schema/valley.cue).
  refPatternMatches =
    pattern: ref:
    builtins.match (lib.concatMapStrings (
      c:
      if c == "*" then
        ".*"
      else if builtins.match "[A-Za-z0-9/_-]" c != null then
        c
      else
        "\\${c}"
    ) (lib.stringToCharacters pattern)) ref != null;

  # Every value sshd is given for one keyword, from the settings and from
  # every line of extraConfig, inside a Match block or not. The keyword is
  # matched in any case, as sshd matches it, with or without `=` after it;
  # the values are kept as written, because sshd's patterns and variable
  # names are case-sensitive. Values are split on any whitespace, tabs
  # included, as sshd splits them. Quoting and escapes are not unpicked
  # here: a value holding a quote or a backslash is refused below instead,
  # because a reading that differed from sshd's would be a hole.
  sshdValues =
    keyword:
    let
      settings = config.services.openssh.settings;
      word = x: if lib.isBool x then (if x then "yes" else "no") else toString x;
      words = v: lib.filter (w: lib.isString w && w != "") (builtins.split "[[:space:]]+" v);
      fromSettings = lib.concatMap (
        k:
        let
          v = settings.${k};
        in
        if lib.isList v then lib.concatMap (x: words (word x)) v else words (word v)
      ) (lib.filter (k: lib.toLower k == lib.toLower keyword && settings.${k} != null) (lib.attrNames settings));
      fromExtra = lib.concatMap (
        line:
        let
          m = builtins.match "[[:space:]]*([A-Za-z]+)[[:space:]]*=?[[:space:]]*(.*)" line;
        in
        if m == null || lib.toLower (lib.head m) != lib.toLower keyword then
          [ ]
        else
          words (lib.elemAt m 1)
      ) (lib.splitString "\n" config.services.openssh.extraConfig);
    in
    fromSettings ++ fromExtra;

  # Every key line the git user's declared authorized_keys holds, from this
  # module and from any other, with the principal its tag names ("" for
  # none) and the key itself.
  declaredKeyEntries = lib.concatMap (
    line:
    let
      key = builtins.match "(.*[[:space:]])?((ssh|ecdsa|sk)-[^[:space:]\"]+)[[:space:]]+([A-Za-z0-9+/=]+)([[:space:]].*)?" line;
      tag = builtins.match ".*environment=\"${principalEnv}=([^\"]*)\".*" line;
    in
    lib.optional (key != null) {
      key = lib.elemAt key 3;
      principal = if tag == null then "" else lib.head tag;
    }
  ) config.users.users.${cfg.user}.openssh.authorizedKeys.keys;

  # Keys declared more than once under different tags. sshd authorizes a
  # key by the first entry that names it, so which principal such a key
  # pushes as depends on file order; the assertion below refuses it.
  conflictingKeys = lib.filterAttrs (_: entries: lib.length (lib.unique (map (e: e.principal) entries)) > 1) (
    lib.groupBy (e: e.key) declaredKeyEntries
  );

  # The declared key lines, for the compiler: a compiled key that is also
  # declared under another tag is refused there, for the same reason.
  declaredKeysFile = pkgs.writeText "valley-declared-keys" (
    lib.concatLines config.users.users.${cfg.user}.openssh.authorizedKeys.keys
  );
in
{
  options.services.valley = {
    enable = lib.mkEnableOption "the valley host: declarative bare-git hosting driven by a CUE declaration";

    user = lib.mkOption {
      type = lib.types.str;
      default = "git";
      description = "System user that owns the repositories and accepts SSH pushes.";
    };

    group = lib.mkOption {
      type = lib.types.str;
      default = "git";
      description = "Primary group of the valley git user.";
    };

    dataDir = lib.mkOption {
      type = lib.types.path;
      default = "/srv/git";
      example = "/mnt/git";
      description = ''
        Directory holding the bare repositories, one `<name>.git` per
        project declared in {option}`services.valley.config`. Also the git
        user's home, so clone URLs are relative to it (`git@host:name.git`).
        Consumers typically point it at a dedicated dataset.
      '';
    };

    authorizedKeys = lib.mkOption {
      type = lib.types.listOf (
        lib.types.either lib.types.str (
          lib.types.submodule {
            options = {
              key = lib.mkOption {
                type = lib.types.str;
                description = "The SSH public key, as it would appear in `authorized_keys`.";
              };

              principal = lib.mkOption {
                type = lib.types.strMatching "[a-zA-Z0-9][a-zA-Z0-9._-]*";
                example = "integrator";
                description = ''
                  Name of the principal this key acts as. Every push made
                  with the key carries the name, and the pre-receive hook
                  decides what the name may write: from the project's
                  `protection` block and named grants in
                  {option}`services.valley.config`, and from the grants the
                  name holds ({option}`services.valley.grants`, and the
                  registry's when it is compiled). Nothing else uses it.
                  A key is declared once: the same key under two tags, or
                  tagged and untagged, is refused.
                '';
              };
            };
          }
        )
      );
      default = [ ];
      example = lib.literalExpression ''
        [
          "ssh-ed25519 AAAA… someone@somewhere"
          {
            principal = "integrator";
            key = "ssh-ed25519 AAAA… integrator";
          }
        ]
      '';
      description = ''
        SSH public keys allowed to push/fetch as the git user. Access is
        host-level by design: every key can reach every project.

        A key written as a plain string is anonymous — it can push, and it
        can write nothing protected. A key written as an attribute set
        names the principal it acts as, which is the only thing that can
        tell one pusher from another: pushes arrive as the shared git user,
        so identity has to ride on the key.

        Which refs a principal may write is declared, not an option here:
        it is a statement about what the host serves, and lives in the
        project's `protection` block in
        {option}`services.valley.config`. Binding a name to keys is the
        machine half — the hand-written form of what the identity registry
        compiles (dcr-b87f6e8).

        With {option}`services.valley.identity.enable` on, the compiled
        keys are added to what sshd reads and the keys here stay
        authorized beside them. The list is therefore the way back in when
        a compilation is wrong or has not happened yet, and it is never
        empty.
      '';
    };

    extraPreReceive = lib.mkOption {
      type = lib.types.attrsOf lib.types.pathInStore;
      default = { };
      example = lib.literalExpression ''
        {
          cosmo = pkgs.writeShellScript "cosmo-pre-receive" ;
        }
      '';
      description = ''
        A further pre-receive hook per project, run after the push policy
        accepts a push, with the same input. It can refuse what the policy
        accepts and never accept what the policy refuses.

        This is the one way to compose a hook with the managed one. A
        pre-receive hook the module did not write fails valley-init,
        because git would run it instead of the policy. A hook that is
        byte-for-byte the one declared here is the exception: valley-init
        replaces it with the managed hook, which runs it. The value is a
        store path, so what runs is exactly what was declared.
      '';
    };

    # Grants held by hand. The registry names who holds a grant
    # (dcr-b87f6e8, dcr-e544f20), and these options are the hand-written
    # form of what it compiles, beside it the way the declared keys sit
    # beside the compiled ones.
    grants = {
      request = lib.mkOption {
        type = lib.types.listOf (lib.types.strMatching "[a-zA-Z0-9][a-zA-Z0-9._-]*");
        default = [ ];
        example = [ "operator" ];
        description = ''
          Principals holding the request grant: the ones whose pushes may
          file, replace and withdraw an integration request
          (`refs/the-valley/integration-requests/*`) on a protected
          project. The pre-receive hook refuses every other write to that
          namespace, so a principal named neither here nor in the
          registry cannot ask for anything to land.

          The grant rides on push. Only a key sshd admits reaches the hook,
          so a principal named here also needs a key, declared in
          {option}`services.valley.authorizedKeys` or compiled from the
          registry.

          With {option}`services.valley.identity.enable` on, the holders
          the registry names are added to these, and a compiled file can
          only ever add. The list is therefore how a host grants request
          before its registry does — including to the operator who has to
          file the registry change that grants it.
        '';
      };
    };

    config = lib.mkOption {
      type = lib.types.path;
      example = lib.literalExpression "./valley.cue";
      description = ''
        Path to the host's CUE declaration (`package valley`) — the
        canonical statement of what this valley host serves, validated
        against the shipped schema at build time. This is the single domain
        input: projects, their push mirrors, and the backup policy are
        declared here, never as Nix options.
      '';
    };

    # The event bus. Machine options, not domain: whether this machine runs
    # a bus, where it listens, and where its storage lives. What gets
    # published onto it is defined portably in ../schema/events.cue.
    bus = {
      enable = lib.mkEnableOption "the valley event bus: NATS JetStream, onto which every push's ref updates are projected as events";

      listen = lib.mkOption {
        type = lib.types.str;
        default = "127.0.0.1:4222";
        example = "100.100.1.7:4222";
        description = ''
          Address the bus listens on, `host:port`. Localhost by default:
          nothing off the host can reach the bus until the consumer widens
          this deliberately — e.g. to the host's tailnet address once a
          remote `valley tail` wants in. Local publishers (the ref-updated
          hook) connect to this same address, so it must be reachable from
          the host itself.
        '';
      };

      storeDir = lib.mkOption {
        type = lib.types.path;
        default = "${cfg.dataDir}/.bus";
        defaultText = lib.literalExpression ''"''${config.services.valley.dataDir}/.bus"'';
        description = ''
          Directory holding the JetStream file storage. The default lives
          under {option}`services.valley.dataDir`; it can never collide
          with a repository because project names must start with an
          alphanumeric. The stream is a projection of git, rebuildable
          with `valley replay` — losing this directory costs one replay.
        '';
      };
    };

    # The integrator (archive/design/roadmap.md, Phase 3): the controller that
    # lands a change once its evidence still transfers to the current tip.
    # Machine options only — whether this host runs controllers, who they
    # are, and how often they look. Which projects get one is not an option
    # either: it follows from which projects the declaration protects,
    # because a protected ref is exactly what a controller exists to write.
    integrator = {
      enable = lib.mkEnableOption "the valley integrator: one controller per protected project, landing changes whose evidence transfers";

      user = lib.mkOption {
        type = lib.types.str;
        default = "valley-integrator";
        description = ''
          System user the controllers run as. Deliberately not
          {option}`services.valley.user`: one unix identity per service is
          the split bd-500adf7 asks for, and this service begins it.

          The account exists only to write the repositories it serves. It
          gets no shell and no SSH key, and its primary group is
          {option}`services.valley.group`, which is the whole of its
          permission model: {option}`services.valley.dataDir` is already
          group-readable, and each served repository is made group-shared
          (group write, setgid directories) so a controller can write
          refs, objects, and git's worktree metadata. Nothing else on the
          host belongs to that group, and the bus's store directory (mode
          0700) stays out of reach.

          The key and signers files are the consumer's to provision; both
          must be readable by this user.
        '';
      };

      signingKeyFile = lib.mkOption {
        type = lib.types.nullOr lib.types.path;
        default = null;
        example = "/run/agenix/valley-integrator-key";
        description = ''
          ed25519 private key the controllers sign their transfer
          statements with. A runtime path the consumer provisions (e.g.
          agenix), never a store path — the store is world-readable.
        '';
      };

      knownSignersFile = lib.mkOption {
        type = lib.types.nullOr lib.types.path;
        default = null;
        example = "/var/lib/valley-instance/known_signers";
        description = ''
          File of verifier keys whose attestations count, one per line, in
          `attest key` format. This is the identity registry's interim
          compilation (dcr-b87f6e8), supplied directly for a host that does
          not compile one: turning on
          {option}`services.valley.identity.enable` renders the same file
          from the registry, and then this option must be left unset.
          Evidence signed by nobody in it does not transfer, which a
          controller reports per check rather than treating as a forgery.
        '';
      };

      signingName = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        example = "integrator";
        description = ''
          The name a controller's transfer statements are signed under.
          Unset leaves the binary's own default, which is this host's
          attestations name — the machine signing as itself.

          Set it to the name the identity registry publishes the
          integrator's key under. The note format's key hash binds the name
          to the key (dcr-de9d996), so a controller signing under one name
          while the registry publishes it under another produces notes no
          verifier can find a key for.
        '';
      };

      instanceProject = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        example = "qinling";
        description = ''
          The project whose served repository carries the group's floor —
          the instance policy layer every controller on this host composes
          against. A controller reads it from `policy/instance/` in that
          bare repository's integrated tip, and nothing is materialized on
          disk in between. It is also where the identity registry is read
          from, for the same reason and at the same altitude: both are the
          instance's, so a host serving that repository already holds the
          authoritative copy.

          The floor is the instance repository's, read at the tip that has
          been integrated (dcr-f41f718); the instance repository is the
          group's own (dcr-b87f6e8). A host serving it is therefore already
          holding the authoritative copy.

          What follows is a property of that design, and worth knowing
          before editing a floor. A change to it takes effect at the first
          pass after it lands on that ref, and never before. An edit in a
          branch, in a worktree, or in an integration request that has not
          been integrated governs nothing. There is no refresh to trigger
          and no staleness to wait out: landing is the whole of publishing
          a floor.

          The project has to declare protection covering refs/heads/main,
          and the registry's ref when the identity compiler is on. Both
          are read from that tip, so a ref any push-capable key could move
          would hand every key the floor and the registry.

          Every controller on the host reads the same repository, the
          controller serving that repository included. Both layers are
          resolved from the controller's own side — this one, and the
          project's `policy/project/` at the target tip — so a change can
          never supply the policy that gates it (bd-eaefe82).

          Those two directories are the convention, and the module states
          neither: a controller takes each layer from the place the
          integrator defaults to, so a host declaration that says nothing
          about policy gets the layout the worked example lays out
          (dcr-f41f718).
        '';
      };

      instancePolicy = lib.mkOption {
        type = lib.types.nullOr lib.types.path;
        default = null;
        example = "/var/lib/valley-instance/policy/instance";
        description = ''
          Directory of `*.cue` files holding the instance policy layer, for
          a host that serves no instance repository. It holds what
          `policy/instance/` in the instance repository holds. Set this or
          {option}`services.valley.integrator.instanceProject`, never both.

          Keeping such a directory in step with the instance repository is
          the consumer's to arrange, which is the reason to prefer the
          project option wherever the host serves the repository.
        '';
      };

      interval = lib.mkOption {
        type = lib.types.str;
        default = "15s";
        example = "1m";
        description = ''
          How long a controller waits between passes. The integrator polls
          deliberately: integration requests are durable in git, and the
          controller level-triggers over those refs, so a lost event costs
          one interval and nothing more.
        '';
      };
    };

    # The identity registry compiler (dcr-b87f6e8): the step between the
    # instance's declared registry and the two gates that read what it
    # compiles to. Machine options only — whether this host compiles, and
    # from where. Who the principals are, what they hold and when their
    # entries end is the registry's, and the registry is a document in the
    # instance repository, never an option here.
    identity = {
      enable = lib.mkEnableOption ''
        compiling the identity registry into what the gates check: the
        verifier keys a controller accepts evidence from, and the git
        user's authorized keys, both rendered from the instance
        repository's integrated tip'';

      ref = lib.mkOption {
        type = lib.types.str;
        default = "refs/heads/main";
        description = ''
          The ref of the instance repository whose tip carries the
          registry. A registry edit takes effect at the first compilation
          after it lands on this ref, and never before: an edit in a
          branch, in a worktree, or in an integration request that has not
          been integrated governs nothing.
        '';
      };

      directory = lib.mkOption {
        type = lib.types.str;
        default = "identity";
        description = ''
          The registry's directory in that tip. It is a CUE package: the
          directory is the enumeration, and every `*.cue` document
          directly in it unifies into one registry.
        '';
      };

      interval = lib.mkOption {
        type = lib.types.str;
        default = "5m";
        example = "1m";
        description = ''
          How long between compilations. The compiler is level-triggered
          and idempotent — it renders the current tip and replaces an
          artifact only when its bytes changed — so a timer is the whole
          of the trigger, and a landed registry change takes effect within
          one interval.
        '';
      };
    };

    # Machine integration for the declared backup policy. The declaration
    # states WHAT must hold (offsite backup, cadence, retention); these
    # options supply HOW on this machine — where the repository is and how
    # to authenticate. All are paths to files the consumer provisions
    # (e.g. agenix), required when the declaration enables backup. Point
    # them at runtime paths, never at files in the Nix store.
    backup = {
      repositoryFile = lib.mkOption {
        type = lib.types.nullOr lib.types.path;
        default = null;
        example = "/run/agenix/valley-restic-repo";
        description = ''
          File containing the restic repository URL, for the sftp target
          e.g. `sftp://u123456@u123456.your-storagebox.de:23//./backups/valley`.
        '';
      };

      passwordFile = lib.mkOption {
        type = lib.types.nullOr lib.types.path;
        default = null;
        example = "/run/agenix/valley-restic-password";
        description = ''
          File containing the restic repository encryption password.
          Losing it loses the backups — keep a copy somewhere that
          survives this host.
        '';
      };

      sshKeyFile = lib.mkOption {
        type = lib.types.nullOr lib.types.path;
        default = null;
        example = "/run/agenix/valley-git-ssh-key";
        description = ''
          SSH private key that authenticates to the sftp target. The
          backup service runs as root, so any identity the target
          authorizes works — reusing the mirror key is fine.
        '';
      };

      knownHostsFile = lib.mkOption {
        type = lib.types.nullOr lib.types.path;
        default = null;
        example = "/var/lib/valley-backup/known_hosts";
        description = ''
          known_hosts file pinning the sftp target's host key. Pin from
          the provider's published fingerprints, not a blind ssh-keyscan.
        '';
      };
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.authorizedKeys != [ ];
        message = "services.valley.authorizedKeys must not be empty — the git user would be unreachable.";
      }
    ]
    ++ lib.optionals backupEnabled (
      lib.mapAttrsToList (name: what: {
        assertion = cfg.backup.${name} != null;
        message = "services.valley.backup.${name} must be set: the host declaration enables backup, and ${what} is machine integration the consumer supplies (e.g. from its secrets).";
      }) backupSecretOptions
    )
    ++ lib.optionals cfg.integrator.enable (
      lib.mapAttrsToList (name: what: {
        assertion = cfg.integrator.${name} != null;
        message = "services.valley.integrator.${name} must be set: the integrator is enabled, and ${what} is machine integration the consumer supplies.";
      }) integratorPathOptions
      ++ [
        {
          assertion = (cfg.integrator.instanceProject == null) != (cfg.integrator.instancePolicy == null);
          message = "exactly one of services.valley.integrator.instanceProject and services.valley.integrator.instancePolicy must be set: the floor is read from the instance repository this host serves, or supplied as a directory for a host that serves none, and two sources for one layer is a question about which one won.";
        }
        {
          assertion =
            cfg.integrator.instanceProject == null || gitProjects ? ${cfg.integrator.instanceProject};
          message = "services.valley.integrator.instanceProject names ${toString cfg.integrator.instanceProject}, which this host declaration does not serve: the floor is read from that repository's tip, so the host has to hold it.";
        }
        {
          assertion = cfg.integrator.user != cfg.user;
          message = "services.valley.integrator.user must differ from services.valley.user: the integrator runs under its own unix identity (bd-500adf7).";
        }
        {
          assertion = integratedProjects != { };
          message = "services.valley.integrator.enable is on and the host declaration protects no project — a controller only ever serves a protected target, so this would render no service at all.";
        }
      ]
    )
    ++ lib.optionals (cfg.integrator.instanceProject != null && gitProjects ? ${cfg.integrator.instanceProject}) (
      let
        name = cfg.integrator.instanceProject;
        protected = gitProjects.${name}.protection.refs or [ ];
        read =
          lib.optional cfg.integrator.enable "refs/heads/main" ++ lib.optional cfg.identity.enable cfg.identity.ref;
      in
      map (ref: {
        assertion = lib.any (pattern: refPatternMatches pattern ref) protected;
        message = "services.valley.integrator.instanceProject names ${name}, whose ${ref} is not protected: the floor and the registry are read from it, so any key that can push could rewrite them. Declare a protection block for ${name} covering ${ref}.";
      }) read
    )
    ++ [
      {
        assertion = !(lib.any (v: builtins.match ".*[\"'\\\\].*" v != null) (
          sshdValues "AcceptEnv" ++ sshdValues "SetEnv"
        ));
        message = "sshd's AcceptEnv or SetEnv here holds a quote or a backslash, which sshd unpicks and this module does not. Write each name plainly, separated by spaces, so what is checked is what sshd reads.";
      }
      {
        assertion = !(lib.any boundaryVariable (sshdValues "AcceptEnv"));
        message = "sshd accepts ${lib.concatStringsSep " " (lib.filter boundaryVariable (sshdValues "AcceptEnv"))} from the client (AcceptEnv), a pattern that admits ${principalEnv} or a GIT_ variable. The git user's shell drops them, and sshd must not offer them either. Remove the pattern.";
      }
      {
        assertion = !(lib.any (v: boundaryVariable (lib.head (lib.splitString "=" v))) (sshdValues "SetEnv"));
        message = "sshd sets ${principalEnv} or a GIT_ variable itself (SetEnv). Remove it.";
      }
      {
        assertion = lib.all (v: lib.toLower v == "no") (sshdValues "PermitUserEnvironment");
        message = "sshd's PermitUserEnvironment must be no: the git user's shell derives the principal from the authenticated key, and nothing a key's entry or ~/.ssh/environment sets is the push boundary's to read.";
      }
      {
        assertion = conflictingKeys == { };
        message = "the git user's authorized keys name ${lib.concatStringsSep ", " (lib.attrNames conflictingKeys)} under more than one principal tag (an untagged entry counts as one): sshd authorizes a key by the first entry naming it, so which principal it pushes as would depend on file order. Declare each key once.";
      }
      {
        assertion = config.users.users.${cfg.user}.openssh.authorizedKeys.keyFiles == [ ];
        message = "users.users.${cfg.user}.openssh.authorizedKeys.keyFiles must be empty: the git user's keys carry the principal tag the push policy reads, so they are declared in services.valley.authorizedKeys, where their tags can be checked.";
      }
    ]
    ++ lib.optionals cfg.identity.enable [
      {
        assertion = cfg.integrator.instanceProject != null;
        message = "services.valley.integrator.instanceProject must be set: the identity registry is read from the served instance repository's integrated tip, so the host has to hold that repository.";
      }
      {
        assertion = cfg.integrator.knownSignersFile == null;
        message = "services.valley.integrator.knownSignersFile must be unset: the registry compiles that file (${identityKnownSigners}), and two sources for one file is a question about which one won.";
      }
    ];

    users.groups.${cfg.group} = { };

    users.users.${cfg.user} = {
      isSystemUser = true;
      group = cfg.group;
      home = cfg.dataDir;
      # The valley's shell, in front of git-shell: it derives the principal
      # from the authenticated key, drops the GIT_ environment, and pauses
      # pushes until valley-init has converged. git-shell then allows only
      # git-upload-pack, git-receive-pack and git-upload-archive, and rejects
      # interactive logins (no ~/git-shell-commands).
      shell = "${gitShell}/bin/valley-git-shell";
      openssh.authorizedKeys.keys = map authorizedKeyLine cfg.authorizedKeys;
    };

    # The integrator's own account. No shell, no keys, no home of its own
    # beyond the state directory its units get; the git group is the only
    # thing it holds.
    users.users.${cfg.integrator.user} = lib.mkIf cfg.integrator.enable {
      isSystemUser = true;
      group = cfg.group;
      home = "/var/lib/valley-integrator";
      description = "The valley integrator";
    };

    services.openssh.enable = lib.mkDefault true;

    # The git user's key files, named in its Match block so no other file
    # can authorize a key for it. The declared keys come first, and they stay
    # authorized beside the compiled ones: a compiled file can only ever
    # add, so an empty or missing one — a first boot before the first
    # compilation, a registry that lost an entry, a bug here — cannot take
    # the git user's access away. Which is the point: the assertion above
    # keeps at least one key declared, and that key is the way back in.
    #
    # Nothing else authorizes a key for the git user. ~/.ssh/authorized_keys
    # under its home is not read, and no AuthorizedKeysCommand runs, so every
    # entry a pusher can arrive through carries the tag this module or the
    # compiler wrote, and the assertions and the compiler hold those to one
    # tag per key.
    #
    # Belt-and-braces hardening for the git user beside it. The trailing
    # `Match All` closes the block so it can't scope directives appended to
    # sshd_config after this snippet.
    services.openssh.extraConfig = ''
      Match User ${cfg.user}
        AuthorizedKeysFile /etc/ssh/authorized_keys.d/%u${lib.optionalString cfg.identity.enable " ${identityAuthorizedKeys}"}
        AuthorizedKeysCommand none
        ExposeAuthInfo yes
        AllowTcpForwarding no
        AllowAgentForwarding no
        X11Forwarding no
        PermitTunnel no
      Match All
    '';

    # git-shell spawns git-receive-pack/git-upload-pack from PATH
    environment.systemPackages = [ pkgs.git ];

    systemd.tmpfiles.rules = [
      "d ${cfg.dataDir} 0750 ${cfg.user} ${cfg.group} - -"
    ]
    ++ lib.optionals cfg.bus.enable [
      "d ${cfg.bus.storeDir} 0700 ${cfg.user} ${cfg.group} - -"
    ];

    # The bus itself. It runs as the git user — the store directory lives in
    # that user's data — but the sandbox is what confines it: the unit can
    # write nowhere except its store directory.
    systemd.services.valley-bus = lib.mkIf cfg.bus.enable {
      description = "The valley event bus (NATS JetStream)";
      wantedBy = [ "multi-user.target" ];
      after = [
        "network.target"
        "systemd-tmpfiles-setup.service"
      ];
      unitConfig.RequiresMountsFor = cfg.bus.storeDir;
      serviceConfig = {
        ExecStart = "${pkgs.nats-server}/bin/nats-server -c ${busServerConfig}";
        Restart = "on-failure";
        User = cfg.user;
        Group = cfg.group;
        LimitNOFILE = 65536;
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ReadWritePaths = [ cfg.bus.storeDir ];
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectClock = true;
        ProtectHostname = true;
        ProtectKernelLogs = true;
        ProtectKernelModules = true;
        ProtectKernelTunables = true;
        ProtectControlGroups = true;
        ProtectProc = "invisible";
        RestrictAddressFamilies = [
          "AF_INET"
          "AF_INET6"
          "AF_UNIX"
        ];
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        CapabilityBoundingSet = "";
        SystemCallArchitectures = "native";
        SystemCallFilter = [ "@system-service" ];
        UMask = "0077";
      };
    };

    # The stream is config, not state: (re)created idempotently on every
    # boot, capturing everything under valley.>. An existing stream — and
    # the events in it — is left untouched.
    systemd.services.valley-bus-init = lib.mkIf cfg.bus.enable {
      description = "Create the valley event stream";
      wantedBy = [ "multi-user.target" ];
      requires = [ "valley-bus.service" ];
      after = [ "valley-bus.service" ];
      serviceConfig = {
        Type = "oneshot";
        User = cfg.user;
        Group = cfg.group;
      };
      path = [ pkgs.natscli ];
      script = ''
        # The server needs a moment to accept connections after startup.
        for _ in $(seq 1 50); do
          nats --server ${busUrl} stream ls >/dev/null 2>&1 && break
          sleep 0.2
        done
        if ! nats --server ${busUrl} stream info valley >/dev/null 2>&1; then
          nats --server ${busUrl} stream add valley \
            --subjects 'valley.>' --storage file --defaults
        fi
      '';
    };

    # Create missing bare repos and (re)wire the managed hooks on every
    # activation where the declaration changed.
    systemd.services.valley-init = {
      description = "Initialize the valley host's bare git repositories";
      wantedBy = [ "multi-user.target" ];
      after = [ "systemd-tmpfiles-setup.service" ];
      unitConfig.RequiresMountsFor = cfg.dataDir;
      serviceConfig = {
        Type = "oneshot";
        User = cfg.user;
        Group = cfg.group;
      };
      path = [
        pkgs.git
        pkgs.diffutils
      ];
      # Everything init can wire is wired before it fails. A conflict in one
      # repository is reported, the rest of the host is still converged,
      # and only then does the unit fail, so the activation fails loudly
      # without leaving another repository's hook out of date.
      script = ''
        # Pushes pause the moment a convergence starts, and resume only once
        # it has finished: the record is removed first and written last.
        rm -f ${convergedRecord}
        ${valleyInitBody}
        printf '%s\n' ${convergenceId} > ${convergedRecord}.tmp
        mv -f ${convergedRecord}.tmp ${convergedRecord}
      '';
    };

    # The registry compiler, on a timer. It renders the instance
    # repository's integrated tip into the two artifacts the gates read;
    # nothing else on the host writes them.
    #
    # It runs as the git user rather than under an identity of its own,
    # which is the one place this module steps back from the service split
    # bd-500adf7 asks for. sshd refuses an authorized-keys file owned by
    # anyone but root or the account it authorizes, so the choice is the git
    # user or root, and the git user is the smaller grant.
    #
    # Level-triggered, like the rest: a compilation reads the current tip
    # and replaces an artifact only when its bytes changed, so re-running is
    # free and the timer needs no coordination with anything.
    systemd.services.valley-identity = lib.mkIf cfg.identity.enable {
      description = "Compile the valley identity registry into what the gates check";
      wantedBy = [ "multi-user.target" ];
      after = [ "valley-init.service" ];
      requires = [ "valley-init.service" ];
      unitConfig.RequiresMountsFor = cfg.dataDir;
      serviceConfig = {
        Type = "oneshot";
        ExecStart = identityCommand;
        User = cfg.user;
        Group = cfg.group;
        StateDirectory = identityStateName;
        # sshd walks every directory above an authorized-keys file and
        # refuses a group- or world-writable one, so the state directory is
        # 0755 deliberately rather than the 0700 a service's state usually
        # takes. Nothing secret lives in it: a registry is public keys.
        StateDirectoryMode = "0755";
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ReadWritePaths = [ identityStateDir ];
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectClock = true;
        ProtectHostname = true;
        ProtectKernelLogs = true;
        ProtectKernelModules = true;
        ProtectKernelTunables = true;
        ProtectControlGroups = true;
        ProtectProc = "invisible";
        RestrictAddressFamilies = [ "AF_UNIX" ];
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        CapabilityBoundingSet = "";
        SystemCallArchitectures = "native";
        SystemCallFilter = [ "@system-service" ];
        UMask = "0022";
      };
    };

    systemd.timers.valley-identity = lib.mkIf cfg.identity.enable {
      description = "Re-compile the valley identity registry";
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnBootSec = "1m";
        OnUnitActiveSec = cfg.identity.interval;
      };
    };

    # One controller per served project, as instances of a single template:
    # the units differ only in the repository they serve, which is the
    # instance name. `watch` rather than a timer firing `reconcile`: the
    # binary owns its poll loop and its interval, and a timer would be a
    # second cadence over the same level-triggered refs.
    systemd.services."valley-integrator@" = lib.mkIf cfg.integrator.enable {
      description = "The valley integrator for %i";
      after = [
        "network.target"
        "valley-init.service"
      ]
      ++ lib.optional cfg.bus.enable "valley-bus-init.service"
      ++ lib.optional cfg.identity.enable "valley-identity.service";
      requires = [ "valley-init.service" ];
      # Wants, not requires: a controller reads the compiled signers file,
      # so it starts after the first compilation — but a compilation that
      # failed must not keep the controllers down. One that finds no file
      # says so and restarts, which is the same loop and a louder one.
      wants = lib.optional cfg.identity.enable "valley-identity.service";
      unitConfig.RequiresMountsFor = cfg.dataDir;
      # nix is not pinned, for the reason the integrator package does not
      # pin it: a check's input closure must be recomputed by the nix this
      # machine actually runs. Everything else a controller drives is
      # pinned inside the wrapper.
      path = [ config.nix.package ];
      # The repositories belong to the git user and the controller does
      # not, so git refuses to touch them as dubiously owned. The exception
      # rides in the unit's environment, for two reasons. GIT_CONFIG_* is
      # command-scope config, which is protected config, so safe.directory
      # is honoured from it — a repository-scoped setting would be ignored,
      # deliberately. And every git the controller drives is a child of
      # this unit, whether run by the integrator itself or by attest or the
      # deriver, so one environment covers all of them.
      #
      # One repository per instance: never a global gitconfig, never a
      # wildcard. Every other repository on the host stays refused. Linked
      # worktrees need no entry of their own — the controller creates them,
      # so it owns them, and git checks the worktree and its gitdir rather
      # than the repository they point at.
      #
      # TMPDIR is the same kind of wiring, for the same reason. A verdict
      # wants a checkout of the tip on disk, and the integrator asks for one
      # the ordinary way — a temporary directory, then a linked worktree in
      # it. The sandbox below leaves no writable temporary directory, so
      # every tick died on `could not create leading directories`. The
      # scratch belongs under the state directory the unit already has:
      # writable by construction, owned by this service, and gone when the
      # service's state is. Setting it in the environment rather than
      # teaching the binary a flag covers every process in the unit at once
      # — the integrator, attest, the deriver, and the git each of them runs
      # all read TMPDIR, and all of them want scratch in the same place.
      #
      # A second exception, for the instance repository. Every controller
      # reads the group's floor from that repository's tip, and it need not
      # be the project the controller serves — the controller for one
      # project reads the floor out of another project's repository, which
      # it likewise does not own. Without the exception git refuses it as
      # dubiously owned and every pass fails, exactly as it did for the
      # served repository. Two entries, both named; still never a wildcard.
      #
      # Reading needs no sandbox entry beside it. ProtectSystem=strict
      # leaves the filesystem readable and only takes write away, so the
      # ReadWritePaths list above stays the list of what may be written,
      # which is what makes it worth reading. What read does need is a
      # filesystem grant, and valley-init makes it: the instance repository
      # is group-readable whether or not it is one of the served projects.
      environment = {
        GIT_CONFIG_COUNT = if instanceRepo != null then "2" else "1";
        GIT_CONFIG_KEY_0 = "safe.directory";
        GIT_CONFIG_VALUE_0 = "${cfg.dataDir}/%i.git";
        TMPDIR = integratorStateDir;
      }
      // lib.optionalAttrs (instanceRepo != null) {
        GIT_CONFIG_KEY_1 = "safe.directory";
        GIT_CONFIG_VALUE_1 = instanceRepo;
      };
      serviceConfig = {
        ExecStart = integratorCommand;
        Restart = "on-failure";
        RestartSec = "10s";
        User = cfg.integrator.user;
        Group = cfg.group;
        StateDirectory = integratorStateName;
        StateDirectoryMode = "0700";
        WorkingDirectory = integratorStateDir;
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        # Everything the controller may write, named where a reader of the
        # unit meets it. Under ProtectSystem=strict the rest of the
        # filesystem is read-only, so this list is the whole of what the
        # service can touch, and it is worth being able to read it as such
        # rather than as the exceptions plus whatever else is implied.
        #
        # Its own directory: its state, and the scratch root TMPDIR names.
        # A declaration says it here rather than relying on the state
        # directory being read-write by virtue of being declared, which is
        # not something this unit found to be true.
        #
        # The one repository it serves, and the nix daemon's socket —
        # recomputing a closure means asking the daemon, and connecting to
        # a unix socket needs write access to it.
        ReadWritePaths = [
          integratorStateDir
          "${cfg.dataDir}/%i.git"
          "/nix/var/nix/daemon-socket"
        ];
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectClock = true;
        ProtectHostname = true;
        ProtectKernelLogs = true;
        ProtectKernelModules = true;
        ProtectKernelTunables = true;
        ProtectControlGroups = true;
        ProtectProc = "invisible";
        RestrictAddressFamilies = [
          "AF_INET"
          "AF_INET6"
          "AF_UNIX"
        ];
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        CapabilityBoundingSet = "";
        SystemCallArchitectures = "native";
        SystemCallFilter = [ "@system-service" ];
        # Group write, not the bus unit's 0077: the git user owns these
        # repositories and has to stay able to write what a controller
        # leaves in them.
        UMask = "0002";
      };
    };

    # What publishes a controller's landings: a path unit per served
    # repository, watching its publish queue, and the drain it starts.
    systemd.paths."valley-publish@" = lib.mkIf cfg.integrator.enable {
      description = "Watch the publish queue of %i";
      pathConfig.DirectoryNotEmpty = "${cfg.dataDir}/%i.git/${publishQueueName}";
    };

    # It runs as the git user, with ssh on its path, because the mirror
    # credentials are that user's. Each run drains the whole queue, so starts
    # are at most one per landing, and a burst of landings must not trip
    # systemd's start limit and leave the queue undrained. A run is bounded
    # instead: a mirror that hangs is cut off, and the next landing runs the
    # drain again.
    systemd.services."valley-publish@" = lib.mkIf cfg.integrator.enable {
      description = "Publish the integrator's landings on %i";
      after = [ "valley-init.service" ] ++ lib.optional cfg.bus.enable "valley-bus-init.service";
      unitConfig.RequiresMountsFor = cfg.dataDir;
      startLimitIntervalSec = 0;
      path = [ config.programs.ssh.package ];
      serviceConfig = {
        Type = "oneshot";
        ExecStart = "${publishDrains}/%i";
        User = cfg.user;
        Group = cfg.group;
        WorkingDirectory = "${cfg.dataDir}/%i.git";
        TimeoutStartSec = "15min";
      };
    };

    # wantedBy on a template unit enables nothing, so the instances the
    # declaration asks for are pulled in by name.
    systemd.targets.valley-integrators = lib.mkIf cfg.integrator.enable {
      description = "The valley integrator's controllers, and what publishes their landings";
      wantedBy = [ "multi-user.target" ];
      wants =
        map (name: "valley-integrator@${name}.service") (lib.attrNames integratedProjects)
        ++ map (name: "valley-publish@${name}.path") (lib.attrNames integratedProjects);
    };

    # Offsite backup, rendered only when the declaration asks for it. The
    # declaration states the policy — that backup exists, its cadence and
    # retention; the services.valley.backup.* options supply the machine
    # half. Declaration absent or disabled ⇒ no restic config at all.
    services.restic.backups = lib.mkIf backupEnabled {
      valley = {
        initialize = true;
        repositoryFile = cfg.backup.repositoryFile;
        passwordFile = cfg.backup.passwordFile;
        paths = [ cfg.dataDir ];
        # The schema admits only the restic-sftp target today; a second
        # target would grow a dispatch here. The service runs as root:
        # authenticate with the supplied identity and only the pinned
        # host key. No prompt can be answered inside a systemd unit, so
        # BatchMode + strict host key checking turn a would-be hang into
        # an immediate error.
        extraOptions = [
          "sftp.args='-i ${cfg.backup.sshKeyFile} -o UserKnownHostsFile=${cfg.backup.knownHostsFile} -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes'"
        ];
        timerConfig = backupTimer.${backupPolicy.cadence};
        pruneOpts = [
          "--keep-daily ${toString backupPolicy.retention.daily}"
          "--keep-weekly ${toString backupPolicy.retention.weekly}"
          "--keep-monthly ${toString backupPolicy.retention.monthly}"
        ];
      };
    };
  };
}
