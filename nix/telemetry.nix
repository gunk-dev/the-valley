# Telemetry on a valley host (services.valley.telemetry): the collector a
# session reads the host's diagnostics through, and the diagnostic
# directories the host's producers export to it.
#
# valley-host.nix imports this with what it derives from the declaration,
# so the catalog below names exactly the units the host runs.
#
# A session reaches the collector only through the git user's shell, over
# ssh, with the telemetry command (../telemetry/protocol.go). The shell
# derives the principal from the authenticated key, holds it to the
# observe or report grant this module writes, and passes the collector one
# request line on a socket only the git user can open. systemd starts one
# collector per connection (Accept=yes), as its own unprivileged user,
# under the limits below, and kills one that outlives them.
#
# The collector reads what the manifest names and nothing else: the
# enumerated units' properties (an explicit list, never the environment)
# and journal, the streams the worker and the integrator export, and the
# reports it keeps itself. It is in the systemd-journal group, which lets
# its account read the whole system journal; its code asks only for the
# manifest's units. It holds no grant on the repositories, no key, and no
# way into the worker's private directory.
#
# Each producer exports into a directory of its own here, owned by it and
# group-readable by the collector alone, apart from its private state: the
# worker's key stays in its 0700 state directory.
derived:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.services.valley;
  tcfg = cfg.telemetry;
  package = cfg.packages.telemetry;

  root = "/var/lib/valley-telemetry";
  collectorState = "${root}/collector";
  workerDir = "${root}/worker";
  integratorDir = "${root}/integrator";
  socket = "/run/valley-telemetry.sock";

  grantsFile = pkgs.writeText "valley-telemetry-grants" (
    ''
      # Declared in services.valley.telemetry — do not edit.
    ''
    + lib.concatMapStrings (p: "observe ${p}\n") tcfg.observers
    + lib.concatMapStrings (p: "report ${p}\n") tcfg.reporters
  );

  webPublisher = config.services.valley.webPublisher.enable or false;
  webReceiver = config.services.valley.webReceiver.enable or false;
  integrated = lib.attrNames derived.integratedProjects;
  published = lib.attrNames derived.publishedProjects;

  source =
    name: description: enabled: units: extra:
    {
      inherit
        name
        description
        enabled
        units
        ;
      location = "local";
    }
    // extra;

  manifest = pkgs.writeText "valley-telemetry-manifest.json" (
    builtins.toJSON {
      v = 1;
      host = config.networking.hostName;
      reporters = tcfg.reporters;
      sources = [
        (source "worker" "the check worker, and the attest runs its passes start" cfg.worker.enable
          [
            "valley-worker.service"
            "valley-worker.timer"
            "valley-worker.path"
          ]
          {
            stream = "${workerDir}/stream";
            state = "${workerDir}/state.json";
            configured = {
              worker = "${derived.workerPackage}";
              attest = "${derived.attestPackage}";
            };
            instruments = [
              "passes, the change, version, head and tree each reads, batches, publication and backoff with its reason"
              "each check's evaluation, slot wait, build and result as it happens, and its build output, bounded"
              "a pass killed before its end, inferred at the next pass"
            ];
          }
        )
        (source "integrator" "the per-project integrators" cfg.integrator.enable
          (map (p: "valley-integrator@${p}.service") integrated)
          {
            stream = "${integratorDir}/stream";
            configured.integrator = "${derived.integratorPackage}";
            instruments = [ "each decision's outcome and its stages, from when it began judging the request" ];
            not_instrumented = [
              "when a request was first seen, before its decision began: a follow-up to the integrator wakeup work"
            ];
          }
        )
        (source "restacker" "the per-project restackers" cfg.restacker.enable
          (map (p: "valley-restacker@${p}.service") cfg.restacker.projects)
          {
            configured.restacker = "${derived.restackerPackage}";
            not_instrumented = [ "no producer records: lifecycle and output only" ];
          }
        )
        (source "identity" "the registry compiler" cfg.identity.enable [
          "valley-identity.service"
          "valley-identity.timer"
        ] { not_instrumented = [ "no producer records: lifecycle and output only" ]; })
        (source "signers" "the known-signers publisher" derived.publishesSigners [
          "valley-signers.service"
          "valley-signers.path"
        ] { not_instrumented = [ "no producer records: lifecycle and output only" ]; })
        (source "mirrors" "the publish queues and their drains" (published != [ ]) (
          map (p: "valley-publish@${p}.service") published ++ map (p: "valley-publish@${p}.path") published
        ) { not_instrumented = [ "no producer records: lifecycle and output only" ]; })
        (source "bus" "the NATS bus" cfg.bus.enable [
          "valley-bus.service"
          "valley-bus-init.service"
        ] { })
        (source "web-publisher" "the web publisher" webPublisher [
          "valley-web-publish.service"
          "valley-web-publish.timer"
          "valley-web-publish-policy.service"
        ] { })
        (source "web-receiver" "the web receiver, on this host" webReceiver [
          "valley-web-receive.service"
          "valley-web.service"
        ] { })
        (source "backup" "the offsite backup" derived.backupEnabled [
          "restic-backups-valley.service"
          "restic-backups-valley.timer"
        ] { })
        (source "convergence" "valley-init, which converges the repositories and hooks" true [
          "valley-init.service"
        ] { })
        (source "build-slice" "the build slice and the nix daemon whose builds run in it" cfg.buildSlice.enable [
          "valley-builds.slice"
          "nix-daemon.service"
        ] { })
        (source "telemetry" "the collector's socket" true [ "valley-telemetry.socket" ] {
          configured.collector = "${package}";
        })
      ]
      ++ map (r: {
        inherit (r) name description;
        enabled = true;
        location = "remote";
        reporter = r.principal;
        stale_after_s = r.staleAfter;
      }) tcfg.remoteSources;
    }
  );
