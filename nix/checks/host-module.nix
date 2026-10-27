# What the NixOS module renders from a declaration, and what it must refuse.
# The web publisher's module, which sits beside it, is held here too, and
# so is the web receiver's module, which an edge host runs.
# The refusals are eval-time: a host that must not evaluate is caught by a
# throw here, since there is nothing to build once it has. What a host does
# render is pinned by module-eval.sh over the rendered scripts.
#
# Every host here runs the stand-ins for this flake's programs
# (nix/checks/hosts.nix). Nothing here runs a program: each assertion is
# about the units, hooks and scripts the module renders, which name a
# program's path and the flags it is given. So module-eval changes when the
# module, a host declaration or this check does, and not when a program
# does.
{
  pkgs,
  lib,
  self,
  stubbedHosts,
  ...
}:
let
  inherit (stubbedHosts)
    unprotectedInstanceHost
    acceptEnvHost
    matchAcceptEnvHost
    wildcardAcceptEnvHost
    quotedAcceptEnvHost
    tabAcceptEnvHost
    setEnvHost
    permitUserEnvironmentHost
    conflictingKeysHost
    host
    noBackupHost
    noSecretsHost
    protectedHost
    busHost
    mirrorHost
    integratorHost
    integratorSelfHost
    restrictedHost
    spacedSignerProjectsHost
    unicodeSpacedSignerProjectsHost
    identityHost
    workerHost
    buildSliceHost
    noSliceWorkerHost
    unrestrictedWorkerHost
    widerWorkerHost
    integratorNamedWorkerHost
    restackerHost
    unservedRestackerHost
    keylessRestackerHost
    ;

  # The web publisher's module (nix/web-publisher.nix) beside the host
  # module, over integratorHost: its instance repository is the one the
  # controllers read the floor from. The host also has a backup key the
  # publisher must not read. The publisher is a stand-in, as the host's
  # programs are. webPublisherOffHost imports the module and leaves it off.
  webPublisherStub = pkgs.writeShellScriptBin "valley-web-publish" ''
    echo "valley-web-publish: a stand-in the module checks render hosts over, which runs nothing" >&2
    exit 1
  '';
  webPublisherOffHost = stubbedHosts.integratorHost.extendModules {
    modules = [
      self.nixosModules.web-publisher
      { services.valley.webPublisher.package = webPublisherStub; }
    ];
  };
  webPublisherHost = webPublisherOffHost.extendModules {
    modules = [
      {
        networking.hostName = "valley-host";
        services.valley.backup.sshKeyFile = "/run/agenix/valley-git-ssh-key";
        services.valley.webPublisher = {
          enable = true;
          destination = "example.org";
          receiver = "https://example.org";
          receiverTokenFile = "/run/keys/valley-web-receiver-token";
          principals = {
            operator = "operator";
            alice = "agent";
          };
        };
      }
    ];
  };
  # The publisher reading the registry at a ref the instance repository's
  # protection does not cover.
  unprotectedRegistryWebPublisherHost = webPublisherHost.extendModules {
    modules = [ { services.valley.identity.ref = "refs/heads/registry"; } ];
  };
  # The publisher reading the registry at a short ref name, which git would
  # resolve by trying other names in turn.
  shortRegistryRefWebPublisherHost = webPublisherHost.extendModules {
    modules = [ { services.valley.identity.ref = "registry"; } ];
  };
  # The publisher with no instance repository to discover the projects and
  # the signers through.
  noInstanceWebPublisherHost = webPublisherHost.extendModules {
    modules = [
      {
        services.valley.integrator = {
          instanceProject = lib.mkForce null;
          instancePolicy = "/var/lib/valley-instance/policy/instance";
        };
      }
    ];
  };

  # The web receiver's module (nix/web-receiver.nix) on an edge host,
  # which runs no valley host. Its programs are stand-ins too.
  # webReceiverOffHost imports the module and leaves it off.
  edgeStub =
    name:
    pkgs.writeShellScriptBin name ''
      echo "${name}: a stand-in the module checks render hosts over, which runs nothing" >&2
      exit 1
    '';
  webReceiverOffHost = lib.nixosSystem {
    inherit pkgs;
    modules = [
      self.nixosModules.web-receiver
      {
        fileSystems."/" = {
          device = "/dev/disk/by-label/nixos";
          fsType = "ext4";
        };
        boot.loader.grub.enable = false;
        system.stateVersion = "25.11";
        services.valley.webReceiver = {
          receiverPackage = edgeStub "valley-web-receive";
          webPackage = edgeStub "valley-web";
        };
      }
    ];
  };
  webReceiverHost = webReceiverOffHost.extendModules {
    modules = [
      {
        services.valley.webReceiver = {
          enable = true;
          instance = "valley-host";
          destination = "example.org";
          verifiers = [ "valley-host/web-export+1a2b3c4d+AQ" ];
          tokenFile = "/run/keys/valley-web-receiver-token";
          listen = "192.0.2.10:8081";
          webListen = "127.0.0.1:8080";
        };
      }
    ];
  };
  # The receiver with its token in the Nix store, and with no verifier.
  storeTokenWebReceiverHost = webReceiverHost.extendModules {
    modules = [
      { services.valley.webReceiver.tokenFile = lib.mkForce "${pkgs.writeText "token" "x"}"; }
    ];
  };
  noVerifierWebReceiverHost = webReceiverHost.extendModules {
    modules = [ { services.valley.webReceiver.verifiers = lib.mkForce [ ]; } ];
  };
  # The publisher with its token in the Nix store.
  storeTokenWebPublisherHost = webPublisherHost.extendModules {
    modules = [
      { services.valley.webPublisher.receiverTokenFile = lib.mkForce "${pkgs.writeText "token" "x"}"; }
    ];
  };

  failedAssertions = builtins.filter (a: !a.assertion) (
    host.config.assertions
    ++ noBackupHost.config.assertions
    ++ busHost.config.assertions
    ++ mirrorHost.config.assertions
    ++ protectedHost.config.assertions
    ++ integratorHost.config.assertions
    ++ integratorSelfHost.config.assertions
    ++ restrictedHost.config.assertions
    ++ identityHost.config.assertions
    ++ workerHost.config.assertions
    ++ restackerHost.config.assertions
    ++ buildSliceHost.config.assertions
    ++ webPublisherHost.config.assertions
    ++ webPublisherOffHost.config.assertions
    ++ webReceiverHost.config.assertions
    ++ webReceiverOffHost.config.assertions
  );

  missingSecretAssertions = builtins.filter (a: !a.assertion) noSecretsHost.config.assertions;

  # The hosts the module must refuse, each with the words its refusal must
  # say. An assertion that stopped firing would leave the push policy
  # quietly wrong, so each is held to naming what is wrong.
  refusedHosts = [
    {
      host = unprotectedInstanceHost;
      says = "whose refs/heads/main is not protected";
      why = "an instance repository without protection would let any key that can push rewrite the floor and the registry";
    }
    {
      host = acceptEnvHost;
      says = "AcceptEnv";
      why = "sshd accepting the principal variable from the client would let any key name any principal";
    }
    {
      host = matchAcceptEnvHost;
      says = "GIT_CONFIG_*";
      why = "sshd accepting GIT_ variables in the git user's Match block, in whatever case the keyword is written, would let a client set git's configuration";
    }
    {
      host = wildcardAcceptEnvHost;
      says = "G?T_*";
      why = "a wildcard that admits GIT_ variables admits them however it is spelled";
    }
    {
      host = quotedAcceptEnvHost;
      says = "holds a quote or a backslash";
      why = "a quoted name is one sshd unquotes and admits, so a check that read it differently would pass it";
    }
    {
      host = tabAcceptEnvHost;
      says = "VALLEY_PRINCIPAL from the client";
      why = "a name after a tab is a name sshd admits";
    }
    {
      host = setEnvHost;
      says = "SetEnv";
      why = "sshd setting the principal itself would make every key push as it";
    }
    {
      host = permitUserEnvironmentHost;
      says = "PermitUserEnvironment";
      why = "a PermitUserEnvironment wider than the principal variable would let a key's entry or ~/.ssh/environment set what the push policy does not expect";
    }
    {
      host = spacedSignerProjectsHost;
      says = "services.valley.integrator.signerProjects holds";
      why = "a project with a space in it would be read back as two projects the declaration never listed";
    }
    {
      host = unicodeSpacedSignerProjectsHost;
      says = "services.valley.integrator.signerProjects holds";
      why = "a project holding a no-break space would be read back by the integrator as two projects, or refused at start";
    }
    {
      host = noSliceWorkerHost;
      says = "services.valley.worker.enable needs services.valley.buildSlice.enable";
      why = "a worker on a host without the build slice would build uncapped, and capping every build of the daemon is the host's decision, not the worker's";
    }
    {
      host = unrestrictedWorkerHost;
      says = "must restrict worker.valley.invalid/attestations, the worker's signer, to exactly the worker's projects";
      why = "a worker whose signer no project list restricts would have its evidence counted in every project by every controller that holds its key";
    }
    {
      host = widerWorkerHost;
      says = "must restrict worker.valley.invalid/attestations, the worker's signer, to exactly the worker's projects";
      why = "a worker trusted in a project it does not build would be trusted where it does no work";
    }
    {
      host = integratorNamedWorkerHost;
      says = "services.valley.worker.signerName must differ from integrator";
      why = "a worker signing under the controllers' name would make every controller refuse to start";
    }
    {
      host = unservedRestackerHost;
      says = "services.valley.restacker.projects must name at least one project, and only projects this host serves, and it names nowhere";
      why = "a controller for a project the host does not serve would fail every pass";
    }
    {
      host = keylessRestackerHost;
      says = "services.valley.restacker.sshKeyFile must be set";
      why = "a restacker with no key cannot reach the repositories it serves, except as a user that could write them on disk";
    }
    {
      host = conflictingKeysHost;
      says = "more than one principal tag";
      why = "a key declared under two tags would push as whichever entry sshd reads first";
    }
    {
      host = unprotectedRegistryWebPublisherHost;
      says = "whose refs/heads/registry is not protected";
      why = "a publisher reading the registry at an unprotected ref would let any key that can push there grant anyone read";
    }
    {
      host = shortRegistryRefWebPublisherHost;
      says = "services.valley.identity.ref is registry, which is not a full ref name";
      why = "git resolves a ref that is not a full name by trying several refs in turn, and a pusher can make one of them";
    }
    {
      host = storeTokenWebPublisherHost;
      says = "services.valley.webPublisher.receiverTokenFile is in the Nix store";
      why = "the publisher's receiver token is in the Nix store";
    }
    {
      host = storeTokenWebReceiverHost;
      says = "services.valley.webReceiver.tokenFile is in the Nix store";
      why = "the receiver's token is in the Nix store";
    }
    {
      host = noVerifierWebReceiverHost;
      says = "services.valley.webReceiver.verifiers names no verifier line";
      why = "the receiver pins no verifier";
    }
    {
      host = noInstanceWebPublisherHost;
      says = "services.valley.webPublisher needs services.valley.integrator.instanceProject";
      why = "a publisher with no instance repository has no publication to discover the projects and their signers through";
    }
  ];

  unrefusedHosts = builtins.filter (
    r: !(lib.any (a: !a.assertion && lib.hasInfix r.says a.message) r.host.config.assertions)
  ) refusedHosts;

  protectedKeyLines = protectedHost.config.users.users.git.openssh.authorizedKeys.keys;

  resticRenderedWithoutDeclaration =
    noBackupHost.config.systemd.services ? "restic-backups-valley"
    || noBackupHost.config.systemd.timers ? "restic-backups-valley";

  # The bus defaults off; a host that never asked for one must
  # render zero bus machinery.
  busRenderedWithoutEnable =
    noBackupHost.config.systemd.services ? valley-bus
    || noBackupHost.config.systemd.services ? valley-bus-init;

  # A declaration with no protection block and no tagged key still gets
  # the push policy on every project — the policy is the host's, and a
  # protection block only adds protected refs to it — and nothing of the
  # principal machinery: no tag on a key, no sshd change.
  protectionRenderedWithoutDeclaration =
    noBackupHost.config.services.openssh.settings ? PermitUserEnvironment
    || lib.any (lib.hasInfix "environment=") (
      noBackupHost.config.users.users.git.openssh.authorizedKeys.keys
    );

  policyMissingWithoutDeclaration =
    !(lib.hasInfix "valley-protect-the-valley" noBackupHost.config.systemd.services.valley-init.script);

  # The integrator's units. What a controller *does* is not checked here
  # and no VM is booted: that is integrator-e2e's job (the real binaries
  # over real repositories) and, past it, live exercise on a host. These
  # assertions cover the wiring — that a controller is rendered for what
  # the declaration protects and for nothing else, under an identity that
  # is not the git user's, pointed at the key and the signers the consumer
  # supplied.
  #
  # There is a seam between those two halves that neither reaches, and in a
  # single day it produced five deployment failures. Every assertion below is
  # an eval of the unit's text; integrator-e2e runs the binaries with no unit
  # around them at all. What sits between is the unit's sandbox and identity
  # meeting the filesystem the binaries actually use at runtime:
  #
  #   - git refusing a repository the service does not own (bd-500adf7)
  #   - a push authenticating as the operator rather than as the run
  #     (bd-353c59d)
  #   - no writable temporary directory, so no worktree could be made
  #   - the worktree metadata root, created by whichever service reached it
  #     first and then unchmodable by the other, which failed valley-init and
  #     took every valley unit down with it
  #   - the state directory not writable under ProtectSystem=strict unless
  #     the unit names it, which the previous fix had made the scratch root
  #
  # Each was found by a host, minutes of a live service after a deploy that
  # every check here passed. They compose: the third's fix set up the fourth,
  # and the fourth's fix set up the fifth. That is the argument. A seam whose
  # failures feed each other is not one to pin an assertion at a time, and
  # each assertion here that marks a piece of it — the git ownership
  # exception, TMPDIR, the worktrees root, ReadWritePaths — marks a place a
  # smoke boot would have spoken first.
  #
  # A nixosTest that boots one host, starts the units and reconciles one
  # change end to end is the next check this module needs. Nothing else
  # closes the seam, and five incidents is enough evidence of what it costs
  # to leave open. Not built here.
  #
  # Protection and integration are separable declarations: protectedHost
  # declares the same protected projects with the service off, and must
  # evaluate to no controller at all.
  integratorRenderedWithoutEnable =
    protectedHost.config.systemd.services ? "valley-integrator@"
    || protectedHost.config.systemd.targets ? valley-integrators
    || lib.hasInfix "sharedRepository" protectedHost.config.systemd.services.valley-init.script;

  # One target pulls in each controller, and another the path unit that
  # publishes each project's queued moves, landings included.
  integratorWants = integratorHost.config.systemd.targets.valley-integrators.wants;
  controllers = lib.filter (lib.hasPrefix "valley-integrator@") integratorWants;
  publishers = integratorHost.config.systemd.targets.valley-publish.wants;

  integratorUser = integratorHost.config.systemd.services."valley-integrator@".serviceConfig.User;

  # The controller is not the repositories' owner, which is the whole point
  # of running it as itself — so git needs an ownership exception, and
  # where that exception lives is load-bearing. GIT_CONFIG_* is command
  # scope, the only scope besides system and global that safe.directory is
  # read from, and the unit's environment is inherited by every git the
  # controller drives. It names one repository: a wildcard here would hand
  # a compromised controller every repository on the host.
  integratorGitEnv = integratorHost.config.systemd.services."valley-integrator@".environment;

  # The count is what git reads; an entry past it is an entry git ignores,
  # silently. So it is checked against the entries actually rendered rather
  # than against a number written here, which would have to be edited every
  # time a grant is added and would go on passing if it were not.
  integratorSafeDirectory =
    integratorGitEnv.GIT_CONFIG_KEY_0 or null != "safe.directory"
    || integratorGitEnv.GIT_CONFIG_VALUE_0 or null != "/srv/git/%i.git"
    || integratorGitEnv.GIT_CONFIG_COUNT or null
      != toString (
        lib.length (lib.filter (lib.hasPrefix "GIT_CONFIG_KEY_") (lib.attrNames integratorGitEnv))
      );

  integratorService = integratorHost.config.systemd.services."valley-integrator@".serviceConfig;

  # Scratch goes under the state directory, because the sandbox leaves no
  # writable temporary directory and a verdict needs a checkout on disk. Two
  # halves, and each was a live failure on its own. TMPDIR naming a path
  # outside the state directory is the first; the state directory not being
  # named in ReadWritePaths is the second, which failed even though
  # ProtectSystem=strict is documented to leave a declared state directory
  # writable. Everything the unit may write is enumerated, so what a reader
  # of the unit sees is the whole of it.
  # The floor is read from the instance repository's integrated tip
  # (dcr-f41f718, dcr-b87f6e8), so a controller is told which repository
  # carries it and reads it there. Three things have to be true of that, and
  # each was a way for it to be silently wrong.
  #
  # The controller has to be told at all — the failure this replaced was a
  # binary looking for a materialized directory nothing populated.
  #
  # It has to work when the repository carrying the floor is not the one the
  # controller serves, which is the ordinary case: on integratorHost every
  # controller serves a protected project and reads the floor out of
  # "open", which no controller serves. git refuses a repository the caller
  # does not own, so that read needs its own safe.directory entry, named and
  # not wildcarded — the same failure as the served repository's, one
  # repository over.
  #
  # And it has to keep working when the two coincide, which is what an
  # instance's own host looks like: integratorSelfHost's controller for
  # "guarded" reads the floor from "guarded".
  integratorFloor =
    let
      command = integratorHost.config.systemd.services."valley-integrator@".serviceConfig.ExecStart;
      selfCommand = integratorSelfHost.config.systemd.services."valley-integrator@".serviceConfig.ExecStart;
      env = integratorHost.config.systemd.services."valley-integrator@".environment;
    in
    !(lib.hasInfix "--instance-repo /srv/git/sealed.git" command)
    || !(lib.hasInfix "--instance-repo /srv/git/guarded.git" selfCommand)
    || env.GIT_CONFIG_COUNT or null != "2"
    || env.GIT_CONFIG_KEY_1 or null != "safe.directory"
    || env.GIT_CONFIG_VALUE_1 or null != "/srv/git/sealed.git";

  integratorScratch =
    integratorGitEnv.TMPDIR or null != "/var/lib/${integratorService.StateDirectory}"
    || !(builtins.elem "/var/lib/${integratorService.StateDirectory}" integratorService.ReadWritePaths);

  # The registry compiler. It is off by default and must stay inert: a
  # consumer that has not asked for it renders the system it always did.
  # integratorHost is the strongest witness available — the integrator on,
  # the signers file supplied by hand, and every option of the identity
  # block left alone.
  identityRenderedWithoutEnable =
    integratorHost.config.systemd.services ? valley-identity
    || integratorHost.config.systemd.timers ? valley-identity
    || integratorHost.config.services.openssh.settings ? PermitUserEnvironment
    || lib.any (lib.hasInfix "valley-identity") integratorHost.config.services.openssh.authorizedKeysFiles
    || lib.hasInfix "valley-identity" integratorHost.config.services.openssh.extraConfig
    || lib.hasInfix "valley-identity" (
      integratorHost.config.systemd.services."valley-integrator@".serviceConfig.ExecStart
    )
    || lib.hasInfix "--name " (
      integratorHost.config.systemd.services."valley-integrator@".serviceConfig.ExecStart
    );

  identityService = identityHost.config.systemd.services.valley-identity.serviceConfig;

  identityStateDir = "/var/lib/${identityService.StateDirectory or ""}";

  # What the compiled artifacts have to be true of, and each was a way for
  # the machinery to be quietly wrong.
  #
  # The compiler writes into its state directory and nowhere else, so that
  # directory has to be the one path the unit may write.
  #
  # sshd walks every directory above an authorized-keys file and refuses a
  # group- or world-writable one, so the state directory is 0755 rather than
  # the 0700 a service's state usually takes — and the unit runs as the git
  # user, because sshd equally refuses a file owned by anyone but root or
  # the account it authorizes.
  #
  # The compiled file is added to what sshd reads for the git user rather
  # than replacing it, and for the git user alone: the user's Match block
  # names the declared keys and the compiled ones, and nothing else — no
  # ~/.ssh/authorized_keys, no AuthorizedKeysCommand — so every key the git
  # user accepts carries a tag this module or the compiler wrote. The
  # compiler is handed the declared keys, so it can refuse a registry key
  # declared under another tag.
  identityCompiler =
    identityService.User or null != identityHost.config.services.valley.user
    || identityService.StateDirectoryMode or null != "0755"
    || identityService.ReadWritePaths or [ ] != [ identityStateDir ]
    || !(lib.hasInfix "--repo /srv/git/sealed.git" identityService.ExecStart)
    || !(lib.hasInfix "--known-signers ${identityStateDir}/known-signers" identityService.ExecStart)
    || !(lib.hasInfix "--authorized-keys ${identityStateDir}/authorized_keys.git" identityService.ExecStart)
    || !(lib.hasInfix "--grants ${identityStateDir}/grants" identityService.ExecStart)
    || !(lib.hasInfix "-valley-declared-keys" identityService.ExecStart)
    || !(lib.hasInfix "AuthorizedKeysFile /etc/ssh/authorized_keys.d/%u ${identityStateDir}/authorized_keys.git\n" identityHost.config.services.openssh.extraConfig)
    || !(lib.hasInfix "AuthorizedKeysCommand none" identityHost.config.services.openssh.extraConfig);

  # A controller under a compiled registry reads the compiled signers file
  # and signs under the name the registry publishes its key under. The key
  # hash binds name to key, so a controller signing under the host's default
  # name while the registry publishes it as "integrator" would produce notes
  # no verifier can find a key for.
  identityIntegrator =
    let
      command = identityHost.config.systemd.services."valley-integrator@".serviceConfig.ExecStart;
    in
    !(lib.hasInfix "--known-signers ${identityStateDir}/known-signers" command)
    || !(lib.hasInfix "--name integrator" command)
    || !(builtins.elem "valley-identity.service"
      identityHost.config.systemd.services."valley-integrator@".after
    );

  # The signers publication. A host whose integrator runs and which serves
  # the instance repository publishes there what a client verifies its
  # transfer statements with. What it publishes has to be what the
  # controllers use: the same signers file and the same --name, or none on
  # either, in which case both take the binary's default. The publisher
  # writes as the git user, which owns the instance repository, may write
  # that repository and nothing else, runs after the compilation when there
  # is one, and runs again whenever the signers file changes. The signer
  # projects file is the controllers' too, or neither has one.
  flagValue =
    flag: command:
    let
      m = builtins.match ".*--${flag} ([^ ]+).*" command;
    in
    if m == null then null else lib.head m;

  signersPublisher =
    host: repo:
    let
      unit = host.config.systemd.services.valley-signers;
      publish = unit.serviceConfig.ExecStart;
      controller = host.config.systemd.services."valley-integrator@".serviceConfig.ExecStart;
    in
    !(lib.hasInfix "/bin/integrator publish-signers " publish)
    || flagValue "instance-repo" publish != repo
    || flagValue "known-signers" publish != flagValue "known-signers" controller
    || flagValue "name" publish != flagValue "name" controller
    || flagValue "signer-projects" publish != flagValue "signer-projects" controller
    || unit.serviceConfig.User or null != "git"
    || unit.serviceConfig.ReadWritePaths or [ ] != [ repo ]
    || !(builtins.elem "valley-init.service" unit.after)
    || !(builtins.elem "multi-user.target" unit.wantedBy)
    || host.config.systemd.paths.valley-signers.pathConfig.PathChanged or null
      != flagValue "known-signers" controller
    || !(builtins.elem "multi-user.target" host.config.systemd.paths.valley-signers.wantedBy);

  signersRenderedWithoutIntegrator =
    protectedHost.config.systemd.services ? valley-signers
    || protectedHost.config.systemd.paths ? valley-signers;

  signersUnpublished =
    signersPublisher integratorHost "/srv/git/sealed.git"
    || signersPublisher restrictedHost "/srv/git/sealed.git"
    || signersPublisher integratorSelfHost "/srv/git/guarded.git"
    || signersPublisher identityHost "/srv/git/sealed.git"
    || flagValue "name" identityHost.config.systemd.services.valley-signers.serviceConfig.ExecStart
      != "integrator"
    || !(builtins.elem "valley-identity.service" identityHost.config.systemd.services.valley-signers.after);

  # A host that restricts no signer gives its controllers no signer
  # projects file, and one that restricts some gives them one.
  signerProjectsMisrendered =
    flagValue "signer-projects" integratorHost.config.systemd.services."valley-integrator@".serviceConfig.ExecStart
      != null
    || flagValue "signer-projects" restrictedHost.config.systemd.services."valley-integrator@".serviceConfig.ExecStart
      == null;

  # The declared keys stay authorized beside the compiled ones. A compiled
  # file that replaced them would make a bad compilation — or a first boot
  # before the first one — a locked-out git user, which is the one failure
  # this machinery must never have.
  identityKeepsDeclaredKeys =
    identityHost.config.users.users.git.openssh.authorizedKeys.keys == [ ];

  # The check worker and the build slice. Both are off by default, and a
  # host that never asked for them renders none of either: no unit, no
  # account, no slice, and a nix daemon where NixOS puts it. integratorHost
  # is the strongest witness, with the integrator the worker would need
  # already on.
  workerRenderedWithoutEnable =
    integratorHost.config.systemd.services ? valley-worker
    || integratorHost.config.systemd.timers ? valley-worker
    || integratorHost.config.systemd.slices ? valley-builds
    || integratorHost.config.users.users ? valley-worker
    || integratorHost.config.systemd.services.nix-daemon.serviceConfig ? Slice;

  # The build slice is a host decision of its own. On, it puts the daemon
  # in the slice at the caps the host names, with no swap, and nothing of
  # the worker.
  buildSliceMisrendered =
    let
      slice = buildSliceHost.config.systemd.slices.valley-builds.sliceConfig or { };
    in
    buildSliceHost.config.systemd.services.nix-daemon.serviceConfig.Slice or null
      != "valley-builds.slice"
    || slice.CPUQuota or null != "200%"
    || slice.MemoryMax or null != "4G"
    || slice.MemorySwapMax or null != "0"
    || buildSliceHost.config.systemd.services ? valley-worker
    || buildSliceHost.config.users.users ? valley-worker;

  # What the VM check runs, held at the operator's values. worker-e2e
  # boots the worker and lowers the memory cap at runtime to make a build
  # run past it, so the values themselves are held here. The caps are on
  # a slice that holds the worker and the nix daemon both, since a build
  # runs in the daemon's cgroup and never in the worker's; the slice and
  # the daemon set no device policy, so a NixOS test still gets /dev/kvm.
  # The worker runs on one CPU, so attest, which runs one nix build per CPU
  # it may run on, runs one at a time, and each nix build builds one
  # derivation at a time with 4 cores. The account is not in the git
  # group, so the repositories on disk are not the worker's to read or
  # write, and it reaches them only through this host's sshd, checking the
  # host's key. Its timer starts the first pass five minutes after the
  # timer starts, not after boot, and each later pass one minute after the
  # last one ended.
  workerMisrendered =
    let
      unit = workerHost.config.systemd.services.valley-worker;
      service = unit.serviceConfig;
      daemon = workerHost.config.systemd.services.nix-daemon.serviceConfig;
      slice = workerHost.config.systemd.slices.valley-builds.sliceConfig;
      account = workerHost.config.users.users.valley-worker;
      timer = workerHost.config.systemd.timers.valley-worker;
      devicePolicy = c: c ? DevicePolicy || c ? DeviceAllow || c ? PrivateDevices;
    in
    service.User or null != "valley-worker"
    || account.group != "valley-worker"
    || account.extraGroups != [ ]
    || service.Type or null != "oneshot"
    || service.Slice or null != "valley-builds.slice"
    || daemon.Slice or null != "valley-builds.slice"
    || slice.CPUQuota or null != "400%"
    || slice.MemoryMax or null != "8G"
    || slice.MemorySwapMax or null != "0"
    || devicePolicy slice
    || devicePolicy daemon
    || service.CPUAffinity or null != "0"
    || !(lib.hasInfix "max-jobs = 1\n" unit.environment.NIX_CONFIG)
    || !(lib.hasInfix "cores = 4\n" unit.environment.NIX_CONFIG)
    || !(lib.hasInfix "builders =\n" unit.environment.NIX_CONFIG)
    || !(lib.hasInfix "-o StrictHostKeyChecking=yes -o HostKeyAlias=localhost" unit.environment.GIT_SSH_COMMAND)
    || service.ReadWritePaths or [ ] != [
      "/var/lib/valley-worker"
      "/nix/var/nix/daemon-socket"
    ]
    || !(lib.hasInfix "exec valley-worker --repo guarded --repo sealed --key \"$key\" --name worker.valley.invalid/attestations --state \"$state\"" unit.script)
    || !(lib.hasInfix "git -C \"$origin\" remote add origin git@localhost:sealed.git\n" unit.script)
    || timer.timerConfig.OnActiveSec or null != "5m"
    || timer.timerConfig ? OnBootSec
    || timer.timerConfig.OnUnitInactiveSec or null != "1m"
    || !(builtins.elem "timers.target" timer.wantedBy);

  # The restacker. When disabled, it renders no units or user account on
  # integratorHost. When enabled, each configured project gets a
  # valley-restacker@<project>.service running valley-restacker run as the
  # valley-restacker user in its own group, with no access to the bare
  # repositories on disk. It connects through localhost sshd as its
  # principal using the key passed via LoadCredential, verifies the host's
  # own SSH key, has its 0700 state directory as its only persistent
  # writable path (its private temporary directories are writable too),
  # and reads the floor from the instance repository over the same SSH
  # connection.
  restackerRenderedWithoutEnable =
    integratorHost.config.systemd.services ? "valley-restacker@"
    || integratorHost.config.systemd.targets ? valley-restackers
    || integratorHost.config.users.users ? valley-restacker;

  restackerMisrendered =
    let
      unit = restackerHost.config.systemd.services."valley-restacker@";
      service = unit.serviceConfig;
      account = restackerHost.config.users.users.valley-restacker;
      target = restackerHost.config.systemd.targets.valley-restackers;
      command = service.ExecStart;
    in
    service.User or null != "valley-restacker"
    || service.Group or null != "valley-restacker"
    || account.group != "valley-restacker"
    || account.extraGroups != [ ]
    || service.LoadCredential or [ ] != [ "key:/run/agenix/valley-restacker-key" ]
    || service.StateDirectory or null != "valley-restacker/%i"
    || service.StateDirectoryMode or null != "0700"
    || service.ProtectSystem or null != "strict"
    || service ? ReadWritePaths
    || !(lib.hasInfix " -i %d/key " unit.environment.GIT_SSH_COMMAND)
    || !(lib.hasInfix "-o StrictHostKeyChecking=yes -o HostKeyAlias=localhost" unit.environment.GIT_SSH_COMMAND)
    || !(lib.hasInfix "-o UserKnownHostsFile=%S/valley-restacker/%i/known_hosts -o GlobalKnownHostsFile=/dev/null" unit.environment.GIT_SSH_COMMAND)
    || !(lib.hasInfix "/bin/valley-restacker run --repo git@localhost:%i.git --state %S/valley-restacker/%i --principal valley-restacker --interval 15s " command)
    || !(lib.hasInfix " --instance-repo git@localhost:sealed.git" command)
    || target.wants != [ "valley-restacker@guarded.service" ]
    || !(builtins.elem "multi-user.target" target.wantedBy);

  # The web publisher (nix/web-publisher.nix). Off, it renders nothing.
  # On, one pass is a oneshot on a five-minute timer whose first pass waits
  # five minutes after the timer starts, and which no switch starts or
  # restarts. It runs as a dynamic user in the git group, with an empty
  # safe.directory entry that resets the system's and the user's, then an
  # exact entry for each repository the host serves and no wildcard, the
  # repositories read-only, nothing writable but its state and cache, and
  # the mirror, backup and signing keys out of reach. Its memory is capped
  # with no swap. It reads the instance repository the controllers read,
  # names the host as the instance, and is given the receiver, the
  # destination, the display mapping and the bounds on its bundles and its
  # storage.
  webPublisherRenderedWithoutEnable =
    webPublisherOffHost.config.systemd.services ? valley-web-publish
    || webPublisherOffHost.config.systemd.timers ? valley-web-publish;

  webPublisherMisrendered =
    let
      unit = webPublisherHost.config.systemd.services.valley-web-publish;
      service = unit.serviceConfig;
      env = unit.environment;
      timer = webPublisherHost.config.systemd.timers.valley-web-publish;
      repos = [
        "/srv/git/guarded.git"
        "/srv/git/open.git"
        "/srv/git/released.git"
        "/srv/git/sealed.git"
      ];
      entries = lib.genList (i: {
        key = env."GIT_CONFIG_KEY_${toString i}" or null;
        value = env."GIT_CONFIG_VALUE_${toString i}" or null;
      }) (lib.length (lib.filter (lib.hasPrefix "GIT_CONFIG_KEY_") (lib.attrNames env)));
      command = service.ExecStart;
    in
    service.Type or null != "oneshot"
    || service.DynamicUser or null != true
    || service ? User
    || service.SupplementaryGroups or [ ] != [ "git" ]
    || env.GIT_CONFIG_COUNT or null != toString (lib.length repos + 1)
    || entries != map (r: {
      key = "safe.directory";
      value = r;
    }) ([ "" ] ++ repos)
    || lib.any (e: lib.hasInfix "*" e.value) entries
    || service.ProtectSystem or null != "strict"
    || service ? ReadWritePaths
    || service.ReadOnlyPaths or [ ] != map (r: "-${r}") repos
    || service.StateDirectory or null != "valley-web-publish"
    || service.CacheDirectory or null != "valley-web-publish"
    || env.XDG_CACHE_HOME or null != "/var/cache/valley-web-publish"
    || !(lib.all (p: builtins.elem p service.InaccessiblePaths) [
      "-/srv/git/.ssh"
      "-/run/agenix/valley-integrator-key"
      "-/run/agenix/valley-git-ssh-key"
      "-/var/lib/valley-integrator"
    ])
    || !(lib.hasInfix "/bin/valley-web-publish --instance-repo /srv/git/sealed.git --instance valley-host --destination example.org --receiver https://example.org --receiver-token %d/receiver-token --principals /nix/store/" command)
    || service.LoadCredential or [ ] != [ "receiver-token:/run/keys/valley-web-receiver-token" ]
    || !(lib.hasInfix "-valley-web-principals.json --state /var/lib/valley-web-publish --max-bundles 1G --max-storage 16G --registry-ref refs/heads/main --registry-dir identity" command)
    || service.MemoryMax or null != "1G"
    || service.MemorySwapMax or null != "0"
    || unit.restartIfChanged != false
    || unit.wantedBy != [ ]
    || timer.timerConfig.OnActiveSec or null != "5min"
    || timer.timerConfig.OnUnitInactiveSec or null != "5min"
    || !(builtins.elem "timers.target" timer.wantedBy);

  # The web receiver (nix/web-receiver.nix). Off, it renders nothing. On,
  # the receiver and valley-web are two units, each started at boot and
  # restarted when it fails, neither requiring the other. The receiver runs
  # as valley-web-receive in the valley-web group, owns the state directory
  # at mode 0750, and is given the instance, the destination, the pinned
  # verifiers from the store, its token as a credential from a file outside
  # the store, and the caps on bodies. valley-web runs as valley-web in the
  # same group, after the receiver, with the state directory read-only and
  # nothing writable, and is given the state directory and its own
  # listener. Each has its own memory cap with no swap.
  webReceiverRenderedWithoutEnable =
    webReceiverOffHost.config.systemd.services ? valley-web-receive
    || webReceiverOffHost.config.systemd.services ? valley-web
    || webReceiverOffHost.config.users.users ? valley-web-receive;

  webReceiverMisrendered =
    let
      cfg = webReceiverHost.config;
      receive = cfg.systemd.services.valley-web-receive;
      web = cfg.systemd.services.valley-web;
      confined =
        unit:
        unit.serviceConfig.ProtectSystem or null == "strict"
        && unit.serviceConfig.NoNewPrivileges or null == true
        && unit.serviceConfig.CapabilityBoundingSet or null == ""
        && unit.serviceConfig.MemorySwapMax or null == "0"
        && unit.serviceConfig.Restart or null == "on-failure"
        && builtins.elem "multi-user.target" unit.wantedBy
        && unit.requires == [ ]
        && unit.bindsTo == [ ];
    in
    !(confined receive && confined web)
    || cfg.users.users.valley-web-receive.group != "valley-web"
    || cfg.users.users.valley-web.group != "valley-web"
    || receive.serviceConfig.User or null != "valley-web-receive"
    || receive.serviceConfig.Group or null != "valley-web"
    || receive.serviceConfig.StateDirectory or null != "valley-web"
    || receive.serviceConfig.StateDirectoryMode or null != "0750"
    || receive.serviceConfig.LoadCredential or [ ] != [ "token:/run/keys/valley-web-receiver-token" ]
    || receive.serviceConfig.MemoryMax or null != "2G"
    || !(lib.hasInfix "/bin/valley-web-receive --state /var/lib/valley-web --listen 192.0.2.10:8081 --instance valley-host --destination example.org --verifiers /nix/store/" receive.serviceConfig.ExecStart)
    || !(lib.hasSuffix "-valley-web-verifiers --token-file %d/token --max-body 1G --max-upload 2G" receive.serviceConfig.ExecStart)
    || web.serviceConfig.User or null != "valley-web"
    || web.serviceConfig.Group or null != "valley-web"
    || web.serviceConfig ? StateDirectory
    || web.serviceConfig ? ReadWritePaths
    || web.serviceConfig ? LoadCredential
    || web.serviceConfig.ReadOnlyPaths or [ ] != [ "-/var/lib/valley-web" ]
    || web.serviceConfig.MemoryMax or null != "1G"
    || !(builtins.elem "valley-web-receive.service" web.after)
    || !(lib.hasSuffix "/bin/valley-web --state /var/lib/valley-web --listen 127.0.0.1:8080" web.serviceConfig.ExecStart);
