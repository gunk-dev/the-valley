# The integrator: its verdict rules, and the controller over real
# repositories.
{ pkgs, packages, ... }:
{
  # The integrator's verdict, case by case, with no repository in
  # sight. Every rule of dcr-439b771 is a function of data, so the
  # unit tests state each one directly; what a repository does to
  # produce those inputs is integrator-e2e's.
  integrator-unit = packages.integrator-unwrapped;

  # The integrator itself, driven end to end over scratch bare
  # repositories and throwaway keys, running the real binaries: the
  # helper produces the evidence, the deriver composes the policy,
  # the controller judges and lands. The scenarios are Phase 3's
  # exit criteria, observed rather than asserted about, and they live
  # with the integrator (integrator/e2e/) rather than here.
  #
  # Only the nix evaluator is stood in for — a nix build has no
  # daemon in the sandbox, the same wall attest-e2e meets — and the
  # stand-in models the one property the pure rule rests on: a
  # check's input closure is the content of the files it reads.
  # Everything downstream of it is real: attest builds the manifest
  # and the digest, and the integrator compares them.
  integrator-e2e = pkgs.runCommand "valley-integrator-e2e" {
    nativeBuildInputs = [
      pkgs.bash
      pkgs.git
      pkgs.openssh
      pkgs.coreutils
      pkgs.findutils
      pkgs.gnused
      pkgs.gnugrep
      pkgs.cue
      packages.attest
      packages.valley-script
      packages.integrator
    ];
    e2eDir = ../../integrator/e2e;
    SCHEMA_ATTESTATION = ../../schema/attestation.cue;
    SCHEMA_VERIFICATION = ../../schema/verification.cue;
    SCHEMA_EVENTS = ../../schema/events.cue;
    # No bus in a sandbox: the payloads are still composed and
    # vetted against the vocabulary, and printed rather than
    # published. What publishing does is the post-receive hook's
    # one `nats pub`, which this shares.
  } (builtins.readFile ./integrator-e2e.sh);
}
