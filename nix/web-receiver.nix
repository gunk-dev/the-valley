# The web export receiver (web/cmd/valley-web-receive) and valley-web
# (web/cmd/valley-web) on a NixOS edge host: two long-running units
# over one state directory.
#
# The receiver takes the publisher's uploads and is the state directory's
# one writer. valley-web serves the activated export to anonymous readers
# and only reads the directory. Each runs as a user of its own, both in
# the valley-web group. The directory is the receiver's, mode 0750, so
# valley-web reads it through the group, and the receiver keeps its
# staging area to itself (web/export/store.go). Each unit has its own
# listener and its own memory cap, so an upload that saturates the
# receiver leaves valley-web serving, and neither unit needs the other to
# run: valley-web keeps serving while the receiver restarts.
#
# The receiver's token is a file outside the Nix store. systemd reads it
# and hands it to the receiver's unit alone as a credential, so
# valley-web's user never reads it. The verifier lines are public, and go
# in the store.
{
  config,
  pkgs,
  lib,
  ...
}:

let
  cfg = config.services.valley.webReceiver;
  stateDir = toString cfg.stateDir;
  stateName = lib.removePrefix "/var/lib/" stateDir;

  verifiersFile = pkgs.writeText "valley-web-verifiers" (
    lib.concatMapStrings (line: line + "\n") cfg.verifiers
  );

  receiveCommand = lib.concatStringsSep " " [
    (lib.getExe cfg.receiverPackage)
    "--state ${lib.escapeShellArg stateDir}"
    "--listen ${lib.escapeShellArg cfg.listen}"
    "--instance ${lib.escapeShellArg cfg.instance}"
    "--destination ${lib.escapeShellArg cfg.destination}"
    "--verifiers ${verifiersFile}"
    # %d is the unit's credentials directory.
    "--token-file %d/token"
    "--max-body ${cfg.maxBody}"
    "--max-upload ${cfg.maxUpload}"
  ];

  webCommand = lib.concatStringsSep " " [
    (lib.getExe cfg.webPackage)
    "--state ${lib.escapeShellArg stateDir}"
    "--listen ${lib.escapeShellArg cfg.webListen}"
  ];

  # What both units are held to. ProtectSystem=strict leaves the whole
  # filesystem read-only but the receiver's state directory, which systemd
  # exempts for the unit that names it.
  confinement = {
    ProtectSystem = "strict";
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
    MemorySwapMax = "0";
    Restart = "on-failure";
    RestartSec = "5s";
  };

  name = lib.types.strMatching "[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}";
  size = lib.types.strMatching "[1-9][0-9]*[KMGT]?";
