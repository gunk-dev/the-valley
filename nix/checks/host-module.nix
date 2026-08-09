# What the NixOS module renders from a declaration, and what it must refuse.
# The refusals are eval-time: a host that must not evaluate is caught by a
# throw here, since there is nothing to build once it has. What a host does
# render is pinned by module-eval.sh over the rendered scripts.
{
  pkgs,
  lib,
  hosts,
  ...
}:
let
  inherit (hosts)
    host
    noBackupHost
    noSecretsHost
    protectedHost
    busHost
    mirrorHost
    integratorHost
    integratorSelfHost
    identityHost
    ;

  failedAssertions = builtins.filter (a: !a.assertion) (
    host.config.assertions
    ++ noBackupHost.config.assertions
    ++ busHost.config.assertions
    ++ mirrorHost.config.assertions
    ++ protectedHost.config.assertions
    ++ integratorHost.config.assertions
    ++ integratorSelfHost.config.assertions
    ++ identityHost.config.assertions
  );

  missingSecretAssertions = builtins.filter (a: !a.assertion) noSecretsHost.config.assertions;

  protectedKeyLines = protectedHost.config.users.users.git.openssh.authorizedKeys.keys;

  resticRenderedWithoutDeclaration =
    noBackupHost.config.systemd.services ? "restic-backups-valley"
    || noBackupHost.config.systemd.timers ? "restic-backups-valley";

  # The bus defaults off; a host that never asked for one must
  # render zero bus machinery.
  busRenderedWithoutEnable =
    noBackupHost.config.systemd.services ? valley-bus
    || noBackupHost.config.systemd.services ? valley-bus-init;

  # A declaration written before the field renders the host it always
  # did: no hook wired, no tag on a key, no sshd change. It still
  # carries the sweep that removes a hook it once had — the same
  # discipline the mirror and bus hooks keep, and the only way
  # protection can be turned off by editing a declaration.
  protectionRenderedWithoutDeclaration =
    noBackupHost.config.services.openssh.settings ? PermitUserEnvironment
    || lib.any (lib.hasInfix "environment=") (
      noBackupHost.config.users.users.git.openssh.authorizedKeys.keys
    )
    || lib.hasInfix "valley-protect-" noBackupHost.config.systemd.services.valley-init.script;

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

  controllers = integratorHost.config.systemd.targets.valley-integrators.wants;

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
    !(lib.hasInfix "--instance-repo /srv/git/open.git" command)
    || !(lib.hasInfix "--instance-repo /srv/git/guarded.git" selfCommand)
    || env.GIT_CONFIG_COUNT or null != "2"
    || env.GIT_CONFIG_KEY_1 or null != "safe.directory"
    || env.GIT_CONFIG_VALUE_1 or null != "/srv/git/open.git";

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
  # The compiled file is added to what sshd reads rather than replacing it,
  # and it carries the %u token: a fixed path in that global list would
  # authorize every registry key for every account on the host.
  identityCompiler =
    identityService.User or null != identityHost.config.services.valley.user
    || identityService.StateDirectoryMode or null != "0755"
    || identityService.ReadWritePaths or [ ] != [ identityStateDir ]
    || !(lib.hasInfix "--repo /srv/git/open.git" identityService.ExecStart)
    || !(lib.hasInfix "--known-signers ${identityStateDir}/known-signers" identityService.ExecStart)
    || !(lib.hasInfix "--authorized-keys ${identityStateDir}/authorized_keys.git" identityService.ExecStart)
    || !(builtins.elem "${identityStateDir}/authorized_keys.%u"
      identityHost.config.services.openssh.authorizedKeysFiles
    )
    || !(builtins.elem "%h/.ssh/authorized_keys" identityHost.config.services.openssh.authorizedKeysFiles);

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

  # The declared keys stay authorized beside the compiled ones. A compiled
  # file that replaced them would make a bad compilation — or a first boot
  # before the first one — a locked-out git user, which is the one failure
  # this machinery must never have.
  identityKeepsDeclaredKeys =
    identityHost.config.users.users.git.openssh.authorizedKeys.keys == [ ];
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
    else if protectionRenderedWithoutDeclaration then
      throw "valley module-eval: write-protection machinery rendered for a declaration with no protection block"
    else if
      protectedHost.config.services.openssh.settings.PermitUserEnvironment or null != "VALLEY_PRINCIPAL"
    then
      throw "valley module-eval: a key naming a principal needs sshd to honour that one variable and no other"
    else if integratorRenderedWithoutEnable then
      throw "valley module-eval: integrator machinery rendered for a protected declaration that never enabled the service"
    else if !(integratorHost.config.systemd.services ? "valley-integrator@") then
      throw "valley module-eval: the integrator is enabled and no controller unit was rendered"
    else if
      controllers != [
        "valley-integrator@guarded.service"
        "valley-integrator@released.service"
      ]
    then
      throw "valley module-eval: a controller runs for each protected project and no other, not ${lib.concatStringsSep ", " controllers}"
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
      throw "valley module-eval: the compiler must run as the git user, write only its own 0755 state directory, read the registry from the instance repository, and have its authorized_keys added to what sshd reads under the %u token — sshd refuses a keys file owned by anyone but root or the account it authorizes, and a fixed path in that global list would authorize every registry key for every account on the host"
    else if identityIntegrator then
      throw "valley module-eval: a controller under a compiled registry must read the compiled signers file, start after the compilation, and sign under the name the registry publishes its key under"
    else if identityKeepsDeclaredKeys then
      throw "valley module-eval: the declared authorized keys must stay authorized beside the compiled ones — a compiled file that replaced them would make a bad compilation a locked-out git user"
    else
      pkgs.runCommand "valley-module-eval" {
        initScript = host.config.systemd.services.valley-init.script;
        sshdConfig = host.config.services.openssh.extraConfig;
        resticService = host.config.systemd.units."restic-backups-valley.service".text;
        resticTimer = host.config.systemd.units."restic-backups-valley.timer".text;
        protectedInit = protectedHost.config.systemd.services.valley-init.script;
        protectedKeys = lib.concatStringsSep "\n" protectedKeyLines;
        integratorUnit = integratorHost.config.systemd.units."valley-integrator@.service".text;
        integratorInit = integratorHost.config.systemd.services.valley-init.script;
        passAsFile = [
          "initScript"
          "sshdConfig"
          "resticService"
          "resticTimer"
          "protectedInit"
          "protectedKeys"
          "integratorUnit"
          "integratorInit"
        ];
      } (builtins.readFile ./module-eval.sh);
}