in
{
  module-eval =
    if failedAssertions != [ ] then
      throw "valley module-eval: failed assertions: ${
        lib.concatMapStringsSep "; " (a: a.message) failedAssertions
      }"
    else if resticRenderedWithoutDeclaration then
      throw "valley module-eval: restic machinery rendered for a declaration without a backup block"
    else if busRenderedWithoutEnable then
      throw "valley module-eval: bus machinery rendered without services.valley.bus.enable"
    else if
      !(lib.any (a: lib.hasInfix "services.valley.backup." a.message) missingSecretAssertions)
    then
      throw "valley module-eval: enabling backup without the secret-path options must fail an assertion naming them"
    else if unrefusedHosts != [ ] then
      throw "valley module-eval: ${lib.concatMapStringsSep "; " (r: "a host was not refused although ${r.why}") unrefusedHosts}"
    else if protectionRenderedWithoutDeclaration then
      throw "valley module-eval: principal machinery rendered for a declaration with no protection block and no tagged key"
    else if policyMissingWithoutDeclaration then
      throw "valley module-eval: a project with no protection block got no pre-receive hook — the push policy is installed on every project a host serves"
    else if protectedHost.config.services.openssh.settings ? PermitUserEnvironment then
      throw "valley module-eval: sshd must pass no key's environment on: the git user's shell derives the principal from the authenticated key"
    else if
      !(lib.hasInfix "valley-git-shell" protectedHost.config.users.users.git.shell)
    then
      throw "valley module-eval: the git user's login shell must be the valley's, which derives the principal and pauses pushes until init converges"
    else if integratorRenderedWithoutEnable then
      throw "valley module-eval: integrator machinery rendered for a protected declaration that never enabled the service"
    else if !(integratorHost.config.systemd.services ? "valley-integrator@") then
      throw "valley module-eval: the integrator is enabled and no controller unit was rendered"
    else if
      controllers != [
        "valley-integrator@guarded.service"
        "valley-integrator@released.service"
        "valley-integrator@sealed.service"
      ]
    then
      throw "valley module-eval: a controller runs for each protected project and no other, not ${lib.concatStringsSep ", " controllers}"
    else if
      publishers != [
        "valley-publish@guarded.path"
        "valley-publish@released.path"
        "valley-publish@sealed.path"
      ]
    then
      throw "valley module-eval: each controller's landings are published by a path unit of its own, not ${lib.concatStringsSep ", " publishers}"
    else if integratorHost.config.systemd.services."valley-publish@".serviceConfig.User != "git" then
      throw "valley module-eval: a landing is published as the git user, which holds the mirror credentials"
    else if integratorUser == integratorHost.config.services.valley.user then
      throw "valley module-eval: the integrator must not run as the git user — it has its own (bd-500adf7)"
    else if !(integratorHost.config.users.users ? ${integratorUser}) then
      throw "valley module-eval: the integrator runs as ${integratorUser}, which the module never declares"
    else if integratorHost.config.users.users.${integratorUser}.group != "git" then
      throw "valley module-eval: the integrator reaches the repositories through the git group, and holds no other grant"
    else if integratorSafeDirectory then
      throw "valley module-eval: a controller must carry safe.directory for the one repository it serves in its own environment — it does not own the repositories, and git refuses what it does not own"
    else if integratorFloor then
      throw "valley module-eval: a controller must be told which repository carries the floor, and must carry a safe.directory exception for it — the floor is read from that repository's integrated tip, and git refuses a repository its caller does not own"
    else if integratorScratch then
      throw "valley module-eval: a controller's TMPDIR must be its state directory and that directory must be named in ReadWritePaths — under ProtectSystem=strict a path the unit does not enumerate is one it cannot write, and a verdict needs a worktree on disk"
    else if identityRenderedWithoutEnable then
      throw "valley module-eval: registry-compilation machinery rendered for a host that never enabled it — nothing may change until a consumer opts in"
    else if identityCompiler then
      throw "valley module-eval: the compiler must run as the git user, write only its own 0755 state directory, read the registry from the instance repository, write the grants the pre-receive hook reads, and have its authorized_keys added to what sshd reads under the %u token — sshd refuses a keys file owned by anyone but root or the account it authorizes, and a fixed path in that global list would authorize every registry key for every account on the host"
    else if identityIntegrator then
      throw "valley module-eval: a controller under a compiled registry must read the compiled signers file, start after the compilation, and sign under the name the registry publishes its key under"
    else if signersRenderedWithoutIntegrator then
      throw "valley module-eval: a signers publication rendered for a host whose integrator is off — there is no transfer statement to verify"
    else if signersUnpublished then
      throw "valley module-eval: the signers publisher must publish the controllers' own signers file and signing name into the instance repository, as the git user, writing nothing else, after valley-init and the compilation, at boot and whenever the signers file changes"
    else if signerProjectsMisrendered then
      throw "valley module-eval: a controller is given a signer projects file exactly when the host restricts a signer to projects"
    else if identityKeepsDeclaredKeys then
      throw "valley module-eval: the declared authorized keys must stay authorized beside the compiled ones — a compiled file that replaced them would make a bad compilation a locked-out git user"
    else if workerRenderedWithoutEnable then
      throw "valley module-eval: check-worker machinery rendered for a host that never enabled it — nothing, the nix daemon's slice included, may change until a consumer opts in"
    else if buildSliceMisrendered then
      throw "valley module-eval: the build slice must put the nix daemon in valley-builds.slice at the quota and memory cap the host names, with no swap, and render nothing of the worker"
    else if workerMisrendered then
      throw "valley module-eval: the check worker must run as its own account outside the git group, on one CPU, with one derivation built at a time on 4 cores, in a slice that holds it and the nix daemon to 4 CPUs and 8 GiB with no swap and no device policy, reaching this host's projects through its sshd under the host's own key, on a timer whose first pass waits five minutes after the timer starts and whose later passes are counted from the end of each pass"
    else if restackerRenderedWithoutEnable then
      throw "valley module-eval: restacker machinery rendered for a host that never enabled it"
    else if restackerMisrendered then
      throw "valley module-eval: each restacker controller must run valley-restacker run for its project as its own account outside the git group, with its own 0700 state directory as the only persistent path it may write, pushing over this host's sshd as its principal with the key systemd hands it as a credential, checking the host's own key, and reading the floor from the instance repository"
    else if webPublisherRenderedWithoutEnable then
      throw "valley module-eval: web publisher machinery rendered for a host that imports its module and never enabled it"
    else if webPublisherMisrendered then
      throw "valley module-eval: the web publisher must be a oneshot on a five-minute timer whose first pass waits five minutes and which no switch starts, run as a dynamic user in the git group with an empty safe.directory entry and then one exact entry per served repository, the repositories read-only, only its state and cache writable, the host's keys inaccessible and its memory capped with no swap, reading the instance repository and naming the host, the receiver, its token as a credential, the destination, the display mapping, the bounds on its bundles and storage, and the identity registry's directory"
    else if webReceiverRenderedWithoutEnable then
      throw "valley module-eval: web receiver machinery rendered for a host that imports its module and never enabled it"
    else if webReceiverMisrendered then
      throw "valley module-eval: the web receiver and valley-web must be two confined units started at boot, restarted on failure and requiring nothing of each other, as two users of the valley-web group, the receiver owning the state directory at 0750 with its token as a credential, its pinned verifiers, its caps and its own memory cap, and valley-web after it with the state directory read-only, no credential and a memory cap of its own"
    else
      pkgs.runCommand "valley-module-eval" {
        initScript = host.config.systemd.services.valley-init.script;
        sshdConfig = host.config.services.openssh.extraConfig;
        resticService = host.config.systemd.units."restic-backups-valley.service".text;
        resticTimer = host.config.systemd.units."restic-backups-valley.timer".text;
        protectedInit = protectedHost.config.systemd.services.valley-init.script;
        protectedKeys = lib.concatStringsSep "\n" protectedKeyLines;
        protectedShell = protectedHost.config.users.users.git.shell;
        integratorUnit = integratorHost.config.systemd.units."valley-integrator@.service".text;
        integratorInit = integratorHost.config.systemd.services.valley-init.script;
        publishUnit = host.config.systemd.units."valley-publish@.service".text;
        identityInit = identityHost.config.systemd.services.valley-init.script;
        passAsFile = [
          "initScript"
          "sshdConfig"
          "resticService"
          "resticTimer"
          "protectedInit"
          "protectedKeys"
          "integratorUnit"
          "integratorInit"
          "publishUnit"
          "identityInit"
        ];
      } (builtins.readFile ./module-eval.sh);
}
