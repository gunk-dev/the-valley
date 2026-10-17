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
#   attest.nix       attest-unit, attest-conformance, attest-e2e — the
#                    helper's unit tests, the written form as a fixed
#                    contract, and the helper driven end to end
#   integrator.nix   integrator-unit, integrator-e2e — the verdict rules,
#                    and the controller over real repositories
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
#                    own-policy, valley-request, valley-go-unit,
#                    valley-status, valley-ready, valley-changes,
#                    valley-knot, valley-plan, valley-darwin-eval —
#                    bin/valley, its Go verbs, the-valley's own policy, and
#                    the CLI's package evaluated for Darwin
#   ssh-e2e.nix      ssh-e2e — the push boundary over a real sshd in a
#                    NixOS machine, and the request grant's handoff
#   publish-e2e.nix  publish-e2e — an integrator landing reaching the push
#                    mirror and the bus, in a NixOS machine running the
#                    real controller
#
# Fixtures live with the thing they are fixtures of: host declarations,
# attestations, events, graphs and policies under examples/, attestation
# vectors under attest/, the integrator's scenarios under integrator/e2e/.
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
      system
      ;
  };
  args = {
    inherit
      pkgs
      lib
      self
      system
      packages
      hosts
      ;
  };
in
import ./checks/host-module.nix args
// import ./checks/schema.nix args
// import ./checks/knowledge.nix args
// import ./checks/knot.nix args
// import ./checks/attest.nix args
// import ./checks/integrator.nix args
// import ./checks/hooks.nix args
// import ./checks/identity.nix args
// import ./checks/sigverify.nix args
// import ./checks/cli.nix args
// import ./checks/ssh-e2e.nix args
// import ./checks/publish-e2e.nix args
