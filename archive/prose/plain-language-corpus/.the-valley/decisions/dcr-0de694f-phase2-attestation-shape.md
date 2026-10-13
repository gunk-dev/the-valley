---
type: decision
id: dcr-0de694f
status: decided
title: "Attestations contain a signed statement about a checked tree"
created: 2026-08-02
source: design conversation, 2026-08-02
---

# Attestations contain a signed statement about a checked tree

An attestation records what check ran, the tree it checked, and the result. The machine that ran the
check signs the statement. This decision defines the statement, signature envelope, storage, and
support for multiple signers used in
[Phase 2](../../design/roadmap.md#phase-2--attestations-verification-mvp).

## Statement

The statement is a typed, versioned document. It contains the information needed to interpret the
claim without consulting the machine that produced it.

Its subject is identified by a set of named digests. The primary digest identifies the contents of
the checked tree. An optional commit hash helps locate that tree in Git; it does not determine the
statement's identity. Because applying a change to a different base may produce a different tree,
evidence is specific to the checked result.
[[ida-cbcbb3c]](../ideas/ida-cbcbb3c-attestations-bind-to-a-base.md) explains this relationship.

The predicate type identifies the kind and version of the claim. It includes whether the check was
pure or effectful, as defined in [verification.md](../../design/verification.md). That distinction
is covered by the signature.

Every statement records the check and its result. A pure-check statement also records digests for
the inputs, derivation, and output so another machine can reproduce the computation.

Run provenance is statement content: the harness, model, prompt and context digests, and recorded
delegation chain. A complete harness record supplies this information; see
[[ida-b42d112]](../ideas/ida-b42d112-harness-log-is-normalized.md).

## Signer

The execution host signs the claim that a computation ran in a particular environment and produced a
particular result.

An agent run holds no signing key of its own. Its contribution is attributed through provenance in
the host-signed statement, as described in
[[ida-a8243d2]](../ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md) and
[[ida-45178f6]](../ideas/ida-45178f6-agent-identity-is-provenance.md).

A human approval is a separate signed act. The host's signature does not establish human approval;
see [[ida-b7025b5]](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md).

## Envelope and storage

A signed note contains the serialized statement, a blank line, and one or more signature lines. Each
line names a signer and carries a raw Ed25519 signature over the statement text.

Multiple parties can sign the same text. Their signatures appear as siblings, in any order, and each
verifies independently. A signature cannot authenticate additional text contributed by another
signer.

Git stores the note under a ref keyed by the subject digest. The signature covers the statement
text, so moving the note to another store does not invalidate it.
[[dcr-de9d996]](dcr-de9d996-statement-text-and-signed-note.md) specifies the serialization and
envelope in detail.

## Reuse and evolution

A statement remains valid when a commit is re-parented without changing its tree. When the tree
changes, the transfer rules in
[[dcr-439b771]](dcr-439b771-integration-occ-over-content-addressed-evidence.md) determine which
check results remain usable. A tree digest alone does not make evidence valid after every rebase.

Versioned predicate types allow new statement formats without changing the meaning of existing
signatures. Digest-based references allow storage to change independently of authentication. These
properties follow the separation of statement, envelope, and transparency described in
[[ida-d2dc957]](../ideas/ida-d2dc957-attestations-ride-on-a-transparent-leaf.md) and
[[ida-51605e8]](../ideas/ida-51605e8-authenticity-not-git-coupled.md).

## Deferred work

[Phase 6](../../design/roadmap.md#phase-6--trust-backstop) covers transparency-log inclusion proofs
and independent re-verification of pure claims.

Agent-held keys require a scenario that needs them. The outbound actuator remains under exploration
in [[ida-f1b39e8]](../ideas/ida-f1b39e8-outbound-effects-pass-through-an-actuator.md) and is
associated with
[Phase 5](../../design/roadmap.md#phase-5--effectful-reactions-armstrong-as-controller).

Phase 2 records delegation chains. Checking whether those chains authorize an action belongs at the
enforcement point, including the
[Phase 3 integrator](../../design/roadmap.md#phase-3--the-integrator).

The envelope for human approval remains open. This decision's envelope carries raw Ed25519
signatures. It does not define how to represent the hardware-backed, physical-presence approval
required by [[ida-b7025b5]](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md).

The contribution workflow is described in [contribute.md](../../design/contribute.md).
