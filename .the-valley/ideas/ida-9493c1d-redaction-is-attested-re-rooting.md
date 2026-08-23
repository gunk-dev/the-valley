---
type: idea
id: ida-9493c1d
status: exploring
title: Redaction is an attested re-rooting
created: 2026-08-22
source: design conversation, 2026-08-22
---

# Redaction is an attested re-rooting

Sensitive content will sometimes have to leave the record: a secret committed in error, data that
may not be retained. Root can always excise it, but an out-of-band fix leaves the journal claiming
what the trees no longer show. Redaction wants an on-path shape, and the shape already has a name:
it is a re-rooting — the attested history rewrite [[dcr-b87f6e8]]
([../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md))
leaves open for genesis-key compromise — carrying a tombstone.

A redaction is a signed act ([[ida-b7025b5]]
([ida-b7025b5-human-decisions-are-signed-acts.md](./ida-b7025b5-human-decisions-are-signed-acts.md)))
naming what was removed and on whose authority. The rewrite maps old history to new; the statement
attests the map. A tombstone stands where the content was, carrying the statement's digest — the
truth without the payload. Content addressing makes the claim checkable: that a tree differs from
its predecessor only at the named path is provable from hashes alone, revealing nothing of what was
removed. A digest of a secret can itself leak — a low-entropy payload is brute-forceable from its
hash — so a tombstone carries a blinded reference, never the raw blob hash.

Two constraints are adopted now, while they cost nothing: the bus and any future transparency log
carry statements over digests, never raw content, so redaction-safety holds by construction rather
than retrofit; and replay equality over the event log survives a re-rooting as equality modulo the
attested rewrite map.

Today a redaction's surface is every replica — primary, mirror, backup snapshots, the soft residue
of transcripts. That enumeration is transitional, not structural: mirroring and backup are the
interim answer to durability ([[dcr-d7952bc]]
([../decisions/dcr-d7952bc-phase0-replication-github-transitional.md](../decisions/dcr-d7952bc-phase0-replication-github-transitional.md))),
and the direction is a more resilient core storage layer for project state — durability intrinsic to
the layer that holds it, backup and mirroring foregone — under which redaction collapses to one
operation at one layer.

## Related

- [[dcr-b87f6e8]]
  ([../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md))
  — the re-rooting open item this gives a second motivation.
- [[ida-b7025b5]]
  ([ida-b7025b5-human-decisions-are-signed-acts.md](./ida-b7025b5-human-decisions-are-signed-acts.md))
  — the signed-act shape a redaction takes.
- [[dcr-d7952bc]]
  ([../decisions/dcr-d7952bc-phase0-replication-github-transitional.md](../decisions/dcr-d7952bc-phase0-replication-github-transitional.md))
  — the transitional replication surface a redaction has to reach today.
