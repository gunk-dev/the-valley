---
type: decision
id: dcr-0de694f
status: decided
title: A Phase 2 attestation is a host-signed statement over a tree digest
created: 2026-08-02
source: design conversation, 2026-08-02
---

# The shape of a Phase 2 attestation

A Phase 2 attestation has five elements: a statement, a signer, an envelope, a place it is stored,
and how several of them compose. This node fixes all five, which is the whole of what
[roadmap.md, Phase 2](../../design/roadmap.md#phase-2--attestations-verification-mvp) needs in order
to be built. The layering it uses — statement, envelope, transparency — is the one set out in
[[ida-d2dc957]]
([ida-d2dc957-attestations-ride-on-a-transparent-leaf.md](../ideas/ida-d2dc957-attestations-ride-on-a-transparent-leaf.md)).

## 1. The statement

The **statement** is the document that makes the claim: a typed, versioned record asserting what
ran, over what, with what result. It is the thing that is signed, and it is self-contained —
everything a verifier needs to understand the claim is inside it.

Its **subject** is what the statement is about. The subject is identified by a **digest set**: a set
of named digests, any of which may identify the same object under a different naming scheme. A
content-addressed digest of the resulting tree is the primary member. A commit hash may accompany it
as an advisory member, useful for finding the change and never relied on for identity. The resulting
tree is a function of the base the change was produced against, so an attestation is bound to that
base — [[ida-cbcbb3c]]
([ida-cbcbb3c-attestations-bind-to-a-base.md](../ideas/ida-cbcbb3c-attestations-bind-to-a-base.md)).

Its **predicate type** is the versioned name of the kind of claim being made. The version is part of
the name, so a statement says which revision of a claim shape it was written against. The predicate
type carries the pure-versus-effectful distinction of
[verification.md](../../design/verification.md), which places that distinction inside the signed
bytes.

The statement records the check that ran and its result. For a pure check it additionally records
the digests of the check's inputs, of its derivation, and of its output, which is what a verifier
re-derives against.

The statement also carries, as content, the provenance of the run that produced the change: the
harness, the model, digests of the prompt and of the context, and the delegation chain as recorded.

## 2. The signer is the host

The host signs. The host is the party competent to assert that a computation ran, in a given
environment, with a given result.

An agent run holds no key of its own, per [[ida-a8243d2]]
([ida-a8243d2-agent-runs-act-under-delegated-authority.md](../ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md)).
An agent-authored change is attributed inside the host-signed statement, as part of the provenance
that statement carries — [[ida-45178f6]]
([ida-45178f6-agent-identity-is-provenance.md](../ideas/ida-45178f6-agent-identity-is-provenance.md)).

A human approval is a separate signed act carrying its own signature, per [[ida-b7025b5]]
([ida-b7025b5-human-decisions-are-signed-acts.md](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md)),
and is never the same signature as the host's.

## 3. The envelope

The **envelope** is how the statement is signed: the **signed note format**, in which a document is
the statement text, then a blank line, then one or more signature lines. Each signature line names
its signer and carries a raw Ed25519 signature over the text above it. The text is the statement in
its serialized form, and that serialization is fixed on its own. No git object is signed —
[[ida-51605e8]]
([ida-51605e8-authenticity-not-git-coupled.md](../ideas/ida-51605e8-authenticity-not-git-coupled.md)).

## 4. Storage

A git ref, keyed by the subject digest.

## 5. Composition

Several parties attesting to the same statement sign it as siblings: their signature lines sit side
by side under one text, in any number and any order, and each verifies on its own. A signature is
bound to the statement it was made over, because the text it covers is the text it sits under.

## Properties this shape holds

- An attestation survives the integrator's rebase, because its subject is a tree digest.
- A verifier can tell a re-derivable claim from a notarized one from the signed bytes alone.
- The format can evolve without invalidating what already exists, because the predicate type carries
  a version.
- A signature cannot be lifted onto claims it was not made about, because it covers the statement
  text it sits under.
- Where attestations are stored can change without invalidating them, because everything is
  referenced by digest.
- Provenance is derived from a complete record of the run, per [[ida-b42d112]]
  ([ida-b42d112-harness-log-is-normalized.md](../ideas/ida-b42d112-harness-log-is-normalized.md)).

## What Phase 2 defers

- **The transparency log and inclusion proofs**, and **witness re-verification** of pure claims.
  Both land in [roadmap.md, Phase 6](../../design/roadmap.md#phase-6--trust-backstop).
- **Keys held by agent runs.** Identity stays thin until a scenario forces it to grow, per
  [[ida-a8243d2]].
- **The actuator.** It is under exploration in [[ida-f1b39e8]]
  ([ida-f1b39e8-outbound-effects-pass-through-an-actuator.md](../ideas/ida-f1b39e8-outbound-effects-pass-through-an-actuator.md))
  and lands no earlier than
  [roadmap.md, Phase 5](../../design/roadmap.md#phase-5--effectful-reactions-armstrong-as-controller).
- **Verification of delegation chains**, as opposed to recording them. Phase 2 records a chain as
  content in the statement. Checking a chain belongs to the enforcement point, which is the
  integrator of [roadmap.md, Phase 3](../../design/roadmap.md#phase-3--the-integrator).

## Open

**The envelope a human approval travels in.** This decision fixes the envelope for host-signed
statements, and for those only. A signature line in the signed note format carries a raw Ed25519
signature, so the format cannot carry a signature from a hardware key that requires physical
presence — which is what [[ida-b7025b5]]
([ida-b7025b5-human-decisions-are-signed-acts.md](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md))
asks of a human approval. The approval's envelope is therefore not this one, and this decision does
not fix it.

## Related

- The three-layer decomposition and the sibling-signature rule: [[ida-d2dc957]]
  ([ida-d2dc957-attestations-ride-on-a-transparent-leaf.md](../ideas/ida-d2dc957-attestations-ride-on-a-transparent-leaf.md))
- What the subject may not be coupled to: [[ida-51605e8]]
  ([ida-51605e8-authenticity-not-git-coupled.md](../ideas/ida-51605e8-authenticity-not-git-coupled.md))
- The authority an agent run acts under: [[ida-a8243d2]]
  ([ida-a8243d2-agent-runs-act-under-delegated-authority.md](../ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md))
- The provenance the statement carries: [[ida-45178f6]]
  ([ida-45178f6-agent-identity-is-provenance.md](../ideas/ida-45178f6-agent-identity-is-provenance.md))
  and [[ida-b42d112]]
  ([ida-b42d112-harness-log-is-normalized.md](../ideas/ida-b42d112-harness-log-is-normalized.md))
- The separate act a human approval is: [[ida-b7025b5]]
  ([ida-b7025b5-human-decisions-are-signed-acts.md](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md))
- The base an attestation is bound to: [[ida-cbcbb3c]]
  ([ida-cbcbb3c-attestations-bind-to-a-base.md](../ideas/ida-cbcbb3c-attestations-bind-to-a-base.md))
- The two kinds of check the predicate type distinguishes:
  [verification.md](../../design/verification.md)
- The protocol the helper implements: [contribute.md](../../design/contribute.md)
