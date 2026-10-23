# The attestation helper: its unit tests, the written form as a fixed
# contract, and the helper driven end to end over scratch repositories.
{
  pkgs,
  packages,
  nixStandIns,
  ...
}:
{
  # The helper's Go tests, run in the package's checkPhase against real
  # git and ssh-keygen: the written form, the note's key hash and
  # signatures, and the tree digest and input manifest. The unwrapped
  # package, because its checkPhase is where the tests run.
  attest-unit = packages.attest-unwrapped;

  # The written form and the note envelope, held to fixed vectors.
  # These bytes are an interop contract rather than an
  # implementation detail: a regenerated or independently written
  # attest must produce them identically or every signature already
  # made stops verifying. So the vectors are checked-in files with a
  # README saying they are the contract (attest/conformance/), and
  # this check is what holds a second implementation to them.
  attest-conformance = pkgs.runCommand "valley-attest-conformance" {
    nativeBuildInputs = [
      pkgs.cue
      packages.attest
    ];
    vectorDir = ../../attest/conformance;
    schema = ../../schema/attestation.cue;
  } (builtins.readFile ./attest-conformance.sh);

  # The helper itself, driven end to end over scratch repositories
  # and a throwaway key: what it publishes, what it refuses to
  # publish, and what a verifier makes of both.
  #
  # A real nix cannot run inside a nix build — there is no daemon in
  # the sandbox — so the nix runner is driven through integrator-e2e's
  # stand-in for nix: the shared evaluation, a build per check, mixed
  # outcomes, an output two checks share, and what a run with a failed
  # check refuses to publish. What a pure statement looks like is
  # pinned by attest-schema and by the unit tests over the digest
  # scheme, and the runner against a real nix by attest's Go tests
  # outside the sandbox.
  attest-e2e = pkgs.runCommand "valley-attest-e2e" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.openssh
      packages.attest
    ];
    nixStandIn = nixStandIns;
  } (builtins.readFile ./attest-e2e.sh);
}
