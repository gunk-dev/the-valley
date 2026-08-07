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
    ;

  failedAssertions = builtins.filter (a: !a.assertion) (
    host.config.assertions
    ++ noBackupHost.config.assertions
    ++ busHost.config.assertions
    ++ mirrorHost.config.assertions
    ++ protectedHost.config.assertions
    ++ integratorHost.config.assertions
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

  integratorSafeDirectory =
    integratorGitEnv.GIT_CONFIG_COUNT or null != "1"
    || integratorGitEnv.GIT_CONFIG_KEY_0 or null != "safe.directory"
    || integratorGitEnv.GIT_CONFIG_VALUE_0 or null != "/srv/git/%i.git";

  integratorService = integratorHost.config.systemd.services."valley-integrator@".serviceConfig;

  # Scratch goes under the state directory, because the sandbox leaves no
  # writable temporary directory and a verdict needs a checkout on disk. Two
  # halves, and each was a live failure on its own. TMPDIR naming a path
  # outside the state directory is the first; the state directory not being
  # named in ReadWritePaths is the second, which failed even though
  # ProtectSystem=strict is documented to leave a declared state directory
  # writable. Everything the unit may write is enumerated, so what a reader
  # of the unit sees is the whole of it.
  integratorScratch =
    integratorGitEnv.TMPDIR or null != "/var/lib/${integratorService.StateDirectory}"
    || !(builtins.elem "/var/lib/${integratorService.StateDirectory}" integratorService.ReadWritePaths);
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
    else if integratorScratch then
      throw "valley module-eval: a controller's TMPDIR must be its state directory and that directory must be named in ReadWritePaths — under ProtectSystem=strict a path the unit does not enumerate is one it cannot write, and a verdict needs a worktree on disk"
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
