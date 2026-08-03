---
type: decision
id: dcr-74c4353
status: decided
title: The statement is signed as canonical JSON, narrowed by excluding numbers
created: 2026-08-02
source: design conversation, 2026-08-02
---

# The statement is signed as canonical JSON

An attestation's statement — the typed record of what check ran, over what tree, with what result —
is serialized as canonical JSON, the form specified by RFC 8785. That specification takes an
ordinary JSON document and fixes every choice a serializer would otherwise be free to make: object
members are sorted by key, no whitespace appears between tokens, and each string is escaped one
particular way. Two serializers following it turn the same document into the same bytes.

The-valley's statements narrow it by one rule: a statement carries strings, objects and arrays, and
never a number. The detached signature covers those canonical bytes and nothing else — no envelope,
no git object, no re-serialization at verification time.

The statement's shape is [schema/attestation.cue](../../schema/attestation.cue); the renderer is
`attest/canonical.go`.

## The stored statement is the signed bytes

An attestation is stored as the statement and a detached signature beside it, so what a reader gets
back from the ref is the byte sequence that was signed. There is no re-derivation step between what
is stored and what is checked, and no encoding to decode before reading. A statement can be read
with `git cat-file`, searched with `grep`, and vetted against the schema exactly as stored — the
same plain-files property the knowledge graph has, applied to evidence.

Verification makes this a rule rather than a convenience. A statement whose bytes are not the
canonical form of what it says is refused, even when its signature checks out, because that
statement would fail to verify wherever it was next recomposed.

## Excluding numbers removes the specification's hard part

Almost all of RFC 8785 is mechanical. The exception is numbers: canonicalizing them means pinning a
single textual form for every IEEE 754 double, which is where independent implementations disagree
and where the specification carries most of its weight.

A statement has no field that is a number. Digests are hex strings, times are RFC 3339 strings,
results are enumerated strings. So the rule is enforced rather than merely observed: a number
reaching the renderer is refused, because the canonical form of a statement carrying one has never
been decided and a guess would be a guess about signed bytes.

## Keys are ASCII, so member order is unambiguous

Member order is the other place a serializer can be subtly wrong. The renderer sorts members by
their keys' UTF-8 bytes, and RFC 8785 sorts them by UTF-16 code units. The two orderings agree over
ASCII and part company above it, so ASCII keys are a condition of these bytes being the bytes anyone
else produces.

`#AsciiKey` in the schema holds keys to printable ASCII, and any map whose keys come from a caller
rather than from the schema is written under that constraint — the ecosystem-specific key-value
context strings of [[ida-d2dc957]]
([ida-d2dc957-attestations-ride-on-a-transparent-leaf.md](../ideas/ida-d2dc957-attestations-ride-on-a-transparent-leaf.md))
are the case already in view. A key outside it fails `cue vet`, and one that reaches the renderer
anyway is refused there rather than sorted. The failure this prevents is the quiet kind: two
implementations signing different bytes over one statement, surfacing much later as a signature that
does not verify, with nothing pointing at the key that caused it.

## Conformance vectors are what hold a second implementation

The canonical bytes are an interop contract, so they are pinned by fixed files rather than by the
code that happens to produce them. `attest/conformance/` holds statements and fragments paired with
their exact expected bytes, covering the cases implementations actually part company over: member
order across case, prefixes and the empty key; nesting; empty objects, arrays and strings; the full
escape table; and the difference between an absent field and one present but empty. One vector
carries a detached signature made by a fixed key, so the path from a statement to a signature
something else can check is pinned end to end and not only the serialization.

The flake's `attest-conformance` check renders every vector and compares byte for byte. That check
is the standard a regenerated or independently written `attest` is held to, and a vector's recorded
bytes may change only when the canonical form itself changes — which invalidates every signature
ever made under it.

## Where this sits

This refines `dcr-0de694f`, which fixes the attestation's five elements and lands on branch
`dcr/phase2-attestation-shape`, by settling the one it leaves to the implementation: how a statement
becomes the bytes a signature covers. Nothing above changes what a statement says, who signs it, or
where it is stored.

## Related

- The statement and envelope directions this serves: [[ida-d2dc957]]
  ([ida-d2dc957-attestations-ride-on-a-transparent-leaf.md](../ideas/ida-d2dc957-attestations-ride-on-a-transparent-leaf.md))
- Why the signature covers the statement rather than a git object: [[ida-51605e8]]
  ([ida-51605e8-authenticity-not-git-coupled.md](../ideas/ida-51605e8-authenticity-not-git-coupled.md))
- The helper and what it verifies: [verification.md](../../design/verification.md#the-helper)
