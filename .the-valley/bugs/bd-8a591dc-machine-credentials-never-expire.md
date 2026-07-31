---
type: bug
id: bd-8a591dc
status: open
title: Machine access is a permanent grant — provisioning is O(machines) and revocation never happens
created: 2026-07-31
source: provisioning a disposable agent VM against the pilot host, 2026-07-31
---

# Machine access is a permanent grant

Granting a machine access to a valley host means appending its public key to
`services.valley.authorizedKeys` and rebuilding the host. The grant has no expiry, so it persists
until someone edits the list again. Nothing observes that the machine it was issued for has ceased
to exist, and a disposable machine leaves its key behind when it goes.

Two costs follow, and they compound as the number of machines grows.

Provisioning is O(machines) with a human deploy in the loop. Each new machine needs a change to the
host's declaration, review, a merge, and an interactive rebuild of the host. That is a deploy of the
primary git host to grant read access to a scratch machine — an expensive motion to repeat, and one
performed on the machine whose availability everything else depends on.

Revocation is the harder cost, because it never happens on its own. A key list only shrinks when a
human notices an entry is dead and removes it, and nothing prompts that noticing. Ephemeral machines
are exactly the case where the grant outlives the grantee, so the list accretes standing grants to
destroyed hosts. Each entry reaches every project on the host, because access is host-level by
design.

## Why it is acceptable today

Every key in the list belongs to the operator, the host is reachable only over a private tailnet,
and the projects are small in number. A stale entry is a key nobody holds on a host nobody else can
route to. The defect is one of scale and hygiene, not a live exposure.

## The gate

The mechanism needs replacing before either of these becomes true:

- machines that are not the operator's own are granted access, or
- machines are provisioned often enough that the manual rebuild is a routine motion rather than an
  exception.

Dispatched agent machines make the second true first, and they arrive with S3.

## Directions, not decisions

- **Move authorization to the tailnet.** Identity is already declared to be Tailscale-ACL-based, and
  the tailnet is already the front door, but it currently establishes reachability while a static
  key list establishes authorization. An ACL tag naming which nodes may act as the git user would
  make access follow from a machine's tag, needing no host change per machine, and would inherit
  node expiry as a TTL. Whether Tailscale SSH composes with `git-shell` and non-interactive
  `git-receive-pack` is unverified, and it bypasses the module's sshd hardening.
- **Short-lived certificates.** An SSH CA trusted by the host, issuing certificates that expire in
  hours, makes revocation the default rather than an act of hygiene. It also introduces a signing
  service, which is a new coordination point to justify against the decentralization constraint. The
  trust backstop wants something shaped like this anyway.
- **Do not credential the machine at all.** Integration is planned to be pull-based. A contributor
  the integrator fetches from needs to be reachable, not authorized to push. A disposable machine is
  a better fetch source than it is an authorized pusher.

## Related

The instance-level counterpart is qinling's `dcr-34b3f6d` (keys partition by capability), which
observes that the pilot host's key inventory conflates decryption, authentication, and human
signing, and wants it partitioned by capability. Splitting a git-only class out of the inventory's
user list — done for the machine that surfaced this — separates authentication from decryption for
one member, and is a down payment on that partition rather than a substitute for it. Neither change
gives a grant an expiry.

The open question this sharpens is _when_ Phase-0 identity has to grow and into what
([openquestions.md](../../design/openquestions.md)). This bug is one concrete forcing function: a
machine that exists for an afternoon.
