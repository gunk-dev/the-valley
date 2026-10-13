---
type: idea
id: ida-7638082
status: raw
title: "Preserve pre-integration verification when migrating a project"
created: 2026-07-31
source: cosmo readiness analysis, 2026-07-31
---

# Preserve pre-integration verification when migrating a project

Moving a repository off GitHub must preserve the checks required before its protected branch
changes. Hosting Git refs and running checks after a mirror push do not provide that guarantee.

The-valley now has attestation and integration mechanisms. Each migrating project still needs the
right policy, trusted check definitions, execution capacity, and verified enforcement.
[[oc-87deec8]](../outcomes/oc-87deec8-valley-can-host-cosmo.md) tracks this work for cosmo.

## Cosmo's existing checks

Cosmo's GitHub CI runs formatting, `nix flake check`, dry-run evaluation of three NixOS
configurations, and dry-run evaluation of four Home Manager configurations. A separate workflow runs
zizmor against workflow definitions.

The NixOS matrix covers classic-laddie, makers-nix, and johnny-walker. Weller and klaus-worker-0 are
outside that matrix. A dry run establishes that derivations can be evaluated and dependencies
resolved; it does not establish that the configurations build successfully.

Migration planning must distinguish this existing gate from the stronger requirement to build all
five machines.

Cosmo also defines local pre-commit checks for formatting, private-key detection, and zizmor. Local
execution is useful, but integration needs evidence that the required checks ran on the relevant
tree.

## Timing matters

GitHub Actions may still run after the primary publishes to its mirror. Such checks see history that
has already landed and may already be deployed by convergence.

They can report a problem, but cannot prevent that landing. The migration must put the required
checks before the protected-ref update.

## Required integration path

[[dcr-f41f718]](../decisions/dcr-f41f718-declared-verification-policy.md) defines how policy selects
the required checks. [[dcr-0de694f]](../decisions/dcr-0de694f-phase2-attestation-shape.md) defines
signed evidence of their execution. The integrator must validate that evidence and refuse the target
update when it is missing or invalid.

The receive boundary must also prevent another writer from bypassing the integrator. All parts are
needed for the policy to hold in practice.

[[ida-1ec03b1]](ida-1ec03b1-path-scoped-verification-policy.md) explains path-based selection, and
[[bd-eaefe82]](../bugs/bd-eaefe82-check-definitions-come-from-the-branch.md) addresses which check
definitions can be trusted.

## Open question

Can a particular project tolerate a deliberately ungated migration interval? That requires an
explicit decision based on what its changes can affect. The answer for a small documentation project
does not establish an acceptable answer for a repository that configures and deploys several
machines.
