# The identity registry compiler, driven end to end. What it renders is
# checkable against something already written down: the known-signers file
# the instance maintains by hand today is exactly what the registry must
# compile to, so the golden case is a byte-for-byte comparison against the
# artifact this compiler exists to replace. The grants it compiles are then
# read by the pre-receive hook a host renders: the approve grant, also
# accepted as request, and restack, whose holder adds a version to another
# principal's ready change and writes its restack report. A registry naming
# the approve grant under either name compiles the same grants, and the
# hook reads a grants file naming either: a holder under either name, or
# both, files a request and updates and deletes another principal's change
# record, and a principal holding neither, including one named approve,
# does none of that. A grant named approve at a boundary of another kind
# compiles to nothing. The registry is read past a replacement ref planted
# to forge it, and from the commit its ref resolved to while a push moves
# that ref mid-read. It is read at its ref by that exact name: with
# refs/heads/registry missing, a branch named refs/heads/refs/heads/registry
# is no registry, and the compilation fails. A registry enrolling a real YubiKey key from the test
# vectors, with the attestation the device wrote, compiles it into both
# approval signers files; that attestation offered for another key, or
# altered, fails the compilation, and so does the example registry, whose
# attestation chains to a test-only root. A registry holding a grant or a
# key under a field spelled another way, beside the field the schema
# spells, fails the compilation, and so does an export holding a key twice.
#
# The schema's own rejections live with the other schemas, in schema.nix.
{
  pkgs,
  packages,
  hosts,
  ...
}:
{
  # The compiler's Go tests: the key hash pinned against the reference vector attest is held to, expiry,
  # the renders it must refuse, and the approval files, over a real
  # YubiKey's attestation and signature.
  identity-unit = packages.identity-unwrapped.unit;

  identity-e2e = pkgs.runCommand "valley-identity-e2e" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.openssh
      packages.identity
      # attest, so the compiled verifier line is checked against the
      # implementation that reads it and not only against a fixed vector.
      packages.attest
      # cue, for the unwrapped compiler the mid-read push races.
      pkgs.cue
    ];
    # The compiler without its wrapper, so a git placed first on PATH is
    # the one it runs, and the schema the wrapper would name.
    identityUnwrapped = "${packages.identity-unwrapped}/bin/identity";
    identitySchema = ../../schema/identity.cue;
    registry = ../../examples/identity/registry;
    compiled = ../../examples/identity/compiled;
    expired = ../../examples/identity/expired;
    attested = ../../examples/identity/attested;
    # The test vectors: the-valley operator's own approval key and its
    # YubiKey's certificates, the compiled files they make, and the
    # example's synthetic chain (identity/testdata/README.md).
    testdata = ../../identity/testdata;
    # The fail-safe case. Any invalid registry would do; this one fails on a
    # property of the floor rather than on a typo, so the check also
    # observes that a floor violation is what stops a compilation.
    invalid = ../../examples/identity/rejected/external-governs-registry.cue;
    # A host compiling the registry, whose rendered pre-receive hook reads
    # the compiled grants. The check follows the hook out of its init
    # script, so what it drives is what such a host would run.
    identityInitScript = hosts.identityHost.config.systemd.services.valley-init.script;
    passAsFile = [ "identityInitScript" ];
  } (builtins.readFile ./identity-e2e.sh);
}
