---
type: decision
id: dcr-9c75283
status: decided
title: Substrate law is inert until adopted; a minimal core is the one exception
created: 2026-08-23
source: design conversation, 2026-08-22/23
---

# Substrate law is inert until adopted; a minimal core is the one exception

The substrate ships definitions that bind no one: the schema, the templates, the defaults are inert
artifacts. A valley's floor is its landed act of adoption — only through that act does substrate
content become the valley's law, embedded at the valley's own tip. Adoption is drift-free by
construction: a substrate update changes what a future adoption means, never what a valley already
embedded.

Two repositories wear two hats each, and the hats are directory-shaped. The substrate's repository
defines policy for everyone (inert) and carries a project layer binding itself alone (active). A
valley's repository carries the floor (active for every project the valley owns) and its own project
layer (active for itself alone). Four authorities, two repositories, and every activation is a
landed change in the valley whose law it becomes — including the loop where the adopting valley's
floor comes back to govern the substrate's repository as an ordinary project.

The one exception is the core: a deliberately minimal mandate that composes unconditionally for
every valley, active by deployment rather than adoption. The core's default is physical control.
Every change entering a valley project lands only with a presence-signed approval over its hash — a
signature that only hardware a person touches can produce. A valley with no policy is maximally
human-controlled, not minimally: absent any more specific policy, each landing costs a touch. A
valley with no floor composes to the core alone and is landable from birth, growing its law
deliberately; a floor written empty cannot dissolve govern. The core is the piece of law that
belongs to the substrate itself rather than to any valley that houses it, and it is what anchors the
bootstrap loop: no valley, including the one hosting the substrate, can legislate away the gate on
its own registry.

Policy subtracts from that default. Exempting a class of change from approval — for instance,
letting changes derived from an already-approved intent artifact flow without a further touch — is
an ordinary policy change, made by whatever authority governs the layer in question. But policy
changes themselves, at every layer, are always presence-signed: an owner may delegate policy-detail
authority to an agent, and the delegation is itself a signed act ([[ida-b7025b5]]
([ideas/ida-b7025b5-human-decisions-are-signed-acts.md](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md))).
Anything may be delegated except the act of delegating, so the exemption machinery can never exempt
itself.

Which defaults are sensible is a question the substrate expects to learn from experience: suggested
base policies per project type are expected to evolve as valleys use the core in practice. The
worked direction is that intent artifacts — requirement documents living in the knowledge base —
carry human approval, and changes derived from them flow without review.

The approval mechanism itself — a signature class that proves presence over the change's identity —
ships before the core activates. Sequencing it this way means activation never strands a valley
without a way to satisfy the gate.

Deployment is a drift channel, and it is accepted for the core alone: a security floor's rare
changes should reach every valley without waiting on adoption. The core stays tolerable exactly as
long as it stays small.

The whole arrangement is a rapidly iterating proof of concept, and the discipline matches that
stage: compatibility windows, coordinated migrations. When the system hardens, the substrate commits
to its adopters with semantic versioning on config and code — a breaking change to the schema or the
core announces itself in the version, and conformance vectors are the contract's test
([[ida-4557af7]]
([ideas/ida-4557af7-spec-driven-iteration.md](../ideas/ida-4557af7-spec-driven-iteration.md))).

## Related

- Delegation as a signed act, and its limit at self-delegation: [[ida-b7025b5]]
  ([ideas/ida-b7025b5-human-decisions-are-signed-acts.md](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md))
- The layering this refines, and its absence semantics: [[dcr-f41f718]]
  ([dcr-f41f718-declared-verification-policy.md](./dcr-f41f718-declared-verification-policy.md))
- The identity schema as precedent: a substrate floor mandates properties, never names:
  [[dcr-b87f6e8]]
  ([dcr-b87f6e8-identity-is-a-governed-registry.md](./dcr-b87f6e8-identity-is-a-governed-registry.md))
- The bootstrap section of [design/architecture.md](../../design/architecture.md)
