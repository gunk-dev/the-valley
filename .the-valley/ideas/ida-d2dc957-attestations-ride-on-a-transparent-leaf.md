---
type: idea
id: ida-d2dc957
status: exploring
title: Attestations ride on a transparent leaf
created: 2026-07-25
source: design conversation, 2026-07-25
---

# Attestations ride on a transparent leaf

An attestation decomposes into three layers that can be chosen separately. The **statement** is what
is asserted — the subject, the kind of check, its inputs and its outputs. The **envelope** is how
that statement is signed. **Transparency** is how the signed statement becomes undeniable. Format
arguments turn circular when these are conflated, because an objection to one layer is answered with
a property of another. Keeping them apart also shows how little they constrain each other: a
transparency log that stores only a hash barely constrains the envelope at all.

This is a direction under exploration, not a decided design. What follows is where the evidence
currently points, and the shape of the questions that remain.

## Direction for the envelope and transparency

The C2SP line of specifications, and specifically the emerging identity-transparency leaf format
that unifies the Sigstore and Sigsum leaf shapes. Its leaf carries a version byte, a digest of a
root of trust, a digest of the message, a sorted set of double-hashed ecosystem-specific key-value
context strings, and a receipt digest. The signature covers the specification identifier, the
checksum and those context strings.

Double hashing is what makes this fit here. A value can be withheld from the log while still being
authenticated by the signature, which matters for a project whose repositories are not public.

## Direction for the statement

The typed-statement shape from the in-toto attestation line: a subject identified by a digest set
rather than a single digest value, and a versioned predicate type naming what kind of claim is being
made.

Two properties earn their place. First, a digest set can hold a content-addressed tree digest
alongside an advisory commit hash, so a change stays identifiable after the integrator rebases it —
which is exactly what [[ida-51605e8]]
([ida-51605e8-authenticity-not-git-coupled.md](./ida-51605e8-authenticity-not-git-coupled.md))
requires. Second, a versioned predicate type carries the pure-versus-effectful distinction inside
the signed bytes, rather than leaving it to convention outside them.

## Multiple parties sign as siblings

When more than one party attests to the same thing, the signatures sit side by side rather than
inside one another. Two statements over the same subject digest, each signed by one party — not one
party's signature embedded as a field within another party's document.

The attack rules nesting out. If an inner approval signature covers only a verdict or a bare tree
hash, whoever composes the outer document can lift that approval onto a different set of claims, and
the outer signature still verifies over the result. Sibling statements defeat this by construction,
because each signature carries its own identification of the subject.

Worth recording why nesting was tempting at all: signature formats intended for humans are not
registered types in the note-based line, so a human approval would have had to travel inside
something else. The nesting would have been forced by a registry gap, not chosen for any property it
has.

## Post-quantum and presence pull in opposite directions

They need not be reconciled in a single signature, because they are claims with different lifetimes.

Presence is consumed immediately. A gate checks it at decision time, and the question of whether a
human was physically present has no long-lived component. Durability is the long-lived claim — that
a statement existed, unaltered, at a given time. A log's Merkle structure is hash-based and already
resists quantum attack, so inclusion carries much of the durability burden regardless of which
algorithm signed the statement.

Where a post-quantum signature over the statement is wanted, it need not be the hardware one.
Requiring two signatures — a hardware classical signature for presence, and a software post-quantum
signature for durability — yields both properties. The automatable half is worthless on its own, so
unforgeability survives the split.

## Considered and set aside

The objection that signing structured documents forces a hazardous canonicalisation step does not
apply to the pre-authentication encoding used by the in-toto envelope line, which signs a
length-prefixed encoding rather than a re-serialised object. That envelope line was set aside for
fit with the transparency direction, not because the canonicalisation objection stood against it.

## Open

**Which context keys this project would define.** The leaf's context strings are ecosystem-specific,
so the set of keys and their meanings is a design decision this project has to make for itself.

**What exactly identifies the subject.** Whether a content-addressed tree digest is the right
primitive, and what else belongs in the digest set alongside it.

**Whether a hardware-presence credential type gains first-class support in the leaf format.** No
hardware token today can produce the raw signature such a leaf requires, which forces a human
approval to travel as data submitted under a machine identity — the opposite of what the presence
requirement is for.

## Related

- The requirement this answers — authenticity that survives a rebase: [[ida-51605e8]]
  ([ida-51605e8-authenticity-not-git-coupled.md](./ida-51605e8-authenticity-not-git-coupled.md))
- The human act one such statement carries: [[ida-b7025b5]]
  ([ida-b7025b5-human-decisions-are-signed-acts.md](./ida-b7025b5-human-decisions-are-signed-acts.md))
- Who signs the other: [[ida-a8243d2]]
  ([ida-a8243d2-agent-runs-act-under-delegated-authority.md](./ida-a8243d2-agent-runs-act-under-delegated-authority.md))
- The pure-versus-effectful distinction the predicate type would carry:
  [verification.md](../../design/verification.md)
- Where this lands in the plan:
  [roadmap.md, Phase 2](../../design/roadmap.md#phase-2--attestations-verification-mvp)
