# The web publisher (web/cmd/valley-web-publish): a oneshot on a timer
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
# backup and signing keys are made inaccessible to it. The receiver's
# token is a file outside the Nix store, which systemd reads and hands to
# the unit as a credential. The unit may connect to the addresses
# allowedAddresses lists and to no other, and it checks before each pass
# that this filter is in force.
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
  # with the same name, schema and declaration, so it is built once. The
  # publisher reads the same export with --host, for the groups and each
  # project's group and short aliases, which its web URLs are made of.
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
    "--host ${configJSON}"
    "--receiver ${lib.escapeShellArg cfg.receiver}"
    # %d is the unit's credentials directory.
    "--receiver-token %d/receiver-token"
    "--principals ${principalsFile}"
    "--state ${lib.escapeShellArg (toString cfg.stateDir)}"
    "--max-bundles ${cfg.maxBundles}"
    "--max-storage ${cfg.maxStorage}"
    "--registry-ref ${lib.escapeShellArg host.identity.ref}"
    "--registry-dir ${lib.escapeShellArg host.identity.directory}"
  ];

  size = lib.types.strMatching "[1-9][0-9]*[KMGT]?";

  # The address the unit's check sends to before each pass, which the
  # filter must deny: 198.51.100.1, in TEST-NET-2 (RFC 5737), which no
  # receiver uses. The port is discard's.
  canary = {
    address = "198.51.100.1";
    port = "9";
  };

  # The check: a UDP datagram to the canary, sent from inside the unit's
  # cgroup. The filter makes the send fail at once with EPERM, and the pass
  # starts only then. A datagram sent means the filter is not attached:
  # systemd ignores IPAddressDeny, with only a warning, where the kernel or
  # the manager lacks cgroup BPF support. Any other error, such as no route,
  # proves nothing. A TCP connect would not do: the filter drops its SYN,
  # and connect waits as it would for an address that never answers.
  egressCheck = pkgs.writeShellApplication {
    name = "valley-web-publish-egress-check";
    runtimeInputs = [
      pkgs.bash
      pkgs.coreutils
    ];
    text = ''
      export LC_ALL=C
      addr=$1 port=$2
      refuse() {
        echo "valley-web-publish-egress-check: $*; the pass does not start" >&2
        exit 1
      }
      status=0
      # shellcheck disable=SC2016
      out=$(timeout 5 bash -c 'printf canary > "/dev/udp/$1/$2"' check "$addr" "$port" 2>&1) || status=$?
      case $status:$out in
        0:*)
          refuse "a datagram to $addr port $port was sent; the unit's IP address filter did not block it (check cgroup BPF support in systemd and the kernel, and check that allowedAddresses excludes $addr)"
          ;;
        124:*)
          refuse "sending a datagram to $addr port $port timed out after 5 seconds without EPERM from the unit's IP address filter"
          ;;
        *"write error: Operation not permitted"*)
          echo "valley-web-publish-egress-check: the unit's IP address filter refused a datagram to $addr port $port (EPERM)"
          ;;
        *"Network is unreachable"* | *"No route to host"*)
          refuse "no route to $addr port $port ($out); add a route covering $addr, such as a default route, so the datagram reaches the unit's IP address filter"
          ;;
        *)
          refuse "sending a datagram to $addr port $port failed without EPERM from the unit's IP address filter ($out)"
          ;;
      esac
    '';
  };

  # An IPv4 address as a number, and whether an entry of allowedAddresses
  # covers the canary: the address itself, or a prefix that holds it. An
  # IPv6 entry covers no IPv4 address in the filter.
  ipv4 = a: lib.foldl' (n: octet: n * 256 + lib.toInt octet) 0 (lib.splitString "." a);
  coversCanary =
    entry:
    let
      m = builtins.match "((0|[1-9][0-9]{0,2})(\\.(0|[1-9][0-9]{0,2})){3})(/([0-9]+))?" entry;
      length = if lib.elemAt m 5 == null then 32 else lib.toInt (lib.elemAt m 5);
      mask = 4294967296 - lib.foldl' (n: _: n * 2) 1 (lib.range 1 (32 - length));
    in
    m != null
    && builtins.bitAnd (ipv4 (lib.head m)) mask == builtins.bitAnd (ipv4 canary.address) mask;
