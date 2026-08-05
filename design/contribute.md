# Contributor protocol

What a contributor (human or agent) does to push a change and request its integration into a
protected ref.

## Four properties

A push that is:

1. **Cryptographically attributable** to the contributor.
2. **Accompanied by a signed promise** that the repo's canonical check set passed locally in a
   hermetic environment.
3. **Externally tamper-evident** via inclusion in a transparency log.
4. **Explicitly marked** as ready for integration into a target branch.

All four are produced by one atomic `git push` — native git verbs plus one helper for attestation
composition, no `git` wrapper command.

## The steps

1. **Commit, signed natively** — SSH commit signing, no GPG.
2. **Run the canonical check set** — the repo's `flake.nix` check derivations, locally and
   hermetically, via one helper (e.g. `nix run .#attest`); if any fails, nothing is published.
3. **Compose the statements** — one per check, each a typed, versioned record of what ran, over
   what, with what result, plus the provenance of the run that produced the change. The subject is a
   digest set whose primary member is a content-addressed digest of the resulting tree; a commit
   hash may accompany it as an advisory member, never relied on for identity
   ([dcr-0de694f](../.the-valley/decisions/dcr-0de694f-phase2-attestation-shape.md)).
4. **Sign as a note** — the host signs the statement text; the signature covers that text and
   nothing else, and further signers add sibling lines under the same text
   ([dcr-de9d996](../.the-valley/decisions/dcr-de9d996-statement-text-and-signed-note.md)).
5. **Store the attestation in the repo** — at a git ref keyed by the subject digest and the signer,
   `refs/the-valley/attestations/<tree digest>/<signer key hash>`.
6. **Publish to the transparency log** — the inclusion proof is appended as a sidecar; lands with
   [roadmap Phase 6](./roadmap.md#phase-6--trust-backstop).
7. **Signal integration intent** — how integration is requested is open. The integrator is designed
   around change objects — a diff targeting a stream, with identity stable across rebases — rather
   than branches, and how a change is submitted is part of that pending design
   ([ida-93e4f91](../.the-valley/ideas/ida-93e4f91-changes-not-branches.md)).
8. **Push atomically** — `git push --atomic` of the topic branch plus its attestation refs; all land
   or none do.
9. **Server-side projection** — the bare repo's `post-receive` hook emits one bus event per updated
   ref; it is a pure projection, no policy and no verification.

## The one invariant

The bare repo's `pre-receive` hook enforces exactly one structural invariant, with two parts: only
the integrator's key may write protected refs, and attestation refs are create-only. Everything else
is open to anyone with push access. All complex policy lives in the integrator
([architecture.md](./architecture.md), _The one structural git invariant_).