in
{
  options.services.valley.webReceiver = {
    enable = lib.mkEnableOption ''
      the valley web export receiver and valley-web on this host: the
      receiver takes a valley host's signed uploads into
      {option}`services.valley.webReceiver.stateDir`, and valley-web
      serves what it activates to anonymous readers
    '';

    instance = lib.mkOption {
      type = name;
      example = "valley-host";
      description = ''
        The instance every manifest must name: the publishing valley
        host's {option}`networking.hostName`, which its publisher names.
      '';
    };

    destination = lib.mkOption {
      type = name;
      example = "example.org";
      description = ''
        This receiver's name, which every manifest must name: the
        publisher's {option}`services.valley.webPublisher.destination`.
      '';
    };

    verifiers = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      example = [ "valley-host/web-export+1a2b3c4d+AQ..." ];
      description = ''
        The publisher's verifier lines. A manifest must open under one of
        them. The publisher writes its own to `verifier` in its state
        directory, and prints it whenever it changes.
      '';
    };

    tokenFile = lib.mkOption {
      type = lib.types.strMatching "/.+";
      example = "/run/keys/valley-web-receiver-token";
      description = ''
        The file that holds the token every request to the receiver must
        carry: one line of 32 to 256 visible ASCII characters. The
        publisher is given the same token. The path is outside the Nix
        store, and systemd reads the file as the receiver's credential, so
        it may be readable by root alone.
      '';
    };

    listen = lib.mkOption {
      type = lib.types.str;
      example = "192.0.2.10:8081";
      description = ''
        The TCP address the receiver listens on, which the publisher
        reaches. Every request to it must carry the token.
      '';
    };

    webListen = lib.mkOption {
      type = lib.types.str;
      example = "127.0.0.1:8080";
      description = ''
        The TCP address valley-web listens on, which readers reach, or a
        reverse proxy in front of it.
      '';
    };

    stateDir = lib.mkOption {
      type = lib.types.strMatching "/var/lib/[^/].*";
      default = "/var/lib/valley-web";
      description = ''
        The state directory, under /var/lib: the receiver's, mode 0750,
        and readable by valley-web through the valley-web group.
      '';
    };

    maxBody = lib.mkOption {
      type = size;
      default = "1G";
      description = ''
        The most bytes one body of an upload may declare: export.json, or
        one project's bundle (`--max-body`).
      '';
    };

    maxUpload = lib.mkOption {
      type = size;
      default = "2G";
      description = ''
        The most bytes the bodies of one manifest may declare together
        (`--max-upload`).
      '';
    };

    receiverMemoryMax = lib.mkOption {
      type = size;
      default = "2G";
      description = ''
        The most memory the receiver may use, with every git it runs to
        accept a bundle (`MemoryMax`, with no swap).
      '';
    };

    webMemoryMax = lib.mkOption {
      type = size;
      default = "1G";
      description = ''
        The most memory valley-web may use, with every git it runs to
        browse a repository (`MemoryMax`, with no swap).
      '';
    };

    # The programs as this flake packages them. Internal, like the host
    # module's packages: module-eval sets them to stand-ins.
    receiverPackage = lib.mkOption {
      type = lib.types.package;
      internal = true;
      default = (import ./packages.nix { inherit pkgs lib; }).valley-web-receive;
      defaultText = lib.literalExpression "(import ./packages.nix { inherit pkgs lib; }).valley-web-receive";
      description = "The web export receiver.";
    };
    webPackage = lib.mkOption {
      type = lib.types.package;
      internal = true;
      default = (import ./packages.nix { inherit pkgs lib; }).valley-web;
      defaultText = lib.literalExpression "(import ./packages.nix { inherit pkgs lib; }).valley-web";
      description = "The web server.";
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.verifiers != [ ];
        message = "services.valley.webReceiver.verifiers names no verifier line, and the receiver admits no manifest without one.";
      }
      {
        assertion = !(lib.hasPrefix builtins.storeDir cfg.tokenFile);
        message = "services.valley.webReceiver.tokenFile is in the Nix store, which every user can read; give a path outside it.";
      }
    ];

    users.groups.valley-web = { };
    users.users.valley-web-receive = {
      isSystemUser = true;
      group = "valley-web";
      description = "valley web export receiver";
    };
    users.users.valley-web = {
      isSystemUser = true;
      group = "valley-web";
      description = "valley-web";
    };

    systemd.services.valley-web-receive = {
      description = "The valley web export receiver";
      wantedBy = [ "multi-user.target" ];
      after = [ "network.target" ];
      serviceConfig = confinement // {
        ExecStart = receiveCommand;
        User = "valley-web-receive";
        Group = "valley-web";
        StateDirectory = stateName;
        StateDirectoryMode = "0750";
        WorkingDirectory = stateDir;
        LoadCredential = [ "token:${cfg.tokenFile}" ];
        MemoryMax = cfg.receiverMemoryMax;
      };
    };

    # valley-web starts after the receiver, which makes the state
    # directory, and needs nothing else of it. It writes nothing: the
    # state directory is read-only to it, as is everything else. A state
    # directory not made yet is skipped (the leading -), and valley-web
    # fails and is restarted until it is there.
    systemd.services.valley-web = {
      description = "valley-web, serving the web export receiver's state directory";
      wantedBy = [ "multi-user.target" ];
      after = [
        "network.target"
        "valley-web-receive.service"
      ];
      serviceConfig = confinement // {
        ExecStart = webCommand;
        User = "valley-web";
        Group = "valley-web";
        ReadOnlyPaths = [ "-${stateDir}" ];
        MemoryMax = cfg.webMemoryMax;
      };
    };
  };
}
