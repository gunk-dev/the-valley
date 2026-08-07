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
# (.the-valley/decisions/dcr-0f5d9b1-cue-config-host-module.md). Per-project
# *write protection* is, because the pre-receive hook below is a real
# enforcement boundary and each key carries a principal name it can read
# (.the-valley/decisions/dcr-b87f6e8-identity-is-a-governed-registry.md) —
# so it is declared, in the project's `protection` block, and this module
# only binds principal names to keys and renders the hook.
#
# Mirror credentials are the consumer's concern: the module assumes the git
# user's SSH identity and known_hosts are provisioned by the host (e.g.
# cosmo, via its secrets). Nothing here plumbs secrets: the backup options
# below take *paths* to consumer-provisioned secret files; the contents
# never pass through this module or the store.
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
      "--known-signers ${toString cfg.integrator.knownSignersFile}"
      "--instance ${toString cfg.integrator.instancePolicy}"
      "--interval ${cfg.integrator.interval}"
    ]
    ++ lib.optional cfg.bus.enable "--bus ${busUrl}"
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
      share chmod -R g+rwX "$repo"
      share find "$repo" -type d -exec chmod g+s {} +
    '') (lib.attrNames integratedProjects);

  # The paths the consumer must supply once the integrator is enabled,
  # with what each names — for the assertion message.
  integratorPathOptions = {
    signingKeyFile = "the key its transfer statements are signed with";
    knownSignersFile = "the signers whose evidence it accepts";
    instancePolicy = "the instance policy layer it composes against";
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

  # The event bus (design/roadmap.md, Phase 1): NATS JetStream, onto which
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

  # The environment variable carrying the pushing key's principal name.
  # sshd sets it from the key's own authorized_keys entry; the pre-receive
  # hook reads it. One name, honoured only because PermitUserEnvironment
  # below names it.
  principalEnv = "VALLEY_PRINCIPAL";

  # Keys that name a principal. An entry written as a bare string names
  # none, and renders exactly as it always did.
  taggedKeys = lib.filter (k: !lib.isString k) cfg.authorizedKeys;

  authorizedKeyLine =
    k: if lib.isString k then k else ''environment="${principalEnv}=${k.principal}" ${k.key}'';

  # Where a contributor's attestations live (design/contribute.md, step 5).
  attestationNamespace = "refs/the-valley/attestations/";

  # The pre-receive hook: the one structural git invariant
  # (design/architecture.md, design/contribute.md). Two clauses and nothing
  # else — only a declared writer may update a protected ref, and an
  # attestation ref may only be created. Every other ref is open to anyone
  # with push access, and all policy beyond this lives in the integrator.
  #
  # What is protected and who may write it comes from the declaration's
  # `protection` block, like every other domain fact here. This renders it.
  #
  # Pushes arrive over SSH as one shared git user, so the unix account
  # behind a push says nothing about who pushed. The principal comes from
  # the key instead: services.valley.authorizedKeys tags each key's
  # authorized_keys entry, sshd puts that tag in the environment of the
  # receive-pack this hook runs under, and an untagged key has no principal
  # at all. The client cannot supply the tag itself — sshd passes on no
  # client environment (no AcceptEnv).
  #
  # This governs pushes, which is every write that crosses the host
  # boundary. It does not govern writes made on the host: the integrator
  # updates refs locally, as could anyone holding the git user's shell.
  # Local access is the host's own boundary, not this hook's.
  protectHook =
    name: p:
    pkgs.writeShellScript "valley-protect-${name}" ''
      # Managed by services.valley — do not edit.
      set -eu

      principal="''${${principalEnv}:-}"
      [ -n "$principal" ] || principal="<untagged key>"
      writers=( ${lib.escapeShellArgs p.writers} )
      protected=( ${lib.escapeShellArgs p.refs} )

      is_writer() {
        local w
        for w in ''${writers[@]+"''${writers[@]}"}; do
          if [ "$w" = "$principal" ]; then return 0; fi
        done
        return 1
      }

      rejected=0
      while read -r old new ref; do
        [ -n "$ref" ] || continue

        # An attestation is a record of what was checked; a record that can
        # be rewritten or dropped is not one. So the namespace is
        # create-only, for everyone — an all-zero old id is a creation, at
        # any hash length.
        case "$ref" in
          ${attestationNamespace}*)
            case "$old" in
              *[!0]*)
                echo "valley: $principal may not update $ref — attestation refs are create-only" >&2
                rejected=1
                ;;
            esac
            continue
            ;;
        esac

        # Protected refs, matched as shell globs (so `*` crosses path
        # separators). Unprotected refs are not the hook's business.
        for pattern in ''${protected[@]+"''${protected[@]}"}; do
          case "$ref" in
            $pattern)
              if ! is_writer; then
                echo "valley: $principal may not write $ref — a protected ref of ${name} (writers: ${
                  lib.concatStringsSep ", " p.writers
                })" >&2
                rejected=1
              fi
              break
              ;;
          esac
        done
      done
      exit "$rejected"
    '';

  # Per-project pre-receive wiring, from the declaration's protection
  # blocks. Same rules as the other managed hooks: only ever installs,
  # updates, or removes a store symlink — a hand-written hook of the same
  # name is left alone. A project that declares no protection gets no hook,
  # so a declaration written before the field installs nothing new.
  protectHookCommands = lib.concatStrings (
    lib.mapAttrsToList (
      name: p:
      let
        phook = lib.escapeShellArg "${cfg.dataDir}/${name}.git/hooks/pre-receive";
      in
      if p ? protection then
        ''
          phook=${phook}
          if [ -L "$phook" ]; then
            case "$(readlink "$phook")" in
              /nix/store/*) ln -sfn ${protectHook name p.protection} "$phook" ;;
            esac
          elif [ ! -e "$phook" ]; then
            ln -s ${protectHook name p.protection} "$phook"
          fi
        ''
      else
        ''
          phook=${phook}
          if [ -L "$phook" ]; then
            case "$(readlink "$phook")" in
              /nix/store/*) rm -f "$phook" ;;
            esac
          fi
        ''
    ) gitProjects
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
                  with the key carries the name, and
                  {option}`services.valley.protect` decides what a name may
                  write. Nothing else uses it.
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
        machine half — the interim form of the identity registry's
        compilation (dcr-b87f6e8).
      '';
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

    # The integrator (design/roadmap.md, Phase 3): the controller that
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
          compilation (dcr-b87f6e8): no compiler exists yet, so the file
          is supplied directly. Evidence signed by nobody in it does not
          transfer, which a controller reports per check rather than
          treating as a forgery.
        '';
      };

      instancePolicy = lib.mkOption {
        type = lib.types.nullOr lib.types.path;
        default = null;
        example = "/var/lib/valley-instance/policy";
        description = ''
          Directory of `*.cue` files holding the instance policy layer,
          from a checkout of the instance repository on this host. Both
          layers are resolved from the controller's own side — this one,
          and the project's `policy/` at the target tip — so a change can
          never supply the policy that gates it (bd-eaefe82).
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
          assertion = cfg.integrator.user != cfg.user;
          message = "services.valley.integrator.user must differ from services.valley.user: the integrator runs under its own unix identity (bd-500adf7).";
        }
        {
          assertion = integratedProjects != { };
          message = "services.valley.integrator.enable is on and the host declaration protects no project — a controller only ever serves a protected target, so this would render no service at all.";
        }
      ]
    );

    users.groups.${cfg.group} = { };

    users.users.${cfg.user} = {
      isSystemUser = true;
      group = cfg.group;
      home = cfg.dataDir;
      # git-shell only allows git-upload-pack/git-receive-pack/git-upload-archive;
      # interactive logins are rejected (no ~/git-shell-commands).
      shell = "${pkgs.git}/bin/git-shell";
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

    # Honour the principal tag on a key's entry, and nothing else: the
    # pattern-list form admits that one variable name. Rendered only once a
    # key names a principal, so a host with none keeps the sshd config it
    # had.
    services.openssh.settings = lib.optionalAttrs (taggedKeys != [ ]) {
      PermitUserEnvironment = principalEnv;
    };

    # Belt-and-braces hardening for the git user. The trailing `Match All`
    # closes the block so it can't scope directives appended to sshd_config
    # after this snippet.
    services.openssh.extraConfig = ''
      Match User ${cfg.user}
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
      path = [ pkgs.git ];
      script = ''
        repos=( ${lib.escapeShellArgs repoNames} )
        for name in "''${repos[@]}"; do
          repo="${cfg.dataDir}/$name.git"
          # Check HEAD rather than the directory itself so a pre-existing
          # empty directory still gets initialized.
          if [ ! -e "$repo/HEAD" ]; then
            git init --bare --initial-branch=main "$repo"
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

        # The one structural invariant, on every project declared protected.
        ${protectHookCommands}

        # The ref-updated publisher hook, on every repo when the bus is on.
        ${busHookCommands}

        # Group-shared repositories, on what the integrator serves.
        ${lib.optionalString cfg.integrator.enable integratorShareCommands}
      '';
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
      ++ lib.optional cfg.bus.enable "valley-bus-init.service";
      requires = [ "valley-init.service" ];
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
      environment = {
        GIT_CONFIG_COUNT = "1";
        GIT_CONFIG_KEY_0 = "safe.directory";
        GIT_CONFIG_VALUE_0 = "${cfg.dataDir}/%i.git";
        TMPDIR = "%S/valley-integrator";
      };
      serviceConfig = {
        ExecStart = integratorCommand;
        Restart = "on-failure";
        RestartSec = "10s";
        User = cfg.integrator.user;
        Group = cfg.group;
        StateDirectory = "valley-integrator";
        StateDirectoryMode = "0700";
        WorkingDirectory = "/var/lib/valley-integrator";
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        # The one repository it serves, and the nix daemon's socket —
        # recomputing a closure means asking the daemon, and connecting to
        # a unix socket needs write access to it.
        ReadWritePaths = [
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

    # wantedBy on a template unit enables nothing, so the instances the
    # declaration asks for are pulled in by name.
    systemd.targets.valley-integrators = lib.mkIf cfg.integrator.enable {
      description = "The valley integrator's controllers";
      wantedBy = [ "multi-user.target" ];
      wants = map (name: "valley-integrator@${name}.service") (lib.attrNames integratedProjects);
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
