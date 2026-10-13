---
type: idea
id: ida-a5214da
status: adopted
title: "Choose formats for the-valley's needs and specify them for independent implementation"
created: 2026-08-02
source: design conversation, 2026-08-02
---

# Choose formats for the-valley's needs and specify them for independent implementation

The-valley can adopt an existing format when it fits and define a format when the project needs
something different. Either choice must leave a clear contract that another implementation can
follow.

The CUE host and event schemas are examples. They describe the domain independently of the NixOS
installer that consumes them.

The attestation design in [[dcr-0de694f]](../decisions/dcr-0de694f-phase2-attestation-shape.md)
similarly defines project-specific claims while borrowing useful ideas such as digest sets and
versioned predicate types. Reusing an encoding alone would not make an external tool understand
those claims.

[[ida-b9f646c]](ida-b9f646c-nix-backend-not-substrate.md) applies the same requirement to
implementation backends: the behavior must remain understandable outside the reference
implementation.

## Interoperability

Federation gives compatibility a concrete purpose. Valleys exchanging attestations and events need
to agree on their meaning and representation, as described in
[[ida-8482624]](ida-8482624-federation-groups.md).

The [open-source constraint](../../design/requirements.md#constraints) also requires practical
implementability. Publishing source code is insufficient if the only way to reproduce the format is
to depend on that implementation.

## Open questions

Which formats should be adopted, and where is a project-specific format justified? What common
contracts will independently operated valleys require at their boundaries?
