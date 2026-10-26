# The web publisher (webui/cmd/valley-web-publish): a oneshot on a timer
# that reads this host's public projects and pushes signed exports to a web
# receiver, which never connects back.
#
# A module of its own beside the host module (valley-host.nix), which it
# needs: it reads that module's options for the repositories the host
# serves, their group, and the instance repository. Which projects exist,
# and the signers that verify their landings, it reads at run time from
# what the instance repository publishes, through the valley CLI.
#
# The service runs as a dynamic user in the git group, so it can read the
# repositories and own nothing on disk but its state and cache. It cannot
# write the repositories, and the files that hold the host's mirror,
# backup and signing keys are made inaccessible to it.
{
  config,
  pkgs,
  lib,
  ...
}:

let
  cfg = config.services.valley.webPublisher;
  host = config.services.valley;

  # The repositories this host serves, read from its declaration the way
  # the host module reads them. The derivation is the host module's own,
  # with the same name, schema and declaration, so it is built once.
  schema = ../schema/valley.cue;
  configJSON =
    pkgs.runCommand "valley-config.json"
      {
        nativeBuildInputs = [ pkgs.cue ];
      }
      ''
        cue vet -c ${schema} ${host.config}
        cue export ${schema} ${host.config} > $out
      '';
  declared = lib.importJSON configJSON;
  repositories = map (name: "${toString host.dataDir}/${name}.git") (
    lib.attrNames (lib.filterAttrs (_: p: p.git.enable) declared.projects)
  );

  instanceRepo = "${toString host.dataDir}/${toString host.integrator.instanceProject}.git";

  # The repositories belong to the git user and the service does not, so
  # git refuses them as dubiously owned. The unit's environment carries
  # safe.directory entries, which every git the publisher and the valley
  # CLI run inherits. GIT_CONFIG_* is command scope, which git reads
  # safe.directory from, after the system and global configuration. The
  # entries add to what those set, so the first is empty, which resets the
  # list (git-config(1)), and one exact entry per repository follows. A
  # system configuration that trusts every directory trusts none of them
  # here. Never a wildcard.
  safeDirectories = {
    GIT_CONFIG_COUNT = toString (lib.length repositories + 1);
  }
  // lib.listToAttrs (
    lib.concatLists (
      lib.imap0 (i: repo: [
        (lib.nameValuePair "GIT_CONFIG_KEY_${toString i}" "safe.directory")
        (lib.nameValuePair "GIT_CONFIG_VALUE_${toString i}" repo)
      ]) ([ "" ] ++ repositories)
    )
  );

  # What the service must never read: the git user's ssh identity, which
  # pushes the mirrors, and the key files the host module is given. A path
  # that does not exist is skipped (the leading -).
  keys = [
    "${toString host.dataDir}/.ssh"
    "/var/lib/valley-integrator"
  ]
  ++ lib.optional (host.integrator.signingKeyFile != null) (toString host.integrator.signingKeyFile)
  ++ lib.optional host.worker.enable (toString host.worker.stateDir)
  ++ map toString (
    lib.filter (p: p != null) [
      host.backup.sshKeyFile
      host.backup.passwordFile
      host.backup.repositoryFile
    ]
  );

  stateName = lib.removePrefix "/var/lib/" (toString cfg.stateDir);
  cacheDir = "/var/cache/${stateName}";

  principalsFile = pkgs.writeText "valley-web-principals.json" (builtins.toJSON cfg.principals);

  command = lib.concatStringsSep " " [
    (lib.getExe cfg.package)
    "--instance-repo ${lib.escapeShellArg instanceRepo}"
    "--instance ${lib.escapeShellArg config.networking.hostName}"
    "--destination ${lib.escapeShellArg cfg.destination}"
    "--receiver ${lib.escapeShellArg cfg.receiver}"
    "--principals ${principalsFile}"
    "--state ${lib.escapeShellArg (toString cfg.stateDir)}"
    "--max-bundles ${cfg.maxBundles}"
    "--max-storage ${cfg.maxStorage}"
    "--registry-dir ${lib.escapeShellArg host.identity.directory}"
  ];

  size = lib.types.strMatching "[1-9][0-9]*[KMGT]?";
