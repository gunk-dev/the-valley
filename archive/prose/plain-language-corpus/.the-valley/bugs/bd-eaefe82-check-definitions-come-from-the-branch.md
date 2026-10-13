---
type: bug
id: bd-eaefe82
status: open
title: "A submitted branch can redefine a required check"
created: 2026-08-02
source: design review of dcr-f41f718, 2026-08-02
---

# A submitted branch can redefine a required check

The integrator reads required check names from governing policy, but evaluates the named derivations
from the candidate tree. A branch can therefore replace a required check with one that does no
useful work and truthfully attest that it passed.

This is a live gap between choosing the required checks and controlling what those checks compute.
[[dcr-f41f718]](../decisions/dcr-f41f718-declared-verification-policy.md) protects the policy
source; it does not, by itself, protect every implementation named by that policy.

## How it applies to supplied checks

A project may import the valley's check through a flake input, as proposed in
[[ida-a9e274c]](../ideas/ida-a9e274c-mandated-checks-come-from-the-instance.md). The branch also
contains the lock file for that input. It could redirect the dependency to a modified
implementation.

A valley-mandated check must therefore resolve from the valley's own governing source. The desired
rule is recorded in
[[dcr-439b771]](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md), but
the candidate-tree evaluation path still needs to enforce it.

## Check changes must remain possible

The project also needs to add tests, repair broken checks, and demonstrate proposed changes to
verification code.

Always running only the old definitions would not test the new definitions. A resolution needs to
distinguish the trusted checks that admit a change from validation of the check changes it proposes.

## Candidate controls

Require human approval for changes to check definitions and their dependencies, including flakes,
lock files, policy, and helper code. The path-class and approval mechanisms are described in
[[ida-1ec03b1]](../ideas/ida-1ec03b1-path-scoped-verification-policy.md) and
[[ida-b7025b5]](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md).

Resolve valley-mandated check implementations independently of the submitted tree. Candidate
definitions can still be evaluated as proposed work, without replacing the governing test.

A deployment could explicitly accept limited exposure while the mechanism is incomplete. Background
reexecution alone is insufficient: rerunning the same weakened no-op check may reproduce its passing
result.

## Completion condition

Resolve and verify the check-source boundary before relying on it to enforce compliance across
projects or to replace cosmo's existing build gate. Tests must show that a submitted branch cannot
weaken a mandatory check by changing its definition or input pin, while legitimate check changes
remain testable.

[integration.md](../../design/integration.md) describes the current enforcement path.
