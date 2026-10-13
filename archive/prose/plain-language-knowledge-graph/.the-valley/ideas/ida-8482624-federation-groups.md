---
type: idea
id: ida-8482624
status: adopted
title: "Let independently governed valleys exchange selected events and evidence"
created: 2026-07-02
source: PR #1
---

# Let independently governed valleys exchange selected events and evidence

Federation would let valleys cooperate while retaining their own policy, identities, and trust
decisions. A valley is the organization or personal namespace that owns a set of projects under
shared policy, as defined in [[dcr-8f069dd]](../decisions/dcr-8f069dd-valley-is-the-unit.md).

Some work crosses repositories within one valley. Other work crosses between independently governed
valleys. These require different coordination and authorization, even when both are described
informally as cross-repository work.

The [architecture](../../design/architecture.md#valleys-projects-and-hosts) records the high-level
commitment. This adopted sketch preserves the reasoning for more detailed design when
[S5 and later scenarios](../../design/user-scenarios.md) require it.

## Services within a valley

A valley's services include Git hosting, an event bus, integration controllers, and a build cache.
Knowledge is stored with projects, while views can combine it across the valley.

[[dcr-9b5da04]](../decisions/dcr-9b5da04-hosts-serve-isolated-valleys.md) defines one integrator
principal per valley. Multiple controllers can serve different protected streams under that
principal.

The build cache allows machines and independent verifiers to reuse content-addressed inputs and
artifacts. This matters to the re-verification model in
[verification.md](../../design/verification.md): rebuilding every dependency from scratch would make
verification expensive.

Input retention remains open. A pure claim does not expire with time, but reproducing it still
requires the referenced inputs to be available. The valley's cache policy needs to state what it
retains and for how long.

## Hosting

A small valley can run on one machine. The design should also permit its services to span hosts,
with separate controllers for protected refs and appropriately distributed hosting and messaging.

Multi-host operation is deferred by
[[dcr-9b5da04]](../decisions/dcr-9b5da04-hosts-serve-isolated-valleys.md). The separation of
services makes it a direction to implement, rather than proof that distributed deployment already
works.

## Coordination within and between valleys

| Concern     | Within one valley                                               | Between valleys                                                    |
| ----------- | --------------------------------------------------------------- | ------------------------------------------------------------------ |
| Integration | Coordinate dependent requests under shared policy and services  | Exchange requests or evidence across independent policy boundaries |
| Feedback    | Route a consumer failure to another local project               | Deliver selected feedback to another valley                        |
| Knowledge   | Reference a blocker in another project with a project namespace | Reference a node owned and served elsewhere                        |

Shared services make the first column easier, but do not automatically provide atomic integration
across repositories. A coordinating controller still needs to define what happens when only part of
a multi-project operation succeeds.

Federation addresses the second column: which events, evidence, and references may cross the
boundary and how the receiving valley interprets them.

## Trust

Trust measurements are local to a valley's verifier pool and policy. A signer's confirmation rate
elsewhere does not automatically have the same meaning locally.

A valley may import another valley's attestations and decide how much evidence they provide. It may
subscribe to selected event types or projects without receiving the other valley's entire bus.

Both sides must authorize the exchange. Local request and access permissions remain those defined in
[[dcr-e544f20]](../decisions/dcr-e544f20-access-is-verbs-on-projects.md).

Imported evidence raises unresolved identity and trust questions. A receiver must recognize the
signer and determine whether the foreign verification process meets its requirements. Possible
approaches include explicit identity links, a translation or discount of external measurements, and
requiring local verification before giving any credit.

[[dcr-b87f6e8]](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md) supplies the registry
model for independently recognizing external identities.

## Discovery

A valley can list the projects it owns. Discovery across valleys needs a way to find other valleys
and learn what they publish or offer for exchange.

That mechanism remains open. It belongs with federation because it must work across independently
managed namespaces.

[[ida-eac723e]](ida-eac723e-outcome-dag.md) describes the outcome engine that may use these
relationships. [self-transparency.md](../../design/self-transparency.md) applies to federated
changes as well as local ones, and [[dcr-2113c52]](../decisions/dcr-2113c52-pr1-relanding.md)
records where this sketch fits in the documentation.
