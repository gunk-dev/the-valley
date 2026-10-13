---
type: idea
id: ida-53ec742
status: superseded
title: "Outbound authority is addressed by the actuator proposal"
created: 2026-07-25
source: design conversation, 2026-07-25
---

# Outbound authority is addressed by the actuator proposal

[[ida-f1b39e8]](ida-f1b39e8-outbound-effects-pass-through-an-actuator.md) replaces this node with a
proposal for mediating an agent's actions on external systems. This node is superseded.

The retained concern is that process isolation and external authorization solve different problems.
A run with valid credentials may perform an unwanted external action without escaping its sandbox.
The action needs a permission check at the service or mediator that can refuse it.

The actuator proposal carries the full reasoning, including local isolation, credential exposure,
audit records, and the limits of physical-presence approval.
