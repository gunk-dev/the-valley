---
type: idea
id: ida-5a59fef
status: exploring
title: "Reuse common components for feedback and improvement loops"
created: 2026-08-05
source: design conversation, 2026-08-05
---

# Reuse common components for feedback and improvement loops

The system's feedback loops could share signal collection, durable records, derived views, declared
policy, and controllers. Reusing these components would make new loops easier to implement and
inspect.

## Common structure

A loop observes domain events, metrics, and logs under the contracts in
[[dcr-62ecc36]](../decisions/dcr-62ecc36-signal-contracts.md). It retains useful findings as durable
events or knowledge nodes. Derived views summarize that history for a decision.

Declared policy determines what actions are allowed or required. A controller reconciles current
state and acts through the relevant enforcement mechanism.
[[ida-f1b39e8]](ida-f1b39e8-outbound-effects-pass-through-an-actuator.md) explores an actuator for
external actions.

The target is for a new loop to need mainly a policy document and a controller. A loop requiring
additional shared machinery would test whether this proposed decomposition is sufficient.

## Improving a loop

A controller could propose changes to its own policy. Those changes would use the normal integration
path and be evaluated under the rules already in force.

This makes the proposed improvement visible and reviewable. It does not let the controller authorize
a relaxation of its own permissions.
[[dcr-9c75283]](../decisions/dcr-9c75283-substrate-adoption-and-the-core.md) and
[[ida-b7025b5]](ida-b7025b5-human-decisions-are-signed-acts.md) define the approval requirements for
policy and delegation.

The same approach applies to system configuration in
[self-transparency.md](../../design/self-transparency.md).

## Candidate uses

- Adjusting agent task size from observed results:
  [[ida-a006b02]](ida-a006b02-ooda-over-neural-nodes.md).
- Detecting operational problems in the-valley itself:
  [[ida-f16bd96]](ida-f16bd96-the-system-observes-itself.md).
- Tuning log retention from the value and cost of stored data:
  [[dcr-62ecc36]](../decisions/dcr-62ecc36-signal-contracts.md).
- Updating trust decisions from independent verification results:
  [verification.md](../../design/verification.md).

Manual practices already supply examples: incidents become bug nodes, a discovered error leads to a
corrected requirement, and experience changes how work is divided. Automation could gradually
replace repeatable parts of those practices, following
[[ida-b48bded]](ida-b48bded-production-dags-and-events.md).

## Open questions

Which loop should be automated first? What delegated authority does a controller need to propose
changes to a particular policy? How should loop health be monitored without requiring an endless
chain of supervising loops?
