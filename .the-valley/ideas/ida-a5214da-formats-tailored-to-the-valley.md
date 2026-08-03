---
type: idea
id: ida-a5214da
status: adopted
title: Formats and interfaces are tailored to the-valley
created: 2026-08-02
source: design conversation, 2026-08-02
---

# Formats and interfaces are tailored to the-valley

The formats and interfaces this design defines are shaped for its own use. Where an external format
fits, it is adopted; where it does not, the design defines its own rather than contorting itself to
fit an ecosystem it does not otherwise participate in.

## Where this already holds

- The host declaration ([`schema/valley.cue`](../../schema/valley.cue)) and the event vocabulary
  ([`schema/events.cue`](../../schema/events.cue)) are CUE documents that are deliberately not Nix.
  Installers are their consumers: the NixOS module in this repo is one, and another installer reads
  the same files.
- The attestation vocabulary of [[dcr-0de694f]]
  ([decisions/dcr-0de694f-phase2-attestation-shape.md](../decisions/dcr-0de694f-phase2-attestation-shape.md))
  is the project's own. Its predicate types and digest schemes are named for this project, so no
  external tool would read one of its statements regardless of encoding. Its statement borrows the
  digest-set and versioned-predicate ideas from an external line of specifications while taking none
  of its wire format.
- [[ida-b9f646c]]
  ([ida-b9f646c-nix-backend-not-substrate.md](./ida-b9f646c-nix-backend-not-substrate.md)) holds the
  same shape one level down: an implementation is a backend, never the contract.

## The boundary

A tailored format is not a closed one. Two things bound it.

Federation crosses group boundaries. [[ida-8482624]]
([ida-8482624-federation-groups.md](./ida-8482624-federation-groups.md)) holds that a group — the
organization that owns a set of repos and shares one trust domain — may import another group's
attestations and decide by its own policy how much they count, and it leaves federated identity
mapping open. Whatever crosses that boundary is understood by two parties who did not agree in
advance, so the boundary is where an external format earns its place.

The _Open source_ constraint of [requirements.md](../../design/requirements.md#constraints) holds
that the substrate must be inspectable and forkable. A format nobody outside can implement from its
specification would satisfy the letter of that and defeat it in practice. So a tailored format
carries an obligation: it is specified well enough to be implemented independently.

## Open

- Where the line falls between a format the valley defines and one it adopts.
- What a federated boundary requires, which is undesigned.
