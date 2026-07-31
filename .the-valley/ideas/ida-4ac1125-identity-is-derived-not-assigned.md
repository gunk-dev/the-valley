---
type: idea
id: ida-4ac1125
status: exploring
title: Identity is derived, not assigned
created: 2026-07-31
source: design conversation, 2026-07-31
---

# Identity is derived, not assigned

> **STUB.** This node names a pattern and collects the instances the design has already reached
> locally. It resolves nothing.

Name a thing by what it is, or by what produced it — never by where it currently sits. Everything
that previously served as the name is then demoted to a locator, and a locator is swappable.

## Instances

- [[ida-93e4f91]] ([ida-93e4f91-changes-not-branches.md](./ida-93e4f91-changes-not-branches.md)) —
  the branch becomes a locator; a change's identity is its content and survives rebase.
- [[ida-51605e8]]
  ([ida-51605e8-authenticity-not-git-coupled.md](./ida-51605e8-authenticity-not-git-coupled.md)) —
  the commit hash becomes a locator; a signed statement identifies its subject by content, and git
  storage becomes a backend rather than the contract.
- [[ida-45178f6]]
  ([ida-45178f6-agent-identity-is-provenance.md](./ida-45178f6-agent-identity-is-provenance.md)) — a
  run has no assigned name at all; its identity is what produced it.
- [[ida-b9f646c]]
  ([ida-b9f646c-nix-backend-not-substrate.md](./ida-b9f646c-nix-backend-not-substrate.md)) — the
  implementation becomes a locator; the current backend is not the contract.
- [[ida-48c8868]] ([ida-48c8868-stores-beyond-git.md](./ida-48c8868-stores-beyond-git.md)) — git
  itself becomes a locator; durability attaches to project state rather than to the store holding
  it.
- No node of its own yet: referencing an agent trajectory or an attestation by digest rather than by
  ref path, so that where the bytes live is a replication decision rather than a semantic one. This
  is what lets the question of whether such data belongs in the repository or in a content-addressed
  store elsewhere stay open at no cost — the deferral is the point, not an evasion.

## What the pattern buys

When a thing is named by what it is, moving where it lives later is a migration with no semantic
consequence. That is the practical payoff, and it is why several open placement questions can stay
open without accruing cost.

## The seam

The pattern is not uniform. [[ida-45178f6]]
([ida-45178f6-agent-identity-is-provenance.md](./ida-45178f6-agent-identity-is-provenance.md)) is
not content-addressing in the same sense as the others. A run has no content to hash; its identity
is derivational — what produced it — not a digest of what it is. Nix draws exactly this line,
between a derivation hash and a content-addressed output. So provenance is the same move in spirit,
replacing an assigned name with a fact derivable from the thing itself, but it is
derivation-addressed, and whether that is the same property or a neighbouring one is unresolved.

## Open

- Whether this is one property or a family of related ones.
- Whether derivation-addressing is the same move as content-addressing or a neighbour to it.
- Whether any part of the design resists the pattern — a case where an assigned name is the right
  answer.
