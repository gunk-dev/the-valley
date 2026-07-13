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
# key-only, Tailscale ACLs in front. Per-project access is not honestly
# enforceable with this mechanism, so it is deliberately not an option
# (.the-valley/decisions/dcr-0f5d9b1-cue-config-host-module.md).
#
# Mirror credentials are the consumer's concern: the module assumes the git
# user's SSH identity and known_hosts are provisioned by the host (e.g.
# cosmo, via its secrets). Nothing here plumbs secrets: the backup options
# below take *paths* to consumer-provisioned secret files; the contents
# never pass through this module or the store.
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

  # Best-effort push replication. The pushes run detached (setsid) so a dead
  # mirror can only ever cost a log line — never block or fail the primary
  # push. Explicit refspecs with --prune force-update every branch and tag
  # and propagate their deletions — the same deletion semantics --mirror
  # gives for heads and tags. --mirror itself is rejected: it also tries to
  # delete remote-only namespaces, and on GitHub the read-only refs/pull/*
  # makes that fail every push, masking real replication failures.
  mirrorPusher =
    name: mirrors:
    pkgs.writeShellScript "valley-mirror-push-${name}" ''
      for url in ${lib.escapeShellArgs mirrors}; do
        if ${pkgs.git}/bin/git push --prune "$url" '+refs/heads/*:refs/heads/*' '+refs/tags/*:refs/tags/*' >/dev/null 2>&1; then
          ${pkgs.util-linux}/bin/logger -t valley-mirror "${name}: pushed to $url" || true
        else
          ${pkgs.util-linux}/bin/logger -t valley-mirror "${name}: push to $url FAILED" || true
        fi
      done
    '';

  mirrorHook =
    name: mirrors:
    pkgs.writeShellScript "valley-mirrors-${name}" ''
      # Managed by services.valley — best-effort push mirrors for ${name}.
      cat >/dev/null   # updated refs unused: the push replicates all heads and tags
      ${pkgs.util-linux}/bin/setsid -f ${mirrorPusher name mirrors} </dev/null >/dev/null 2>&1
      exit 0
    '';

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
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = ''
        SSH public keys allowed to push/fetch as the git user. Access is
        host-level by design: every key can reach every project.
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
    );

    users.groups.${cfg.group} = { };

    users.users.${cfg.user} = {
      isSystemUser = true;
      group = cfg.group;
      home = cfg.dataDir;
      # git-shell only allows git-upload-pack/git-receive-pack/git-upload-archive;
      # interactive logins are rejected (no ~/git-shell-commands).
      shell = "${pkgs.git}/bin/git-shell";
      openssh.authorizedKeys.keys = cfg.authorizedKeys;
    };

    services.openssh.enable = lib.mkDefault true;

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
    ];

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
      '';
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