in
{
  options.services.valley.webPublisher = {
    enable = lib.mkEnableOption ''
      the valley web publisher: a timer that uploads this host's public
      projects, as signed exports, to a web receiver. A project is public
      when its own policy/read lets anyone read it, a read grant to
      anyone in the identity registry covers it at the destination, and
      the host declaration places it in a group, which its web URLs open
      with. The
      registry is read where the identity compiler reads it: in
      {option}`services.valley.identity.directory` at the instance
      repository's {option}`services.valley.identity.ref`. The host module
      requires that ref to be protected. The manifest names
      {option}`networking.hostName` as the instance, and is signed under
      `<hostName>/web-export`
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

    allowedAddresses = lib.mkOption {
      type = lib.types.listOf (lib.types.strMatching "[0-9a-fA-F.:]+(/[0-9]+)?");
      default = [ ];
      example = [
        "192.0.2.10"
        "2001:db8::10"
      ];
      description = ''
        IPv4 or IPv6 addresses and prefixes that a pass may connect to.
        Set this to the receiver's IP addresses. The unit allows only
        these addresses (`IPAddressAllow`) and blocks all others
        (`IPAddressDeny=any`), including loopback. A pass reads local
        repositories and runs `valley` commands that use no network, so
        it needs to reach only the receiver.

        Before each pass, the unit runs an `ExecStartPre` check that
        sends a UDP datagram to ${canary.address} and starts the pass
        only if the cgroup filter rejects the send with `EPERM`. If
        systemd or the kernel lacks cgroup BPF support, systemd logs a
        warning and leaves the filter unattached, and the check stops
        the pass from starting. Leave ${canary.address} out of this
        list, and ensure the host has a route to it, such as a default
        route.

        The unit sets `GODEBUG=netdns=cgo`, so the cgo-built publisher
        resolves host names through the C library. On NixOS, the C
        library queries `nscd` ({option}`services.nscd.enable`, enabled
        by default) over a local Unix socket, and `nscd` queries DNS
        outside the unit. If `nscd` is disabled, DNS queries run inside
        the unit and require the resolver address from `/etc/resolv.conf`
        in this list.

        The filter checks only the destination IP address and permits
        every port on a listed address. When the receiver's IP addresses
        change, passes fail until this list is updated.
      '';
    };

    receiverTokenFile = lib.mkOption {
      type = lib.types.strMatching "/.+";
      example = "/run/keys/valley-web-receiver-token";
      description = ''
        The file that holds the receiver's token, which every request of
        an upload carries: one line of 32 to 256 visible ASCII characters,
        the token the receiver is given. The path is outside the Nix
        store, and systemd reads the file as the unit's credential, so it
        may be readable by root alone.
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
        a clone and a mirror of each project a read grant to anyone names,
        and a bundle of each public one. A pass checks it before it fetches and once it
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
        assertion = cfg.allowedAddresses != [ ];
        message = "services.valley.webPublisher.allowedAddresses is empty: list the receiver's IP addresses. A pass may connect to no address the list leaves out.";
      }
      {
        assertion = !(lib.any coversCanary cfg.allowedAddresses);
        message = "services.valley.webPublisher.allowedAddresses covers ${canary.address}, which the unit sends a datagram to before each pass to check that its filter is in force. The filter must deny it: remove the address or prefix that covers it.";
      }
      {
        assertion = !(lib.hasPrefix builtins.storeDir cfg.receiverTokenFile);
        message = "services.valley.webPublisher.receiverTokenFile is in the Nix store, which every user can read; give a path outside it.";
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
      # Go's own resolver would query /etc/resolv.conf's nameserver from
      # inside the unit, where the filter denies it. The C library's asks
      # nscd over a Unix socket, which resolves outside the unit.
      environment = safeDirectories // {
        HOME = toString cfg.stateDir;
        XDG_CACHE_HOME = cacheDir;
        GODEBUG = "netdns=cgo";
      };
      serviceConfig = {
        Type = "oneshot";
        # Run in the unit's cgroup, under its filter, before each pass.
        ExecStartPre = "${lib.getExe egressCheck} ${canary.address} ${canary.port}";
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
        LoadCredential = [ "receiver-token:${cfg.receiverTokenFile}" ];
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
        # A pass connects to the receiver and to nothing else.
        IPAddressDeny = "any";
        IPAddressAllow = cfg.allowedAddresses;
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
