# Recording changes to the system itself

This is a proposed requirement, not a settled invariant. It asks whether every lasting change to
the-valley or its outputs should have an inspectable record, including changes to the components and
rules that govern the system.

## Proposed requirement

No actor should be able to make a lasting change to the system or its outputs without leaving the
required record of that change.

The proposal includes the system's own policy, controllers, and configuration. A change to the
integrator should be attributable and reviewable. A change to the policy governing integration
should identify both the previous rule and the authority for changing it.

The motivation is practical. If the system can explain application changes but cannot explain
changes to its own behavior, part of the causal history is missing. The-valley's ability to improve
itself depends on retaining that history.

The words “lasting change,” “output,” and “required record” still need precise definitions. A
temporary adjustment that automatically returns to declared state may need different treatment from
a persistent configuration change.

## Cases the requirement would cover

- **Integrator changes.** The code deciding whether changes can land must itself have an authorized
  path for updates.
- **Policy changes.** The rules admitting a policy amendment must come from already accepted
  governing state.
- **Enforced principles.** If a project principle constrains integration, its meaning, enforcement,
  and amendment history must be inspectable.
- **Configuration changes.** Changes to protected refs, accepted signers, trust thresholds, and
  witnesses must be governed and recorded.

Representing principles or configuration as graph nodes is one proposal. The current
[node schema](../.the-valley/README.md#files-and-metadata) does not implement those node types. The
requirement concerns how changes are governed and recorded; it does not yet choose their storage
format.

## What has been decided

The
[policy adoption decision](../.the-valley/decisions/dcr-9c75283-substrate-adoption-and-the-core.md)
requires explicit adoption by a valley and physical approval for policy changes. The approval
mechanism must be available before the core is activated.

Ordinary amendments use accepted state to judge the next state. The
[bootstrap description](./architecture.md#bootstrap-and-recovery) identifies initial manual setup
and recovery through a lower administrative layer. These decisions address parts of the problem
without establishing the complete proposed requirement.

Host root access remains outside the valley's normal enforcement boundaries. The documentation must
not imply that repository policy prevents an administrator from changing the machine.

## Questions still to resolve

- Which changes need durable records, and what must those records contain?
- What establishes the first trusted state, and how is that setup recorded?
- How are emergency repairs authorized and recorded when normal integration is unavailable?
- Which out-of-band changes can be prevented, which can be detected, and which remain a stated trust
  assumption?
- What happens when a change takes effect but writing its evidence fails?
- Which temporary or low-impact changes can use a lighter process while preserving the required
  explanation?

The same questions apply when a change crosses a valley boundary. Each receiving valley needs to
know what it is accepting and under whose authority.

This proposal is related to the
[outcome-based work model](./architecture.md#work-from-outcomes-and-dependencies): work on the
system belongs in the same model as work produced by it. It remains open in
[the question index](./openquestions.md#identity-approval-and-bootstrap).