in
{
  options.services.valley.webPublisher = {
    enable = lib.mkEnableOption ''
      the valley web publisher: a timer that uploads this host's public
      projects, as signed exports, to a web receiver. A project is public
      when its own policy/read lets anyone read it, and a read grant to
      anyone in the identity registry, read from the instance repository's
      main in {option}`services.valley.identity.directory`, covers it at
      the destination. The manifest names {option}`networking.hostName` as
      the instance, and is signed under `<hostName>/web-export`
    '';

    destination = lib.mkOption {
      type = lib.types.strMatching "[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}";
      example = "example.org";
      description = ''
        The receiver's name, which every manifest is signed for. The
        receiver admits only manifests that name it. It is also the
        destination of the registry's edge read boundary whose read grants
        decide what anyone may read there.
      '';
    };

    receiver = lib.mkOption {
      type = lib.types.strMatching "https?://[^/?#@]+(/[^?#]*)?";
      example = "https://example.org";
      description = ''
        The receiver's base URL. The upload's paths are appended to it.
      '';
    };

    principals = lib.mkOption {
      type = lib.types.attrsOf lib.types.str;
      default = { };
      example = {
        operator = "operator";
        alice = "agent";
      };
      description = ''
        The display mapping: what an export shows in place of each
        principal's name. A principal it does not name is shown as
        `contributor`.
      '';
    };

    stateDir = lib.mkOption {
      type = lib.types.strMatching "/var/lib/[^/].*";
      default = "/var/lib/valley-web-publish";
      description = ''
        The publisher's state, under /var/lib: its export key, made on the
        first start, the key's verifier line, which the receiver pins, and
        the last acknowledgement. The cache is the directory of the same
        name under /var/cache.
      '';
    };

    memoryMax = lib.mkOption {
      type = size;
      default = "1G";
      description = ''
        The most memory a pass may use, the publisher and every git, cue
        and valley it runs together (`MemoryMax`, with no swap). Past it,
        the kernel ends the pass.
      '';
    };

    maxBundles = lib.mkOption {
      type = size;
      default = "1G";
      description = ''
        The most the bundles of one export may hold together. Each bundle
        carries a public project's whole history on main. A bundle is
        written only into what this leaves, and a pass past it sends
        nothing (`--max-bundles`).
      '';
    };

    maxStorage = lib.mkOption {
      type = size;
      default = "16G";
      description = ''
        The most the state and cache directories may hold. The cache holds
        a clone of every project the host serves, and a mirror and a bundle
        of each public one. A pass checks it before it fetches and once it
        has read the valley, and writes a bundle only into what it leaves.
        A pass past it sends nothing (`--max-storage`).
      '';
    };

    # The publisher as this flake packages it. Internal, like the host
    # module's packages: module-eval sets it to a stand-in.
    package = lib.mkOption {
      type = lib.types.package;
      internal = true;
      default = (import ./packages.nix { inherit pkgs lib; }).valley-web-publish;
      defaultText = lib.literalExpression "(import ./packages.nix { inherit pkgs lib; }).valley-web-publish";
      description = "The web publisher.";
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = host.enable;
        message = "services.valley.webPublisher needs services.valley: it reads the repositories that host serves.";
      }
      {
        assertion = host.integrator.instanceProject != null;
        message = "services.valley.webPublisher needs services.valley.integrator.instanceProject: the publisher discovers the projects, and the signers that verify their landings, through what the instance repository publishes.";
      }
    ];

    # One pass, as a oneshot the timer below starts. A switch never starts
    # it: nothing wants the service, and a changed unit is not restarted.
    # The publisher's own lock serializes a pass started by hand with the
    # timer's.
    systemd.services.valley-web-publish = {
      description = "One pass of the valley web publisher";
      after = [
        "network-online.target"
        "valley-init.service"
      ];
      wants = [ "network-online.target" ];
      restartIfChanged = false;
      unitConfig.RequiresMountsFor = toString host.dataDir;
      environment = safeDirectories // {
        HOME = toString cfg.stateDir;
        XDG_CACHE_HOME = cacheDir;
      };
      serviceConfig = {
        Type = "oneshot";
        ExecStart = command;
        # A pass that hangs is ended, and the timer starts the next. One
        # that outgrows its memory is ended too.
        TimeoutStartSec = "1h";
        MemoryMax = cfg.memoryMax;
        MemorySwapMax = "0";
        DynamicUser = true;
        SupplementaryGroups = [ host.group ];
        StateDirectory = stateName;
        StateDirectoryMode = "0700";
        CacheDirectory = stateName;
        CacheDirectoryMode = "0700";
        WorkingDirectory = toString cfg.stateDir;
        # ProtectSystem=strict leaves the whole filesystem read-only but
        # the state and cache directories, which systemd exempts. So the
        # repositories, readable through the git group, are read-only,
        # and named here so a reader of the unit sees it. A repository
        # valley-init has not made yet is skipped (the leading -).
        ProtectSystem = "strict";
        ReadOnlyPaths = map (p: "-${p}") repositories;
        InaccessiblePaths = map (p: "-${p}") keys;
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        NoNewPrivileges = true;
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

    # The first pass starts five minutes after the timer starts, and each
    # later pass five minutes after the last one ended. These are delays
    # and nothing more: a switch that runs longer than five minutes can
    # see the first pass start inside it, and any later switch can see a
    # pass start inside it.
    systemd.timers.valley-web-publish = {
      description = "Run the valley web publisher";
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnActiveSec = "5min";
        OnUnitInactiveSec = "5min";
      };
    };
  };
}
