{
  pkgs,
  lib,
  packages,
  hosts,
  ...
}:
let
  # Throwaway keys, made once at build time: the contributor's, which signs
  # evidence, and the integrator's, which signs what it lands. They hold no
  # authority anywhere. known_signers is the one verifier file the rendered
  # hook, the integrator and the restacker all read, under the names each
  # signs as.
  keys =
    pkgs.runCommand "valley-restack-e2e-keys"
      {
        nativeBuildInputs = [
          pkgs.openssh
          packages.attest
        ];
      }
      ''
        mkdir $out
        for who in contributor integrator; do
          ssh-keygen -q -t ed25519 -N "" -C "$who" -f "$out/$who"
        done
        attest key --key "$out/contributor" --name contributor.valley.invalid/attestations > "$out/known_signers"
        attest key --key "$out/integrator" --name integrator.valley.invalid/attestations >> "$out/known_signers"
      '';

  # The protected host, with its hook checking evidence against those keys
  # rather than a file under /var/lib that no sandbox holds. Its grants are
  # the protected host's: requester holds request and restack, and
  # restacker holds restack alone.
  host = hosts.protectedHost.extendModules {
    modules = [
      { services.valley.integrator.knownSignersFile = lib.mkForce "${keys}/known_signers"; }
    ];
  };
  initScript = host.config.systemd.services.valley-init.script;

  # The transport the end-to-end tests reach origin through: a stand-in for
  # ssh that serves pushes and fetches with the real receive-pack and
  # upload-pack, and holds or cuts one where a test chooses
  # (restack/e2e/transport). Go, standard library only.
  transport = pkgs.buildGoModule {
    pname = "valley-restack-transport";
    version = "0";
    src = ../../restack/e2e/transport;
    vendorHash = null;
    postInstall = ''
      mv $out/bin/transport $out/bin/valley-restack-transport
    '';
  };
in
{
  # The restacker's own tests (restack/), run in its checkPhase: how a
  # request relates to a family, which outcomes are current and which
  # release a family, the arithmetic of each capacity limit, and how the
  # policy's cap is counted, one larger than any int included. Its scratch
  # repository commits as its flags say with no identity in the
  # environment, a pass locks its state directory, the directory belongs to
  # one repository, target and principal, and a cue or an ssh that never
  # returns is killed at the deadline. Given the fixture host's rendered
  # hook, the restack append is pushed through it as the restacker
  # principal: the hook takes the active title and refuses a forged one,
  # and takes a report from the restacker and not from the change's owner.
  restack-unit = packages.restack-unwrapped.overrideAttrs {
    inherit initScript;
    passAsFile = [ "initScript" ];
    preCheck = ''
      VALLEY_RESTACK_TEST_HOOK="$(grep -o '/nix/store/[^ ]*-valley-protect-open' "$initScriptPath" | head -n1)"
      test -x "$VALLEY_RESTACK_TEST_HOOK"
      export VALLEY_RESTACK_TEST_HOOK
    '';
  };

  # valley-restacker once --report-only over a bare repository wired with
  # the fixture host's rendered hook, every change readied by its owner
  # through the repository's own valley. Ready changes of two owners are
  # planned; a linear stack is one family, and so are two stacks on a
  # shared lower layer. A draft, a withdrawn change, a lone plain branch, a
  # change landed by fast-forward and an uncertain landing are left alone,
  # and a family with a plain branch or a draft in it is held. A change
  # with two versions uploaded by the restacker principal is held by the
  # project's cap of 2. A request by name with no outcome holds; a current
  # stale and a current reject release, the reject noted; an outcome about
  # another head, tip or evidence, and one that does not read, hold.
  # Requests by exact head and by range hold, the range's head fetched by
  # the planner, and a request whose head is no commit holds every family.
  # A cap larger than any int composes and is no practical cap, so the
  # change the cap of 2 held is planned to restack. Every ref on origin is
  # compared before and after each pass. Outcomes are injected on the
  # server side, since no push may write one.
  #
  # Then the fixtures the publishing slice's tests stand on, each proved
  # by a small test. The restacker principal pushes through the transport
  # and cannot file a request, which requester can. A barrier holds a push
  # before origin sees it and releases it, and one held there loses its
  # lease to a push made while it waits; a barrier holds a push origin has
  # taken; a dropped answer leaves origin holding a push its client
  # reports as failed. A second pass on a state directory fails while a
  # barrier holds the first, and a pass for another principal is refused
  # it. In a repository served with the integrator's evidence fixtures
  # (integrator/e2e: the nix stand-in and a policy of pure checks), the
  # real integrator lands one change and rejects another whose signed
  # evidence lies about its kind. The planner, reading the shared verifier
  # keys, sees the landed change submitted, which it reads as uncertain
  # without them, and releases the rejected change's family on the real
  # outcome, noting the reject.
  restack-e2e = pkgs.runCommand "valley-restack-e2e" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.jq
      pkgs.cue
      pkgs.openssh
      packages.attest
      packages.integrator
      packages.valley-script
      packages.valley-restacker
      transport
    ];
    inherit initScript keys;
    passAsFile = [ "initScript" ];
    smokePolicy = ../../examples/policy/smoke;
    integratorE2e = ../../integrator/e2e;
    SCHEMA_ATTESTATION = ../../schema/attestation.cue;
    SCHEMA_VERIFICATION = ../../schema/verification.cue;
    SCHEMA_EVENTS = ../../schema/events.cue;
  } (builtins.readFile ./restack-e2e.sh);
}
