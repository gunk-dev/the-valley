---
type: idea
id: ida-51605e8
status: exploring
title: "Authenticate statements independently of Git objects"
created: 2026-07-25
source: design conversation, 2026-07-25
---

# Authenticate statements independently of Git objects

Approval and verification evidence should be signed statements about identified content. Their
authenticity should survive a change of storage system.

A signing primitive such as SSHSIG can already sign arbitrary bytes. The coupling arises when those
bytes are a Git commit object or the evidence identifies only a commit hash.

Rebasing changes a commit hash. An approval tied only to that hash therefore needs an explicit rule
connecting it to the result that lands. [[ida-93e4f91]](ida-93e4f91-changes-not-branches.md)
explores stable change identity, while
[[dcr-439b771]](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md) defines
transfer of check evidence to a new base.

[[dcr-0de694f]](../decisions/dcr-0de694f-phase2-attestation-shape.md) and
[[dcr-de9d996]](../decisions/dcr-de9d996-statement-text-and-signed-note.md) define the current
self-contained attestation statement and envelope. Tree contents identify its subject, and Git
provides storage. The broader identity for a change across different resulting trees remains open.

Storage-independent authentication follows the same direction as the portable implementation
contract in [[ida-b9f646c]](ida-b9f646c-nix-backend-not-substrate.md) and the store-independent
durability requirements in [[ida-48c8868]](ida-48c8868-stores-beyond-git.md).

Where statements should live outside Git remains undecided.
[[ida-b7025b5]](ida-b7025b5-human-decisions-are-signed-acts.md) describes the human approval they
also need to carry, and [contribute.md](../../design/contribute.md) describes the current
contribution path.
