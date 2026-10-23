# What is tested, and where each test's logic lives.
#
# One file per subject, each returning the checks for it. A check's shell
# body is never here: it is a script under checks/, and a check definition
# is only what builds it, what it is given, and why it exists.
#
#   hosts.nix        the eval-only host declarations the module checks share
#   host-module.nix  module-eval — what the NixOS module renders from a
#                    declaration, and what it must refuse
#   schema.nix       cue-vet, attest-schema, identity-schema — the schemas
#                    against the examples they accept and the ones they
#                    must reject
#   knowledge.nix    knowledge-lint-consumer, knowledge-lint-rejects,
#                    prose-format — the lint's reusable and rejection
#                    forms, and the prose
#   knot.nix         knot-unit, knot-agree — the plan graph's module, and
#                    its validator held to the schema and the lint
#   delta.nix        delta-unit — valley-delta-v1, its conformance vectors,
#                    and the trees it reads and refuses
#   derive.nix       derive-unit — the Go deriver of required checks, held
#                    to bin/valley's
#   review.nix       review-unit — review records, their binding to the
#                    version judged, and their append-only history
#   attest.nix       attest-unit, attest-conformance, attest-e2e — the
#                    helper's unit tests, the written form as a fixed
#                    contract, and the helper driven end to end
#   integrator.nix   integrator-unit, integrator-e2e — the verdict rules,
#                    and the controller over real repositories
#   restack.nix      restack-unit, restack-e2e — the restacker's planner
#                    over a repository wired with the rendered hook
#   signers.nix      signers-e2e — the signers the host publishes, read by
#                    a fresh client to verify the real integrator's landing
#   hooks.nix        valleyhook-unit, note-unit, change-unit, mirror-e2e,
#                    bus-e2e, protect-e2e, knots-e2e, init-e2e — the hooks
#                    the module renders, run against real git, the formats
#                    they read, and the init script that wires them
#   identity.nix     identity-unit, identity-e2e — the compiler's unit
#                    tests, and the registry compiler over a real
#                    instance repository, against the hand-written
#                    artifacts it replaces, and the hook reading its grants
#   sigverify.nix    sigverify-unit, sigverify-e2e — the security-key
#                    signature verifier against real ssh-keygen, real git
#                    and OpenSSH's software authenticator
#   cli.nix          valley-cli, review-notes, policy-deriver,
#                    own-policy, valley-request, rebase-unit,
#                    valley-go-unit, valley-status, valley-ready,
#                    valley-changes, valley-knot, valley-plan,
#                    selfupdate-unit, valley-self-update,
#                    valley-darwin-eval — bin/valley, its Go verbs, the
#                    rebase engine, self-update and its home-manager
#                    module, the-valley's own policy, and the CLI's
#                    package evaluated for Darwin
#   ssh-e2e.nix      ssh-e2e — the push boundary over a real sshd in a
#                    NixOS machine, and the request grant's handoff
#   publish-e2e.nix  publish-e2e — an integrator landing reaching the push
#                    mirror and the bus, in a NixOS machine running the
#                    real controller
#   worker-e2e.nix   worker-e2e — the check worker on its timer in a NixOS
#                    machine: its key, its enrollment, its evidence on
#                    origin, and a runaway build stopped by the caps
#
# Fixtures live with the thing they are fixtures of: host declarations,
# attestations, events, graphs and policies under examples/, attestation
# vectors under attest/, delta vectors under delta/, the integrator's
# scenarios under integrator/e2e/.
#
# A check is given only the files it reads, so an edit to any other file
# leaves its derivation, and the evidence for it, unchanged. A source path
# names one file or directory, and lib.fileset narrows a directory to the
# part of it a check reads. The sandbox holds a check to what it is given:
# a file it reads and was not given fails it. nix/tools/drv-churn.sh
# measures which checks an edit changes.
{
  pkgs,
  lib,
  self,
  system,
  packages,
}:
let
  hosts = import ./checks/hosts.nix {
    inherit
      pkgs
      lib
      self
      ;
  };
  # The same hosts over stand-ins for the programs they run, for the checks
  # that read what the module renders and run none of the programs.
  stubbedHosts = import ./checks/hosts.nix {
    inherit
      pkgs
      lib
      self
      ;
    stubbed = true;
  };
  # The part of a directory a check reads: the paths given, each in its
  # place under root.
  partOf =
    root: paths:
    lib.fileset.toSource {
      inherit root;
      fileset = lib.fileset.unions paths;
    };
  # integrator-e2e's stand-ins for nix and nix-store, without its
  # scenarios. attest-e2e, valley-request, signers-e2e and restack-e2e run
  # through these two and read nothing else of integrator/e2e/.
  nixStandIns = partOf ../integrator/e2e [
    ../integrator/e2e/nix
    ../integrator/e2e/nix-store
  ];
  args = {
    inherit
      pkgs
      lib
      self
      system
      packages
      hosts
      stubbedHosts
      partOf
      nixStandIns
      ;
  };
in
import ./checks/host-module.nix args
// import ./checks/schema.nix args
// import ./checks/knowledge.nix args
// import ./checks/knot.nix args
// import ./checks/delta.nix args
// import ./checks/derive.nix args
// import ./checks/review.nix args
// import ./checks/attest.nix args
// import ./checks/integrator.nix args
// import ./checks/restack.nix args
// import ./checks/signers.nix args
// import ./checks/hooks.nix args
// import ./checks/identity.nix args
// import ./checks/sigverify.nix args
// import ./checks/cli.nix args
// import ./checks/ssh-e2e.nix args
// import ./checks/publish-e2e.nix args
// import ./checks/worker-e2e.nix args
