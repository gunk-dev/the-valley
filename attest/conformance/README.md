# Conformance vectors for the written form and the note

These files are the interop contract for an attestation. A second implementation of `attest` — a
rewrite of this one, or a reader written in another language — must write every `input.json` here as
exactly the bytes of the `statement.txt` beside it, and must verify every `statement.note`. Anything
else and the two sign different bytes over the same statement, so a signature made by one does not
verify against the other, and the way that failure arrives is a verification error that names
nothing.

The written form is lines in a fixed order, and the envelope is a signed note. Both are specified in
[design/verification.md](../../design/verification.md); the reasons behind each rule are in
[`../text.go`](../text.go) and [`../sign.go`](../sign.go), and the decision node those cite.

## What is here

Each directory under `vectors/` is one vector.

- `input.json` is a statement written in some arbitrary rendering — members out of order, whitespace
  anywhere, escapes chosen freely. It is input, not output.
- `statement.txt` is the exact byte sequence the written form produces from it. It is what a
  signature covers, and nothing else is.
- `statement.note`, where present, is that text with a blank line and one signature line per signer
  beneath it. The keys are in [`known_keys`](./known_keys); they exist for these vectors and hold no
  authority anywhere.

Every vector is a whole statement, because line order is defined per predicate type and a fragment
has no order at all. Between them they cover the places implementations part company: both predicate
types, each in its own order; a digest set with several members, whose entries sort by key while
everything around them does not; a delegation chain long enough that index `10` follows index `9`,
where an implementation ordering by the key's bytes rather than by the index will get the array
wrong; values written as their own bytes, including the characters a JSON encoder would have
escaped, text above the basic multilingual plane, and leading, trailing and repeated spaces, which
are all load-bearing and none of which survive an editor that trims whitespace; a statement with and
without provenance; and a note with two signature lines, which is what "several parties attest to
one subject" looks like.

`known_keys` is the whole of what a verifier is given: one line per key, each a name, the key hash
its name and public key produce, and the public key itself.

Every vector also vets against [`../../schema/attestation.cue`](../../schema/attestation.cue), so
the set does not drift into documents no verifier would accept.

`refused/` is the other half of the contract: documents and texts that must not be written or read
at all. A `.json` there is a document with no written form — a number, an empty object, a value
carrying a newline, a field name a line cannot carry. A `.txt` there is text that is not the written
form of anything: sections out of order, a set out of order, an array element out of its position, a
repeated key, an array with a gap in it, a key that is both a value and a path. An implementation
that accepts any of them will sooner or later sign bytes another one reads differently.

## Running them

`nix flake check` runs them as `attest-conformance`. Each vector is written out with
`attest render`, compared byte for byte, and written out again from its own recorded text to confirm
the form is a fixed point. Each signed vector is put through `attest verify`, so the path from a
statement to a note something else can check is pinned end to end rather than only the
serialization. Each file under `refused/` is put through `attest render` and must fail. The check
also perturbs one recorded byte sequence and one signature and requires both to be rejected, so a
vector set that agrees with itself vacuously cannot pass.

By hand, against any one vector:

```
$ nix run .#attest -- render attest/conformance/vectors/01-statement-minimal/input.json \
    | cmp - attest/conformance/vectors/01-statement-minimal/statement.txt
$ nix run .#attest -- verify \
    --note attest/conformance/vectors/04-two-signers/statement.note \
    --known-keys attest/conformance/known_keys
```

## Changing them

A vector's recorded bytes may only change when the written form itself changes, and that invalidates
every signature ever made under it. Regenerating a `statement.txt` to make a failing check pass is
the one thing this directory exists to prevent. A new vector is a new directory and needs no
registration; the check finds it.
