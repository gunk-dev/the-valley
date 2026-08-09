# The identity registry compiler, driven end to end. What it renders is
# checkable against something already written down: the known-signers file
# the instance maintains by hand today is exactly what the registry must
# compile to, so the golden case is a byte-for-byte comparison against the
# artifact this compiler exists to replace.
#
# The schema's own rejections live with the other schemas, in schema.nix.
{ pkgs, packages, ... }:
{
  identity-e2e = pkgs.runCommand "valley-identity-e2e" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.openssh
      packages.identity
      # attest, so the compiled verifier line is checked against the
      # implementation that reads it and not only against a fixed vector.
      packages.attest
    ];
    registry = ../../examples/identity/registry;
    compiled = ../../examples/identity/compiled;
    expired = ../../examples/identity/expired;
    # The fail-safe case. Any invalid registry would do; this one fails on a
    # property of the floor rather than on a typo, so the check also
    # observes that a floor violation is what stops a compilation.
    invalid = ../../examples/identity/rejected/external-governs-registry.cue;
  } (builtins.readFile ./identity-e2e.sh);
}
