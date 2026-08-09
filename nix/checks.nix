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
#   knowledge.nix    knowledge-lint and its consumer and rejection forms,
#                    prose-format — the graph and the prose
#   attest.nix       attest-conformance, attest-e2e — the written form as a
#                    fixed contract, and the helper driven end to end
#   integrator.nix   integrator-unit, integrator-e2e — the verdict rules,
#                    and the controller over real repositories
#   hooks.nix        mirror-e2e, bus-e2e, protect-e2e, init-e2e — the hooks
#                    the module renders, run against real git, and the init
#                    script that wires them
#   identity.nix     identity-e2e — the registry compiler over a real
#                    instance repository, against the hand-written
#                    artifacts it replaces
#   cli.nix          valley-cli, review-notes, policy-deriver — bin/valley
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
// import ./checks/attest.nix args
// import ./checks/integrator.nix args
// import ./checks/hooks.nix args
// import ./checks/identity.nix args
// import ./checks/cli.nix args
