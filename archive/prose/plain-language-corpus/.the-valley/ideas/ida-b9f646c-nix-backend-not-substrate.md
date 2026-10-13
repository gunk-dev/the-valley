---
type: idea
id: ida-b9f646c
status: adopted
title: "Keep schemas portable while using Nix as the reference implementation"
created: 2026-07-06
source: conversation, 2026-07-06
---

# Keep schemas portable while using Nix as the reference implementation

The schemas and behavioral contracts should remain implementable on ordinary Linux systems. Nix
provides the reference implementation; additional installers and runners should be built when a
concrete deployment needs them.

The hosting module manages familiar components such as Unix users, SSH, bare repositories, and
hooks. Another installer can consume [schema/valley.cue](../../schema/valley.cue) and provide those
components.

Events and knowledge records also need no Nix-specific storage or transport.

## Verification

Verification needs particular care because the reference runner uses Nix derivations. The
attestation contract must still state which check ran, the subject, and the result in terms another
runner can represent.

Purity is a property claimed and enforced through a runner's execution model. Merely naming a runner
does not make arbitrary computation pure. [verification.md](../../design/verification.md) describes
the distinction.

A useful review question is whether a small program on a conventional Linux distribution could
produce or consume each schema field. Fields that only the reference implementation can supply need
justification.

## When to add a backend

A second runner is needed when a real check requires it. A non-NixOS installer is needed when a
deployment requires it.

Keeping the contract portable now should make those additions possible without prematurely
maintaining implementations that no one uses.

[[ida-1ec03b1]](ida-1ec03b1-path-scoped-verification-policy.md) describes how policy selects checks
independently of the contributor's execution environment.
