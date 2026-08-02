---
type: idea
id: ida-45178f6
status: exploring
title: Agent identity is agent provenance
created: 2026-07-31
source: design conversation, 2026-07-31
---

# Agent identity is agent provenance

An agent run's identity is its provenance. There is nothing else to name it by. This is the
counterpart of [[ida-a8243d2]]
([ida-a8243d2-agent-runs-act-under-delegated-authority.md](./ida-a8243d2-agent-runs-act-under-delegated-authority.md)):
that node says a run holds no key of its own, only a bundle of permissions delegated from the
initiating human. This one says the run holds no name of its own either. Both are readings of the
same chain — one asking what a run was permitted to do, the other asking what produced it.

The inputs to that provenance are the context, the prompt, the environment (the tools), the model,
and the chain of events leading back to the human identity that ultimately generated the run —
including the content that human provided in order to do so.

Recording those inputs makes them checkable. A policy can assert properties of the run itself: that
a model of a certain grade was used; that a specific model was not used; that the prompt did not
contain specified content. Provenance is therefore an input to verification policy — the path-scoped
required-check set of [[ida-1ec03b1]]
([ida-1ec03b1-path-scoped-verification-policy.md](./ida-1ec03b1-path-scoped-verification-policy.md))
— as much as it is the record that names a run.

This is acknowledged to be a hard thing to package.

The boundary to a SaaS language-model provider is impure, and that is accepted rather than solved. A
hosted model names a service, not a bit-exact artifact, and identical inputs do not reproduce
identical outputs — so unlike every other input in the list, it does not content-address.
Open-weight models would change this: the exact weights in use at the moment of an agent run could
be known, making the model input a digest like the others. The impurity is therefore a property of
today's provider boundary, not of the shape itself, in the same way that [[ida-b9f646c]]
([ida-b9f646c-nix-backend-not-substrate.md](./ida-b9f646c-nix-backend-not-substrate.md)) holds the
current implementation is not the contract. The pure-versus-effectful distinction in
[verification.md](../../design/verification.md) is the existing vocabulary for it.

That list of inputs is a candidate answer to the question [[ida-d2dc957]]
([ida-d2dc957-attestations-ride-on-a-transparent-leaf.md](./ida-d2dc957-attestations-ride-on-a-transparent-leaf.md))
leaves open — which ecosystem-specific context keys this project would define. That node's double
hashing is also what makes the large and private inputs, the prompt and the context, recordable: a
value can be committed to and authenticated by the signature while being withheld from the log.

Provenance is data, and unsigned data is a claim anyone can fabricate, so something holding a key
must assert it. The actuator of [[ida-f1b39e8]]
([ida-f1b39e8-outbound-effects-pass-through-an-actuator.md](./ida-f1b39e8-outbound-effects-pass-through-an-actuator.md))
is the candidate, because it already holds credentials the run does not. A run is therefore named by
a record that something else signs, and signs nothing itself — for a Phase 2 attestation, the signer
is the host and the run is attributed inside its statement, per [[dcr-0de694f]]
([dcr-0de694f-phase2-attestation-shape.md](../decisions/dcr-0de694f-phase2-attestation-shape.md)).

## Open

- Who signs the provenance, given that the run itself holds no key.
- How the chain is verified when the harness that ran the agent is also the software recording the
  citation — already open on [[ida-a8243d2]]
  ([ida-a8243d2-agent-runs-act-under-delegated-authority.md](./ida-a8243d2-agent-runs-act-under-delegated-authority.md)).
- Which context keys this project defines, per [[ida-d2dc957]]
  ([ida-d2dc957-attestations-ride-on-a-transparent-leaf.md](./ida-d2dc957-attestations-ride-on-a-transparent-leaf.md)).

## Related

- [[ida-a8243d2]]
  ([ida-a8243d2-agent-runs-act-under-delegated-authority.md](./ida-a8243d2-agent-runs-act-under-delegated-authority.md))
  — the same chain read for authority rather than for name.
- [[ida-d2dc957]]
  ([ida-d2dc957-attestations-ride-on-a-transparent-leaf.md](./ida-d2dc957-attestations-ride-on-a-transparent-leaf.md))
  — the carrier this provenance would ride in, and the context keys it asks for.
- [[ida-f1b39e8]]
  ([ida-f1b39e8-outbound-effects-pass-through-an-actuator.md](./ida-f1b39e8-outbound-effects-pass-through-an-actuator.md))
  — the candidate signer.
- [[ida-1ec03b1]]
  ([ida-1ec03b1-path-scoped-verification-policy.md](./ida-1ec03b1-path-scoped-verification-policy.md))
  — the policy these recorded inputs are checked by.
- [[ida-a763b3f]]
  ([ida-a763b3f-attestations-are-the-substrate-for-evaluation.md](./ida-a763b3f-attestations-are-the-substrate-for-evaluation.md))
  — the same record read as the definition of a repeatable experiment.
- [[ida-b9f646c]]
  ([ida-b9f646c-nix-backend-not-substrate.md](./ida-b9f646c-nix-backend-not-substrate.md)) — the
  precedent for treating today's model boundary as an implementation, not the contract.
- [verification.md](../../design/verification.md) — the pure-versus-effectful distinction.
