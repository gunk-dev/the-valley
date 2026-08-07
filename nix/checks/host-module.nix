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
    ;

  failedAssertions = builtins.filter (a: !a.assertion) (
    host.config.assertions
    ++ noBackupHost.config.assertions
    ++ busHost.config.assertions
    ++ mirrorHost.config.assertions
    ++ protectedHost.config.assertions
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
    else
      pkgs.runCommand "valley-module-eval" {
        initScript = host.config.systemd.services.valley-init.script;
        sshdConfig = host.config.services.openssh.extraConfig;
        resticService = host.config.systemd.units."restic-backups-valley.service".text;
        resticTimer = host.config.systemd.units."restic-backups-valley.timer".text;
        protectedInit = protectedHost.config.systemd.services.valley-init.script;
        protectedKeys = lib.concatStringsSep "\n" protectedKeyLines;
        passAsFile = [
          "initScript"
          "sshdConfig"
          "resticService"
          "resticTimer"
          "protectedInit"
          "protectedKeys"
        ];
      } (builtins.readFile ./module-eval.sh);
}
