---
type: decision
id: dcr-2965320
status: decided
title: "A valley's repository stores its governing state"
created: 2026-08-22
source: design conversation, 2026-08-22
---

# A valley's repository stores its governing state

A valley stores all its governing state in its repository. This includes its shared policy, called
the floor, and its identity registry. A valley that owns a host will also store that host's
declaration. The integrated revision of the repository is the source for the rules the valley sets
for its projects and the configuration compiled to enforce those rules. The shared core requirements
in [[dcr-9c75283]](dcr-9c75283-substrate-adoption-and-the-core.md) also apply through deployment.

## Policy changes take effect through integration

Changing access requires integrating a change to the repository. The rules in the current integrated
revision determine whether the next change may land. This also applies to changes to the rules
themselves.

The initial configuration establishes this process. The host that runs the enforcement services
remains the underlying trust and recovery boundary. The bootstrap mechanism is described in
[design/architecture.md](../../design/architecture.md).

An integrated policy change takes effect when the enforcement configuration is next compiled and
applied. A grant or revocation therefore affects subsequent access; it does not change which actions
were permitted in the past.

## The registry declares which keys are authorized

The identity registry stores public keys. A caller must prove possession of the corresponding
private key to act under that identity. Editing the registry cannot produce that proof.

An attacker who gains control of the registry could authorize a new key. That grant would appear as
a repository change and could be attributed through the integration record. It would not give the
attacker another principal's private key or the ability to produce that principal's signatures.

## Revocation limits future access

Write permissions are declared in the repository. Each grant is recorded in a commit and can be
revoked by a later change. Read permissions can also be revoked, but revocation only prevents future
reads. It cannot remove copies a reader already obtained.

Governing state should generally be public. In particular, outside readers need the public keys in
the registry to verify the system's signatures.

## The same rules cover other valleys and hosts

A valley grants access to a principal from another valley by integrating a change that identifies
that principal. Its floor constrains these grants in the same way it constrains other policy
changes.

The host that runs a valley's enforcement services has its own declaration. That declaration lives
in the repository of the valley that owns the host.

## Related

- [[dcr-b87f6e8]](dcr-b87f6e8-identity-is-a-governed-registry.md) — the registry from which access
  and verification configuration are compiled.
- [[dcr-9b5da04]](dcr-9b5da04-hosts-serve-isolated-valleys.md) — the host a valley's gates run on,
  whose declaration is itself governed state.
- [[dcr-e544f20]](dcr-e544f20-access-is-verbs-on-projects.md) — the operations that access grants
  authorize.
