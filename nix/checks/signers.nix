# The signers a client verifies an integrator's transfer statement with:
# published by the host into its instance repository, and read by a fresh
# client with nothing configured.
{
  pkgs,
  lib,
  packages,
  hosts,
  nixStandIns,
  ...
}:
let
  # Throwaway keys, made once at build time: the contributor's, which signs
  # evidence, and two of the integrator's, the second for after a rotation.
  # They hold no authority anywhere. The hook checks pushed evidence against
  # the contributor's key alone, since the integrator writes locally. The
  # integrator's lines are written when the test runs, under the name the
  # integrator defaults to on the machine it runs on.
  keys =
    pkgs.runCommand "valley-signers-e2e-keys"
      {
        nativeBuildInputs = [
          pkgs.openssh
          packages.attest
        ];
      }
      ''
        mkdir $out
        for who in contributor integrator rotated; do
          ssh-keygen -q -t ed25519 -N "" -C "$who" -f "$out/$who"
        done
        attest key --key "$out/contributor" --name contributor.valley.invalid/attestations > "$out/hook_signers"
      '';

  # The protected host's rendered hook, checking evidence against that key.
  host = hosts.protectedHost.extendModules {
    modules = [
      { services.valley.integrator.knownSignersFile = lib.mkForce "${keys}/hook_signers"; }
    ];
  };

  # The host that restricts a worker's signer to projects, and the same host
  # once its list has changed.
  publisherOf = h: h.config.systemd.services.valley-signers.serviceConfig.ExecStart;
  changedHost = hosts.restrictedHost.extendModules {
    modules = [
      {
        services.valley.integrator.signerProjects = lib.mkForce {
          "worker.valley.invalid/attestations" = [ "sealed" ];
        };
      }
    ];
  };
in
{
  # The real integrator lands a change by a commit that is no fast-forward,
  # so only its transfer statement shows the landing. The publisher the
  # module renders (integratorHost's, which sets no signing name) writes
  # the controller's signers file and its default name into the instance
  # repository, and a fresh clone with neither VALLEY_KNOWN_SIGNERS nor
  # VALLEY_INTEGRATOR_SIGNER set reads the landing as landed-recorded,
  # through valley changes --all, valley status, and valley rebase, which
  # drops the landed change's commits from a stack above it. Before the
  # publication the same landing reads uncertain and every other landing
  # reads as it does after. A flag beats a variable and a variable beats
  # the publication; an override that does not read, or holds no key
  # under the name, is reported. No push can replace or delete the
  # publication. The publisher refuses a signer projects list that
  # restricts the name it publishes; a client given one anyway, one that
  # does not read, or an entry that is not a file, verifies no transfer
  # statement, and one naming only a worker changes nothing. After the integrator's key is rotated, a landing signed
  # with the new key reads uncertain until the publisher runs again, and
  # landed-recorded after. A run that read the file before the rotation,
  # and is still running when the host's run starts, makes that run wait
  # for it, so the old keys are not published last. Once the registry
  # revokes every signing key, the compiler's file with no key is
  # published, and both merged landings read uncertain. A host that
  # restricts signers to projects publishes its list beside the signers,
  # a changed list is published again, a list that does not read publishes
  # nothing, and a host that restricts nobody publishes no list.
  signers-e2e = pkgs.runCommand "valley-signers-e2e" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.jq
      pkgs.cue
      pkgs.openssh
      packages.attest
      packages.identity
      packages.integrator
      packages.valley-script
      packages.valley-go
    ];
    inherit keys;
    initScript = host.config.systemd.services.valley-init.script;
    publishCommand = publisherOf hosts.integratorHost;
    restrictedPublishCommand = publisherOf hosts.restrictedHost;
    changedPublishCommand = publisherOf changedHost;
    passAsFile = [ "initScript" ];
    integratorE2e = nixStandIns;
    SCHEMA_ATTESTATION = ../../schema/attestation.cue;
    SCHEMA_VERIFICATION = ../../schema/verification.cue;
    SCHEMA_EVENTS = ../../schema/events.cue;
  } (builtins.readFile ./signers-e2e.sh);
}
