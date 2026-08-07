# The attestation helper: the written form as a fixed contract, and the
# helper driven end to end over scratch repositories.
{ pkgs, packages, ... }:
{
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
  # Every check there uses the command runner. The nix runner cannot
  # be exercised from inside a nix build — there is no daemon in the
  # sandbox — so what a pure statement looks like is pinned by
  # attest-schema and by the unit tests over the digest scheme, and
  # the runner itself is exercised by running the helper outside the
  # sandbox.
  attest-e2e = pkgs.runCommand "valley-attest-e2e" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.openssh
      packages.attest
    ];
  } (builtins.readFile ./attest-e2e.sh);
}
