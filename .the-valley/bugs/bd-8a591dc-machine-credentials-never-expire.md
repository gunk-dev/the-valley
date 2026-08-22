---
type: bug
id: bd-8a591dc
status: closed
title: Machine access is a permanent grant — provisioning is O(machines) and revocation never happens
created: 2026-07-31
source: provisioning a disposable agent VM against the pilot host, 2026-07-31
---

# Machine access is a permanent grant

Granting a machine access to a valley host meant appending its public key to
`services.valley.authorizedKeys` and rebuilding the host. The grant had no expiry, so it persisted
until someone edited the list again. Nothing observed that the machine it was issued for had ceased
to exist, and a disposable machine left its key behind when it went.

Two costs followed, and they compounded as the number of machines grew.

Provisioning was O(machines) with a human deploy in the loop. Each new machine needed a change to
the host's declaration, review, a merge, and an interactive rebuild of the host. That was a deploy
of the primary git host to grant read access to a scratch machine — an expensive motion to repeat,
and one performed on the machine whose availability everything else depends on.

Revocation was the harder cost, because it never happened on its own. A key list only shrank when a
human noticed an entry was dead and removed it, and nothing prompted that noticing. Ephemeral
machines are exactly the case where the grant outlives the grantee, so the list accreted standing
grants to destroyed hosts. Each entry reached every project on the host, because access was
host-level by design.

## The resolution

The identity registry ([[dcr-b87f6e8]]
([dcr-b87f6e8-identity-is-a-governed-registry.md](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md)))
replaces the hand-maintained key list. Machine and service entries carry mandatory expiry, and
expiry is enforced at compilation: the step that renders the registry into the artifacts a boundary
checks — the host's authorized keys among them — refuses expired entries. Access ends at the first
convergence after expiry, with no separate revocation act. Provisioning a machine is now a registry
entry rather than a host declaration, so granting it no longer requires a rebuild of the primary
host for each machine.

The compiler is [identity/](../../identity/), staged first against the host's push boundary — the
same boundary this bug was raised against. A compilation that fails for any reason leaves the last
good artifacts as they were, so a bad registry edit cannot lock the host's git user out; the
declared keys remain the way back in.

## Why the risk was tolerable before this landed

Every key in the list belonged to the operator, the host was reachable only over a private tailnet,
and the projects were small in number. A stale entry was a key nobody held on a host nobody else
could route to. The defect was one of scale and hygiene, not a live exposure.

## Related

The instance-level counterpart is qinling's `dcr-34b3f6d` (keys partition by capability), which
observes that the pilot host's key inventory conflates decryption, authentication, and human
signing, and wants it partitioned by capability. Splitting a git-only class out of the inventory's
user list — done for the machine that surfaced this bug — separates authentication from decryption
for one member, and was a down payment on that partition rather than a substitute for it.

Short-lived certificates issued per instance are the direction machine access is still moving
toward; the issuance service and what authenticates an issuance request remain open in
[[dcr-b87f6e8]]
([dcr-b87f6e8-identity-is-a-governed-registry.md](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md)).
