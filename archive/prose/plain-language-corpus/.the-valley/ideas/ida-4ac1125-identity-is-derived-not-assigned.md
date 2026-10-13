---
type: idea
id: ida-4ac1125
status: exploring
title: "Separate stable identity from storage location and implementation"
created: 2026-07-31
source: design conversation, 2026-07-31
---

# Separate stable identity from storage location and implementation

Several parts of the design identify an object through its contents or recorded inputs, while
treating its location as something that can change. This node collects those related ideas. It does
not establish a single rule that applies to all of them.

## Examples

- [[ida-93e4f91]](ida-93e4f91-changes-not-branches.md) seeks a change identity that survives
  rebasing and moving between branches.
- [[ida-51605e8]](ida-51605e8-authenticity-not-git-coupled.md) makes a signed statement refer to its
  subject's contents independently of Git storage.
- [[ida-45178f6]](ida-45178f6-agent-identity-is-provenance.md) identifies an agent run through its
  inputs and execution provenance.
- An attestation or trajectory referenced by digest could move between repository refs and another
  content-addressed store without changing what the reference identifies.
- [[ida-b9f646c]](ida-b9f646c-nix-backend-not-substrate.md) separates a component's contract from
  its implementation backend.
- [[ida-48c8868]](ida-48c8868-stores-beyond-git.md) separates durability requirements from the store
  holding project state.

The last two examples concern stable contracts more than object names. They may be related
principles rather than instances of exactly the same one.

## Benefit and limits

A reference based on contents can remain meaningful after the bytes move. This allows some storage
decisions to be deferred without making every consumer depend on the initial path. Migration still
needs to preserve availability and access.

A run's input record has a different meaning from a digest of its output. The same recorded inputs
may produce different results, especially with hosted models. The distinction resembles the
difference between identifying a build derivation and identifying the resulting contents.

## Open questions

Is this one general property or several related ones? When should identity come from content, from
the producing process, or from an assigned name? Which parts of the design need a stable human name
even when their contents change?
