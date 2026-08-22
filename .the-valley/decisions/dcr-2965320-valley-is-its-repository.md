---
type: decision
id: dcr-2965320
status: decided
title: "A valley is its repository"
created: 2026-08-22
source: design conversation, 2026-08-22
---

# A valley is its repository

All of a valley's governing state is bytes in its repository: the floor, the identity registry, in
time the host declaration held by the host-owning valley. Project content is governed by those
bytes; compiled artifacts and runtime state are derived from them — pure functions of the integrated
tip, never a second source of truth.

Access to anything the valley governs is a function of its integrated tip. Changing access is
landing a change. The ability to land changes is itself so governed — tip N's bytes govern what may
become tip N+1 — grounded at genesis and, beneath the machinery, in the host that runs the gates
(the bootstrap treatment in [design/architecture.md](../../design/architecture.md)).

The bytes declare; keys prove. The registry holds public halves only, so no modification of bytes
manufactures possession: a hijacked registry can grant a new key, visibly and attributably; it
cannot silently become anyone.

The tip governs in epochs: a landed change takes effect at the next compilation, so access begins
and ends at convergence, never retroactively.

Write and read are asymmetric. Write access is fully a function of the bytes — every grant a commit,
auditable in history, revocable by another. Read is prospective only: a gate refuses the next fetch,
and nothing un-reads what was read. Governing state accordingly leans public — outside verification
of the machinery's signatures depends on reading the registry.

Cross-valley access is more bytes: the granting valley lands a change citing another valley's
principal, and the floor constrains outward grants the way it constrains policy. Hosting is state
too: a valley's gates run on a host whose declaration lives in the host-owning valley's repository.

## Related

- [[dcr-b87f6e8]]
  ([dcr-b87f6e8-identity-is-a-governed-registry.md](./dcr-b87f6e8-identity-is-a-governed-registry.md))
  — the registry this node's account of access and epochs describes.
- [[dcr-9b5da04]]
  ([dcr-9b5da04-hosts-serve-isolated-valleys.md](./dcr-9b5da04-hosts-serve-isolated-valleys.md)) —
  the host a valley's gates run on, whose declaration is itself governed state.
- [[dcr-e544f20]]
  ([dcr-e544f20-access-is-verbs-on-projects.md](./dcr-e544f20-access-is-verbs-on-projects.md)) — the
  verb model this axiom grounds.
