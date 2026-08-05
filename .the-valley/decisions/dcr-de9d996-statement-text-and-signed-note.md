---
type: decision
id: dcr-de9d996
status: decided
title: A statement is written down as lines and signed as a note
created: 2026-08-03
source: design conversation, 2026-08-03
---

# A statement is written down as lines and signed as a note

An attestation's statement — the typed record of what check ran, over what tree, with what result —
is written down as lines, and it is signed as a note: that text, a blank line, and one signature
line per signer. This node settles both halves of how an attestation is put on disk, because they
are one question: the bytes a signature covers, and how a signature sits beside them.

The written form is
[design/verification.md](../../design/verification.md#a-statement-is-written-down-as-lines); the
envelope is [the section after it](../../design/verification.md#the-envelope-is-a-signed-note). The
statement's shape is [schema/attestation.cue](../../schema/attestation.cue), and the reference
implementation is `attest/text.go` and `attest/sign.go`.

## The written form is lines, in the order a reader reads them

The first line is the statement type. Every line after it is a key, one space, and a value. A key is
the path to the value with dots between segments, and a segment that is a number indexes an array.

The statement type is on the first line and nowhere else. It is the one thing a reader needs before
anything else — what this document is, and which revision of it — and it is also what separates an
attestation from any other note a key signs, because a signature covers the text alone. One line
answers both.

Lines are written in a fixed order: what the statement is about, then what kind of claim it makes,
then the claim, then what produced the change. A statement is a document a reader reads in order to
decide something, so the reader's first question is answered first. The claim's own order is per
predicate type, because the claim reads differently for each: a pure claim reads as the check, its
result, and then the three digests a verifier re-derives against; a notarization reads as the check,
the environment it ran in, when that was, and what was observed. The order table is
[design/verification.md](../../design/verification.md#line-order).

Within a set keyed by names a caller supplies rather than the order table — a digest set, and any
map added later — entries sort by key. Fixed order where position carries meaning, sorted order
where it does not. An array is positional, so its elements are written in index order.

A field with no position in the order has no written form and is refused. A new field's position
therefore arrives with the field, and the predicate type is versioned, which is what lets a claim
shape grow without moving what came before it. It also means only a whole statement has a written
form, since order is defined per predicate type.

## The form has no escapes

A value is written as its own bytes, so there is exactly one way to write it and nothing for two
implementations to disagree about. Everything a line cannot carry is refused rather than escaped or
dropped — a value holding a newline, an empty value, a number at a leaf, an empty object, a field
name holding the dot or the space the form uses as separators.

A serialization that admits escapes has to pin which escape each character gets, and that table is
where independent implementations part company; a form with no table has no such surface. Refusing
rather than dropping is what keeps the document that passed validation and the document that got
signed the same document.

## What is stored is what was signed

An attestation is stored as one note, so what a reader gets back from the ref is the byte sequence
that was signed with its signatures beneath it. There is no re-derivation step between what is
stored and what is checked, and no encoding to decode before reading. A statement can be read with
`git cat-file`, searched with `grep`, and vetted against the schema exactly as stored — the same
plain-files property the knowledge graph has, applied to evidence.

Text is accepted only if writing the document it says back out reproduces it byte for byte, so text
that parses is text a renderer would have produced. A statement whose bytes are not the written form
of what it says is refused even when its signature checks out, because that statement would fail to
verify wherever it was written out again. The form is a rule rather than a convention, and a
verifier settles it by reading rather than by trusting the sender.

## Several signatures are sibling lines under one text

Where more than one party attests to the same subject, their signatures are sibling lines beneath
one statement. A signature therefore cannot be lifted onto a different statement: it sits under the
text it covers, and moving it means moving that text with it. Composition is structural, not a rule
a verifier has to remember to apply.

This is the note format of `golang.org/x/mod/sumdb/note`, which is also the format transparency log
checkpoints are published in. So the envelope that carries an attestation now carries a checkpoint
later, and the transparency work of
[roadmap.md, Phase 6](../../design/roadmap.md#phase-6--trust-backstop) does not change envelope at
the boundary — the direction [[ida-d2dc957]]
([ida-d2dc957-attestations-ride-on-a-transparent-leaf.md](../ideas/ida-d2dc957-attestations-ride-on-a-transparent-leaf.md))
points at.

A signature covers the text and nothing else — not the signer's name, not a namespace, and no git
object, per [[ida-51605e8]]
([ida-51605e8-authenticity-not-git-coupled.md](../ideas/ida-51605e8-authenticity-not-git-coupled.md)).
What separates an attestation from any other note a key signs is the text's own first line, which is
the statement type.

## The signer is named, and a verifier holds names and hashes

A note names its key by a string and a hash. The string a valley host uses is its fully qualified
name, a slash, and what it is signing: `laddie.gunk.dev/attestations`. The host part says which
machine to go and ask about a key; the suffix keeps a key signing attestations distinct, as a
verifier key, from the same key signing anything else, because the name is inside the key hash.

A verifier is given a set of known keys — one line each, carrying the name, the key hash and the
public key. That is the whole interface: no allowed-signers file, no certificate, no directory
lookup. A signature by a key not among them is a signature by nobody the verifier accepts, and a
signature by one of them that does not check out refuses the whole note.

The host signs with a raw Ed25519 key, which is the only algorithm a valley note carries.
`/etc/ssh/ssh_host_ed25519_key` is the natural production identity, because the host already has it
and it already says which host this is, but the key is a parameter and provisioning one is
deployment.

## The format is written out rather than depended on

`golang.org/x/mod/sumdb/note` is the reference implementation of the envelope, and `attest` writes
that format out again over `crypto/ed25519` instead of importing it.

The format is frozen: it is what sum.golang.org publishes and what every checkpoint verifier already
reads, so it cannot drift out from under a second implementation. Writing it out is about a hundred
lines. Against that, a dependency graph's size is itself the exposure, and this program depends on
nothing outside the standard library — it builds with no vendored modules, fetches nothing, and the
whole of what it runs is in one directory. One module would end that property for code this short.

Interoperability is bought by pinning bytes rather than by sharing code, which is the same trade the
conformance vectors already make.

## Conformance vectors are what hold a second implementation

The written form and the note are an interop contract, so they are pinned by fixed files rather than
by the code that happens to produce them. [attest/conformance/](../../attest/conformance/) holds
statements paired with their exact text, notes that must verify — including one carrying two
signatures — and the documents and texts that must be refused. The flake's `attest-conformance`
check runs the whole set, and it holds the implementation to the reference one from the other side
too: the unit tests re-derive the published key hash of sum.golang.org from its name and its public
key, an answer the reference implementation already computed.

A vector's recorded bytes may change only when the form itself changes, which invalidates every
signature ever made under it.

## Where this sits

This refines [[dcr-0de694f]]
([dcr-0de694f-phase2-attestation-shape.md](./dcr-0de694f-phase2-attestation-shape.md)), which fixes
the attestation's five elements, by settling the two it leaves to the implementation: how a
statement becomes the bytes a signature covers, and how those signatures are carried. Nothing above
changes what a statement says, who signs it, or where it is stored.

## Related

- The statement, envelope and transparency layering this serves: [[ida-d2dc957]]
  ([ida-d2dc957-attestations-ride-on-a-transparent-leaf.md](../ideas/ida-d2dc957-attestations-ride-on-a-transparent-leaf.md))
- Why the signature covers the statement rather than a git object: [[ida-51605e8]]
  ([ida-51605e8-authenticity-not-git-coupled.md](../ideas/ida-51605e8-authenticity-not-git-coupled.md))
- The helper and what it verifies: [verification.md](../../design/verification.md#the-helper)
