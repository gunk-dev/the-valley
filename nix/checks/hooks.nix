# The hooks the module renders, run against real git. Each check follows the
# store paths out of a rendered valley-init script, so what it drives is what
# a host would execute — never a copy of it written for the test.
{
  pkgs,
  packages,
  hosts,
  ...
}:
{
  # What a mirror ends up holding, end to end and unmocked: a bare
  # repo wired with the real rendered hooks, pushed to for real, and
  # a second bare repo standing in for the mirror. The refspec is
  # easy to get subtly wrong — --prune ignores the non-glob heads
  # refspec, so pruning heads is the sweep's job — and only running
  # git can tell. Relative mirror URLs (mirrorHost) resolve against
  # the pushing repo, so the shipped scripts run with no
  # substitution: this is exactly what a host executes.
  mirror-e2e = pkgs.runCommand "valley-mirror-e2e" {
    nativeBuildInputs = [ pkgs.git ];
    initScript = hosts.mirrorHost.config.systemd.services.valley-init.script;
    passAsFile = [ "initScript" ];
  } (builtins.readFile ./mirror-e2e.sh);

  # The one structural invariant, driven for real: bare repos wired
  # with the rendered pre-receive hook, pushed to as each principal.
  # What sshd would set from a key's authorized_keys entry, these
  # pushes set in the environment directly — that rendering is pinned
  # by module-eval, and everything the hook decides after it is what
  # this check exercises.
  protect-e2e = pkgs.runCommand "valley-protect-e2e" {
    nativeBuildInputs = [ pkgs.git ];
    initScript = hosts.protectedHost.config.systemd.services.valley-init.script;
    passAsFile = [ "initScript" ];
  } (builtins.readFile ./protect-e2e.sh);

  # Phase 1's exit criteria, end to end and unmocked: a push to a
  # bare repo wired with the real rendered hooks produces a
  # ref-updated event on a real JetStream server within seconds,
  # visible in `valley tail`; replaying the repo's refs is
  # deterministic; and a dead bus never fails a push. The hook and
  # stream-init scripts are followed from the rendered unit scripts,
  # so the check exercises exactly what a host would run — only the
  # server invocation differs (the sandbox cannot write /srv), same
  # binary and flags, relocated storage.
  bus-e2e = pkgs.runCommand "valley-bus-e2e" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.natscli
      pkgs.nats-server
      pkgs.jq
      pkgs.cue
      packages.valley-script
    ];
    initScript = hosts.busHost.config.systemd.services.valley-init.script;
    busInitScript = hosts.busHost.config.systemd.services.valley-bus-init.script;
    eventSchema = ../../schema/events.cue;
    passAsFile = [
      "initScript"
      "busInitScript"
    ];
  } (builtins.readFile ./bus-e2e.sh);
}
