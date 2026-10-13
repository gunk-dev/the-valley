---
type: idea
id: ida-d2dc957
status: exploring
title: "Separate attestation claims, signatures, and transparency records"
created: 2026-07-25
source: design conversation, 2026-07-25
---

# Separate attestation claims, signatures, and transparency records

An attestation design has three separate concerns. The **statement** describes the claim. The
**envelope** carries signatures over it. A **transparency log** provides evidence that a record was
published and helps expose inconsistent or altered histories.

Choosing one layer need not determine the others. A log that records a digest can accommodate
several statement or envelope formats.

## Statement and envelope

The useful parts of the in-toto statement model are subjects identified by digest sets and versioned
predicate types.

A digest set can identify tree contents while retaining a commit hash as a locator. It does not make
evidence valid after an arbitrary rebase;
[[dcr-439b771]](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md) defines
that transfer.

A versioned predicate type makes the kind of claim explicit in the signed bytes, including the
pure-versus-effectful distinction.

[[dcr-0de694f]](../decisions/dcr-0de694f-phase2-attestation-shape.md) and
[[dcr-de9d996]](../decisions/dcr-de9d996-statement-text-and-signed-note.md) define the current
statement and signed-note envelope. Further transparency work can build on those decisions without
reopening every format choice.

## Transparency direction

The C2SP identity-transparency work is a candidate source of ideas. The examined leaf design
combines commitments to a message, trust information, ecosystem-specific context, and a receipt.

Its treatment of context allows a value to be authenticated without placing the full value in the
public log. This could support private repository data or large run inputs. The exact context fields
and disclosure rules for the-valley remain to be designed.

## Multiple signers

Each signature must authenticate the complete claim it is intended to support, including the subject
and kind of approval.

For parties endorsing identical text, the signed-note format uses sibling signature lines. Different
claims need separate statements with explicit relationships.

A bare approval signature over a verdict or tree hash must not be embedded in a larger document and
treated as approval of every additional claim in that document. The outer signer's signature cannot
extend the meaning of the inner one.

## Presence and long-term verification

Human approval needs evidence of physical presence at decision time. Long-term records need durable,
verifiable evidence of what was approved and published.

These may use different mechanisms. One direction is a hardware-backed presence signature combined
with another signature or log evidence intended for longer-term verification.

Whether that provides the required post-quantum properties needs a complete design. Hash-based logs,
checkpoints, witness signatures, and retained verification data all matter. A Merkle tree alone does
not settle the lifetime of the authorization evidence.

## Encoding considerations

A signed envelope can authenticate exact bytes without requiring a verifier to reserialize an
arbitrary structured document. Length-prefixed pre-authentication encoding is one example.

The current text format instead specifies a canonical representation and checks it explicitly.
Format evaluation should distinguish these approaches rather than assume that all structured signing
has the same canonicalization risks.

## Open questions

- Which project-specific context fields should be committed to?
- How should a stable change identity appear alongside a checked-tree digest?
- How should a hardware-presence assertion be represented and verified?
- What transparency and long-term verification guarantees are actually required?

[[ida-51605e8]](ida-51605e8-authenticity-not-git-coupled.md) covers storage-independent
authenticity. [[ida-b7025b5]](ida-b7025b5-human-decisions-are-signed-acts.md) and
[[ida-a8243d2]](ida-a8243d2-agent-runs-act-under-delegated-authority.md) cover human approval and
delegated agent work. The [roadmap](../../design/roadmap.md#phase-6--trust-backstop) places
transparency in the trust-backstop work.
