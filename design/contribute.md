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
3. **Compose the attestation** — a structured claim about what ran: inputs, derivations, outputs,
   results.
4. **Sign and publish to the transparency log** — same SSH key as the commit; the inclusion proof is
   appended as a sidecar.
5. **Store the attestation in the repo** — as a git blob under
   `refs/the-valley/attestations/<commit-sha>`, fetchable and lookupable by SHA.
6. **Signal integration intent** — a second ref, `refs/the-valley/integration-requests/<name>`,
   pointing at the topic commit; without it, the topic branch is just a backup.
7. **Push atomically** — `git push --atomic` of topic branch + attestation ref + request ref; all
   three land or none do.
8. **Server-side projection** — the bare repo's `post-receive` hook emits one bus event per updated
   ref; it is a pure projection, no policy and no verification.

## The one invariant

The bare repo's `pre-receive` hook enforces exactly one structural rule: only the integrator's key
may write protected refs, and attestation refs are create-only. Everything else is open to anyone
with push access. All complex policy lives in the integrator ([architecture.md](./architecture.md),
_The one structural git invariant_).
