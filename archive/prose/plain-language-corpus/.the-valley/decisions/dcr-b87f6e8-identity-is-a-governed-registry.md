---
type: decision
id: dcr-b87f6e8
status: decided
title: "Declare identities, keys, and permissions in a governed registry"
created: 2026-08-06
source: design conversation, 2026-08-06
---

# Declare identities, keys, and permissions in a governed registry

A valley declares the people, machines, and services it recognizes in a CUE registry in its
repository. Each named actor, called a **principal**, has keys, permissions, and any required expiry
date. Registry changes take effect through integration.

The registry establishes what an identity may do in this valley. Authentication requires proof of
possession of a registered private key.

## Registry changes and entries

Granting access, rotating a key, and revoking access are ordinary versioned changes. The registry's
path class requires human approval under the policy model in
[[dcr-f41f718]](dcr-f41f718-declared-verification-policy.md) and
[[ida-b7025b5]](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md).

[schema/identity.cue](../../schema/identity.cue) defines the implemented entry format.
[examples/identity/](../../examples/identity/) provides a worked registry.

Signing names belong to individual keys. The signed-note format binds a name and public key into a
key hash, as specified in [[dcr-de9d996]](dcr-de9d996-statement-text-and-signed-note.md). A
principal using different signing names on two machines therefore has two verifier-key entries.
Compiling the wrong name would prevent the verifier from matching the signature.

Agent runs have no registry entry or key of their own. They act under delegated authority from a
registered principal, with the run and delegation recorded as provenance.
[[ida-a8243d2]](../ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md) and
[[ida-45178f6]](../ideas/ida-45178f6-agent-identity-is-provenance.md) describe that division.

## Permissions must be enforced

A permission is meaningful only if some part of the running system checks it before allowing an
action. Examples include SSH access to repositories and authorization to publish on the bus. A
deployment must reject a declared permission it cannot enforce.

[[dcr-e544f20]](dcr-e544f20-access-is-verbs-on-projects.md) defines the current access model in
terms of verbs such as fetch, push, request, and govern. Infrastructure settings are generated from
those declarations. The implemented registry schema still expresses grants through named enforcement
boundaries; the verb-oriented declaration is the intended interface.

A valley may define reusable permission bundles and membership sets as its own configuration. The
shared schema does not prescribe role names.

The initial **genesis** principal is created with the valley and holds governance of the registry's
own stream. Shared identity constraints require expiry for external principals and prohibit them
from holding registry governance. These are requirements on entries, independent of local principal
names.

## External identities

An external collaborator can be identified by a domain handle or decentralized identifier that the
person already controls. Onboarding verifies that reference, and others must be able to verify it
again.

Each valley independently grants access to that identity. This supports the cross-valley identity
proposal in [[ida-8482624]](../ideas/ida-8482624-federation-groups.md).

## Web authentication and human approval

A web interface authenticates against a registry key by checking a signature over a fresh challenge,
then issuing a short session. The intended design uses no separate username and password store.
[[ida-23aa413]](../ideas/ida-23aa413-project-surfaces-are-derived-views.md) describes the interface.

For human credentials that satisfy the physical-approval requirement, the chosen key class must
provide hardware binding and evidence of user presence. A browser-mediated assertion is a candidate
mechanism. Binding its challenge to a statement digest could support approval of that statement.

The approval envelope and its relationship to signed notes remain open. Describing a credential as a
passkey does not, by itself, specify all of the binding and presence properties required here.

## Credential lifetime

Long-lived credentials must be asymmetric and bound to human-held hardware or a machine host.
Authentication uses the private key to produce a signature; the private key is not sent to the
verifier.

Machine and service entries using raw keys require expiry dates. Successful registry compilation
omits expired entries from generated access artifacts. Access removal therefore occurs through
convergence after expiry, rather than at the instant the date passes.

Short-lived certificates issued from registry entries are the intended next step for machine access.
Their issuance service and authentication mechanism remain deferred.

Any bearer secrets still required during migration must be recorded as temporary liabilities with
expiry. This keeps them visible until the platforms requiring them can be removed.
[[bd-8a591dc]](../bugs/bd-8a591dc-machine-credentials-never-expire.md) tracks the
credential-lifetime problem.

## Compilation on the host

The [identity compiler](../../identity/) reads the valley repository's integrated revision and
produces two artifacts:

- A known-signers file containing the registry's attestation-capable verifier keys. Both the
  integrator and `attest verify --known-keys` can use it. It includes the integrator's own key.
- Authorized keys for the Git access boundary, with each key tagged by its principal name.

An unintegrated registry edit grants no access. Schema errors, policy violations, repository-access
failures, and loss of every unexpired governing key must leave the last valid artifacts in place.
This prevents a failed render from emptying the access files. It also means failed convergence can
delay removal of expired or revoked access.

During the initial deployment, generated keys supplement keys declared directly by the host. Those
declared keys provide a recovery path.

Bus credentials should compile from the same registry when bus authentication is implemented.
[[bd-500adf7]](../bugs/bd-500adf7-bus-shares-git-user.md),
[[bd-d853d9c]](../bugs/bd-d853d9c-bus-unauthenticated.md), and
[[dcr-62ecc36]](dcr-62ecc36-signal-contracts.md) cover service isolation, bus authentication, and
subject permissions.

## Identity services

The registry is the valley's authority for identity and access. If an integration requires an
identity-provider protocol, an adapter can expose registry-derived data through that protocol. It
must not become a separate account authority.

## Open questions

- How are short-lived machine certificates issued, and how is an issuance request authenticated?
- How does a presence-proving approval compose with the signed-note envelope?
- How is governance recovered after compromise of the genesis key?
- What does a signed dispatch authorization identify, and how does it start a delegation chain?
