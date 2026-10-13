---
type: idea
id: ida-9493c1d
status: exploring
title: "Record authorized history redaction with a signed rewrite map"
created: 2026-08-22
source: design conversation, 2026-08-22
---

# Record authorized history redaction with a signed rewrite map

When sensitive content must be removed, the system should record an authorized history rewrite that
explains which history replaces the old history. The record must preserve accountability without
retaining the removed payload.

This is related to the recovery operation left open for a compromised genesis key in
[[dcr-b87f6e8]](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md). Both change the
history or authority from which later verification proceeds.

## Proposed record

A signed human act would authorize the removal and identify its scope. A rewrite map would connect
old and replacement history. A tombstone would point to the signed record at the place where content
was removed.

The proof format still needs design. It should let a verifier check that the rewrite changed only
the authorized scope without disclosing the removed content.

A raw digest can reveal a low-entropy secret through guessing. Public references to removed content
therefore need an appropriate blinded commitment, rather than simply publishing the original blob
hash.

## Constraints to preserve

The proposal requires event and transparency records to refer to content through safe statements and
commitments, rather than embedding raw sensitive payloads.

Replay must also account for the authorized rewrite. Equality between reconstructed history and
recorded events would be evaluated through the signed mapping.

These constraints do not make every digest safe or make the redaction mechanism complete by
themselves.

## Copies and storage

Redaction currently has to account for the primary, mirrors, backups, and copies in run transcripts
or other artifacts.
[[dcr-d7952bc]](../decisions/dcr-d7952bc-phase0-replication-github-transitional.md) describes the
replication arrangement.

A future common storage layer might coordinate this as one requested operation. It would still need
to define how replicas, retention, and unavailable copies are handled. Removing a reference from the
primary cannot establish that all previous readers have deleted their copies.

[[ida-b7025b5]](ida-b7025b5-human-decisions-are-signed-acts.md) describes the signed authorization.
The rewrite format, proof, and completion guarantees remain exploratory.
