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

The one exception is the core: a deliberately minimal mandate — the governing path classes require
human approval — that composes unconditionally for every valley, active by deployment rather than
adoption. A valley with no floor composes to the core alone and is landable from birth, growing its
law deliberately; a floor written empty cannot dissolve govern. The core is the piece of law that
belongs to the substrate itself rather than to any valley that houses it, and it is what anchors the
bootstrap loop: no valley, including the one hosting the substrate, can legislate away the gate on
its own registry.

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

- The layering this refines, and its absence semantics: [[dcr-f41f718]]
  ([dcr-f41f718-declared-verification-policy.md](./dcr-f41f718-declared-verification-policy.md))
- The identity schema as precedent: a substrate floor mandates properties, never names:
  [[dcr-b87f6e8]]
  ([dcr-b87f6e8-identity-is-a-governed-registry.md](./dcr-b87f6e8-identity-is-a-governed-registry.md))
- The bootstrap section of [design/architecture.md](../../design/architecture.md)