in
{
  options.services.valley.telemetry = {
    enable = lib.mkEnableOption ''
      the telemetry collector: the host's diagnostics, read from a session
      over ssh with the telemetry command (valley telemetry). Only the
      principals in {option}`services.valley.telemetry.observers` can read,
      and only those in {option}`services.valley.telemetry.reporters` can
      send records'';

    observers = lib.mkOption {
      type = lib.types.listOf (lib.types.strMatching "[a-zA-Z0-9][a-zA-Z0-9._-]*");
      default = [ ];
      example = [ "operator" ];
      description = ''
        Principals that hold the observe grant: they read every source on
        the host, the shared units' output included, which spans every
        project. It is a host-wide grant, apart from any project's read
        consent. A principal is named by its key's tag, as a push is.
      '';
    };

    reporters = lib.mkOption {
      type = lib.types.listOf (lib.types.strMatching "[a-zA-Z0-9][a-zA-Z0-9._-]*");
      default = [ ];
      example = [ "alice" ];
      description = ''
        Principals that hold the report grant: they send records about
        their own approvals and check runs, which the collector stores
        under their name, stamped with when it received them, in a stream
        of their own of at most 4 MiB. Reporting grants no reading: a
        principal that should read its reports back is an observer too.
      '';
    };

    remoteSources = lib.mkOption {
      type = lib.types.listOf (
        lib.types.submodule {
          options = {
            name = lib.mkOption {
              type = lib.types.strMatching "[a-z0-9][a-z0-9._@-]*";
              description = "The source's name in the catalog.";
            };
            description = lib.mkOption {
              type = lib.types.str;
              description = "What the source is.";
            };
            principal = lib.mkOption {
              type = lib.types.strMatching "[a-zA-Z0-9][a-zA-Z0-9._-]*";
              description = "The reporter whose reports are the source's records.";
            };
            staleAfter = lib.mkOption {
              type = lib.types.ints.positive;
              default = 3600;
              description = "Seconds without a report after which the source reads as stale.";
            };
          };
        }
      );
      default = [ ];
      description = ''
        Components on other machines, such as a web receiver, that report
        over the same interface with their own reporter principal. Each is
        catalogued as remote, and reads as unavailable until it reports
        and as stale once it stops.
      '';
    };

    shellFlags = lib.mkOption {
      type = lib.types.str;
      default = "";
      internal = true;
      description = "The flags the git user's shell serves the telemetry command with; set by this module.";
    };
  };

  options.services.valley.packages.telemetry = lib.mkOption {
    type = lib.types.package;
    internal = true;
    default = (import ./packages.nix { inherit pkgs lib; }).telemetry;
    description = "The collector, valley-telemetry serve.";
  };

  config = lib.mkIf (cfg.enable && tcfg.enable) {
    assertions = [
      {
        assertion = lib.all (r: lib.elem r.principal tcfg.reporters) tcfg.remoteSources;
        message = "services.valley.telemetry.remoteSources names a principal that is not in services.valley.telemetry.reporters.";
      }
    ];

    services.valley.telemetry.shellFlags = "--telemetry-socket ${socket} --telemetry-grants ${grantsFile}";

    users.users.valley-telemetry = {
      isSystemUser = true;
      group = "valley-telemetry";
      description = "The valley telemetry collector";
    };
    users.groups.valley-telemetry = { };

    systemd.tmpfiles.rules = [
      "d ${root} 0755 root root - -"
      "d ${collectorState} 0700 valley-telemetry valley-telemetry - -"
    ]
    ++ lib.optional cfg.worker.enable "d ${workerDir} 2750 valley-worker valley-telemetry - -"
    ++ lib.optional cfg.integrator.enable "d ${integratorDir} 2750 ${cfg.integrator.user} valley-telemetry - -";

    # The producers export into their own directories, and are told where
    # by VALLEY_TELEMETRY_DIR. Each directory is setgid to the collector's
    # group, so what a producer makes in it is the collector's to read, and
    # a producer runs with a umask that keeps group read for it. Nothing
    # else a producer makes is in a directory others can enter: the
    # worker's own directory stays 0700, its key 0400.
    systemd.services.valley-worker = lib.mkIf cfg.worker.enable {
      environment.VALLEY_TELEMETRY_DIR = workerDir;
      serviceConfig.ReadWritePaths = [ workerDir ];
      serviceConfig.UMask = lib.mkForce "0027";
      unitConfig.RequiresMountsFor = [ workerDir ];
    };
    systemd.services."valley-integrator@" = lib.mkIf cfg.integrator.enable {
      environment.VALLEY_TELEMETRY_DIR = "${integratorDir}/stream";
      # Its own umask, 0002, already keeps group read.
      serviceConfig.ReadWritePaths = [ integratorDir ];
    };

    # One collector per connection, which only the git user can make. At
    # most 16 at once; each principal's own slots are the collector's
    # (four reads, one report). A connection that outlives the longest
    # follow is killed.
    systemd.sockets.valley-telemetry = {
      description = "The valley telemetry collector's socket";
      wantedBy = [ "sockets.target" ];
      socketConfig = {
        ListenStream = socket;
        Accept = true;
        SocketUser = cfg.user;
        SocketGroup = cfg.group;
        SocketMode = "0600";
        MaxConnections = 16;
      };
    };

    systemd.services."valley-telemetry@" = {
      description = "The valley telemetry collector, for one connection";
      path = [ config.systemd.package ];
      serviceConfig = {
        ExecStart = lib.concatStringsSep " " [
          "${package}/bin/valley-telemetry serve"
          "--manifest ${manifest}"
          "--grants ${grantsFile}"
          "--state ${collectorState}"
          "--peer-user ${cfg.user}"
        ];
        StandardInput = "socket";
        StandardOutput = "socket";
        StandardError = "journal";
        User = "valley-telemetry";
        Group = "valley-telemetry";
        SupplementaryGroups = [ "systemd-journal" ];
        RuntimeMaxSec = 360;
        MemoryMax = "256M";
        TasksMax = 32;
        CPUQuota = "50%";
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ReadWritePaths = [ collectorState ];
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        PrivateNetwork = false;
        ProtectClock = true;
        ProtectHostname = true;
        ProtectKernelLogs = true;
        ProtectKernelModules = true;
        ProtectKernelTunables = true;
        ProtectControlGroups = true;
        RestrictAddressFamilies = [ "AF_UNIX" ];
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
  };
}
