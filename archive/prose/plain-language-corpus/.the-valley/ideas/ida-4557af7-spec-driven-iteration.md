---
type: idea
id: ida-4557af7
status: exploring
title: "Develop specifications and conformance checks as components mature"
created: 2026-07-15
source: design conversation, 2026-07-15 — captured imperfect on purpose ("even if it's imperfect, it's there to be polished")
---

# Develop specifications and conformance checks as components mature

As each component matures, the project could maintain a precise specification and conformance suite
from which implementations can be developed or replaced. Prototyping would help discover the
contract; the contract would then preserve the behavior that matters.

The host configuration already demonstrates part of this approach.
[schema/valley.cue](../../schema/valley.cue) defines the data model, installers consume it, and CUE
validation checks conformance. [[ida-b9f646c]](ida-b9f646c-nix-backend-not-substrate.md) requires
the model to remain implementable outside Nix.

The same approach could cover event formats, attestation claims, and integrator invariants. These
need specifications suited to their behavior, not necessarily the same notation used for data
shapes.

Agent-assisted implementation makes this worth exploring. If replacing an implementation becomes
cheaper, more effort can go into the specification and the tests that distinguish a correct
replacement. This extends the existing practice of deriving requirements from scenarios and the
roadmap from requirements, recorded in
[[oc-49555c7]](../outcomes/oc-49555c7-requirements-from-ladder.md) and
[[oc-2fbcd7b]](../outcomes/oc-2fbcd7b-roadmap-rederived.md).

Specifications should develop component by component when the relevant scenario becomes a priority.
The proposal does not require a complete system specification before useful software can be built.

## Open questions

- Which forms can specify behavior and invariants beyond CUE data shapes?
- Where should conformance suites live, and how should their results gate integration?
- How should specification changes be reviewed and authorized?
- When does a specification change invalidate pending implementation work or its evidence?

[[ida-93e4f91]](ida-93e4f91-changes-not-branches.md) provides the change model relevant to that last
question.
