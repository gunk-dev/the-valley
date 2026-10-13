---
type: idea
id: ida-f1b39e8
status: exploring
title: "Mediate agent actions on external systems through an actuator"
created: 2026-07-25
source: design conversation, 2026-07-25
supersedes: [ida-53ec742]
---

# Mediate agent actions on external systems through an actuator

An agent run could request external actions from a service called an **actuator**. The actuator
would hold the credentials, check the run's delegated permissions, perform permitted operations, and
record their results. The run would receive the response without receiving the credential.

This is an exploratory design for controlling changes to external systems and making those changes
auditable.

## Why the external action needs its own check

A run can perform an unwanted action using valid credentials without escaping its sandbox. For
example, a proposing agent with approval credentials can approve its own work through the intended
API.

Many workspace edits can be discarded before publication. An integrated change can immediately
trigger deployment and further actions. Permission checks must account for those consequences at the
point that can refuse the external action.

Local isolation remains necessary, especially on workstations holding private keys and other
sensitive data. It addresses what the run can access locally. It does not revoke authority carried
by credentials the run can use elsewhere.

## Permissions and credential handling

The actuator enforces the delegation bundle described in
[[ida-a8243d2]](ida-a8243d2-agent-runs-act-under-delegated-authority.md). A general proxy that
injects credentials into arbitrary requests would not provide the required control.

The operation vocabulary must be explicit enough for policy to constrain it and for a reader to
understand the resulting audit records. Hiding a credential while honoring every request would leave
the same authority available to the run.

Credential non-disclosure also matters because an agent's observations may be stored, summarized,
replayed, and copied into later artifacts. A credential revealed in tool output can spread through
that trajectory.

Scoped, short-lived credentials can complement this approach. The actuator could obtain and use them
without exposing them to the run.

## Audit records

Credential mediation and recording are separate capabilities. The proposal combines them so that all
actions within the mediated scope can have a record.

That completeness depends on preventing bypass and defining failure behavior around recording. If
intent is recorded before execution, the record can describe an action that never happened. If
execution occurs first, a recording failure can leave an action unrecorded. The design needs to
represent that uncertainty.

Cryptographic transparency is a further property. It helps detect a log operator changing entries or
showing different histories to different readers. A complete operational record does not
automatically require or provide that protection.

## Human approval

[[ida-b7025b5]](ida-b7025b5-human-decisions-are-signed-acts.md) requires physical presence for
particular approvals. Properly enforced hardware presence prevents a process from silently producing
such an approval on its own.

The system still needs to bind that gesture to the intended action and present the action
accurately. Host compromise does not make those surrounding questions disappear.

## Open questions

- Which external systems and actions require mediation?
- Are permissions declared before a run, checked as actions are requested, or both?
- Which actions can be adequately controlled with scoped credentials alone?
- What isolation can be assumed on contributor-owned machines?
- How are intent, attempted execution, observed result, and uncertain outcome recorded?
- Which reads require recording, and what latency is acceptable?
- How does the operation vocabulary grow without becoming an unrestricted forwarding API?
- How much logic can the actuator contain while remaining small enough to review?
- Can it constrain data disclosure, or only credential use and named actions?

The integrator already mediates one important action: landing a change.
[verification.md](../../design/verification.md) distinguishes pure and effectful checks, and
[Phase 5](../../design/roadmap.md#phase-5--effectful-reactions-armstrong-as-controller) covers
effectful reactions.
