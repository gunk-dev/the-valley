---
type: decision
id: dcr-b87f6e8
status: decided
title: "Identity is a governed registry: possession-proving keys, boundary-grounded grants, first-class expiry"
created: 2026-08-06
source: design conversation, 2026-08-06
---

# Identity is a governed registry

An instance's identities are a governed registry: one declared CUE document in the instance
repository, changed only through the integration path. A principal — a named actor the instance
grants something to: a human, a machine, a service — is an entry in that document, holding a name,
one or more keys, its grants, and where the kind warrants it an expiry. Keys prove possession, every
grant is grounded in an enforcement boundary that checks it, and expiry is a first-class property of
an entry. This node sits beside [[dcr-f41f718]]
([dcr-f41f718-declared-verification-policy.md](./dcr-f41f718-declared-verification-policy.md)) as a
sibling: the same schema-versus-instance split and the same floor machinery, applied to who may act.

## The registry

Granting, rotating, and revoking are ordinary changes — reviewed, landed, visible in history.
Registry changes are a path class in the sense of [[dcr-f41f718]]
([dcr-f41f718-declared-verification-policy.md](./dcr-f41f718-declared-verification-policy.md)): a
named set of paths carrying required checks, and for this class the required checks include human
approval ([[ida-b7025b5]]
([ida-b7025b5-human-decisions-are-signed-acts.md](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md))).

The schema is [schema/identity.cue](../../schema/identity.cue), and it is the authority on what an
entry may hold; the worked registry at [examples/identity/](../../examples/identity/) is the shape
read rather than imagined. An entry, illustratively:

```cue
principals: "runner-03": {
	kind: "machine"
	keys: [{
		class:  "ssh-ed25519"
		bound:  "host"
		public: "ssh-ed25519 AAAA…"
		signs:  "runner-03/attestations"
	}]
	grants: fetch: boundary: "qinling-push"
	expires: "2026-09-01"
}
```

A key's signing name is recorded per key rather than per entry. The note format's key hash binds the
name to the key ([[dcr-de9d996]]
([dcr-de9d996-statement-text-and-signed-note.md](./dcr-de9d996-statement-text-and-signed-note.md))),
so one person signing from two machines under two names holds two verifier keys, and a compilation
that recorded the wrong name would write a line no verifier can match.

An agent run holds no entry. A run acts under authority delegated from a principal ([[ida-a8243d2]]
([ida-a8243d2-agent-runs-act-under-delegated-authority.md](../ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md)))
and is named by its provenance ([[ida-45178f6]]
([ida-45178f6-agent-identity-is-provenance.md](../ideas/ida-45178f6-agent-identity-is-provenance.md)));
the registry holds the top of that chain, never its links.

## The boundary rule

A grant is a named permission. An enforcement boundary is the point in the running system that
refuses an action when the grant is absent — the SSH server deciding whether a key may push, the bus
deciding whether a connection may publish. A grant may exist only where an enforcement boundary
checks it, and each grant names its boundary. The schema grows a capability when its gate ships,
never before. The test: if removing a name from the schema breaks no enforcement point, it was
ontology and never belonged.

Roles are not schema. A deployment may define named grant bundles and groups as instance content —
templates a registry embeds, compiled to what gates check — and the substrate never requires a role
name.

The one structural requirement is the genesis entry: created at instance birth, the anchor later
boundaries verify against, holding governance of the registry's own stream.

## Groups, bundles, and the floor

A grant bundle is a named set of grants; a group is a named set of members. Both are
instance-defined, and both compile into the same gate-checked artifacts as any entry. A grant's
scope is the boundary it names, and nothing narrower. A stream list — the streams of the instance a
grant covers — is the natural next narrowing, and the boundary rule holds it back: the one boundary
that exists is host-level, so a stream list would be a scope nothing checks. It arrives with the
first boundary that checks one. The floor — the substrate's mandatory minimum over every instance
registry, the counterpart of [[dcr-f41f718]]'s
([dcr-f41f718-declared-verification-policy.md](./dcr-f41f718-declared-verification-policy.md))
instance floor over project policy — mandates properties, never names: external principals carry
expiry, and no external principal holds registry governance.

## External identity is cited, not minted

An external collaborator's entry references an identity the person already controls — a domain
handle or a decentralized identifier — verified at onboarding and re-verifiable by anyone. The
registry decides what that identity may do here; it never becomes the person's identity. This is the
shape that the open cross-group identity mapping of [[ida-8482624]]
([ida-8482624-federation-groups.md](../ideas/ida-8482624-federation-groups.md)) slots into.

## Web authentication is a registry key class

A passkey is an asymmetric, hardware-bound, presence-proving credential: a key pair whose private
half lives in hardware the person holds and signs only on a physical gesture, with the browser
supplying the ceremony. It is the same class of credential as a hardware SSH key. The registry holds
a passkey like any key; a web surface ([[ida-23aa413]]
([ida-23aa413-project-surfaces-are-derived-views.md](../ideas/ida-23aa413-project-surfaces-are-derived-views.md)))
authenticates by assertion — the hardware signing a fresh challenge — against the registry, and
issues a short session. There is no username or password store anywhere.

A presence assertion whose challenge is the digest of a statement is a hardware signature over that
statement. That is the candidate mechanism for the human approval gate ([[ida-b7025b5]]
([ida-b7025b5-human-decisions-are-signed-acts.md](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md)));
its envelope binding is deliberately open.

## Long-lived credentials prove possession

A bearer secret is a credential that authenticates whoever presents it — a token or password whose
possession is the entire proof, copyable without trace. Bearer secrets are rejected. Long-lived
credentials are asymmetric and bound — to hardware for humans, to a host for machines. They prove
possession: the private half never travels, and authentication is a signature only its holder could
produce.

Machines hold no stored long-lived secrets beyond their bound keys. The direction for machine access
is short-lived certificates derived from registry entries, issued per instance; that issuance
service is deliberately deferred. For now machine and service entries carry their raw keys with
mandatory expiry, and expiry is enforced at compilation: the step that renders the registry into
gate artifacts refuses expired entries, so access ends at the first convergence after expiry.
Revocation is the default, not an act of hygiene — this resolves [[bd-8a591dc]]
([bd-8a591dc-machine-credentials-never-expire.md](../bugs/bd-8a591dc-machine-credentials-never-expire.md)).

Bearer secrets the transition still requires are inventoried in the registry as liabilities with
expiry, so they stay visible and lintable until their platforms are exited.

## No identity provider at the root

The registry is the root of identity for the instance. An external identity platform — outsourced or
self-hosted — would reintroduce the account authority the registry replaces. Where an integration
someday demands such a protocol, it can be a facade compiled from the registry, never the source.

## The staged path: qinling first

The first enforcement compilation is the existing push boundary: the registry renders the host's
authorized keys, replacing the hand-maintained list. That begins the service split of [[bd-500adf7]]
([bd-500adf7-bus-shares-git-user.md](../bugs/bd-500adf7-bus-shares-git-user.md)) and makes expiry
real. Bus credentials compile from the same entries when the authentication of [[bd-d853d9c]]
([bd-d853d9c-bus-unauthenticated.md](../bugs/bd-d853d9c-bus-unauthenticated.md)) ships, per the
namespace [[dcr-62ecc36]] ([dcr-62ecc36-signal-contracts.md](./dcr-62ecc36-signal-contracts.md))
carved.

The compiler is [identity/](../../identity/), and it renders two artifacts. One is the known-signers
file: every attestation-capable key of the registry, written as the note format's verifier keys.
That single artifact serves both the integrator's acceptance list and any reader's
`attest verify --known-keys`, so it holds the integrator's own key like any other. The other is the
git user's authorized keys, one tagged line per key of every principal holding a grant at a push
boundary — the tagged-key shape the host module already reads a principal name off.

It runs on the host, reading the registry from the served instance repository's integrated tip, the
same provenance the verification floor has ([[dcr-f41f718]]
([dcr-f41f718-declared-verification-policy.md](./dcr-f41f718-declared-verification-policy.md))): a
registry edit governs nothing until it lands. A compilation that fails for any reason — a schema
violation, a floor violation, an unreachable repository — leaves the last good artifacts exactly as
they were, because a compiler that emptied them would lock the host's git user out. Access is
therefore additive over what the host declares by hand, and the declared keys are the way back in.

## Open

- The certificate issuance service: its design, and what authenticates an issuance request — the
  tailnet composition.
- The presence-assertion envelope for approvals, and how it composes with the signatures of the note
  format ([[dcr-de9d996]]
  ([dcr-de9d996-statement-text-and-signed-note.md](./dcr-de9d996-statement-text-and-signed-note.md))).
- Re-rooting after a genesis-key compromise.
- What a dispatch authorization names, and its signature — the top of the delegation chain
  ([[ida-a8243d2]]
  ([ida-a8243d2-agent-runs-act-under-delegated-authority.md](../ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md)))
  as a first-class signed act; discussed, not settled.

## Related

- [[dcr-f41f718]]
  ([dcr-f41f718-declared-verification-policy.md](./dcr-f41f718-declared-verification-policy.md)) —
  the sibling decision: the same declared-in-CUE, floor-plus-instance machinery, over verification
  policy.
- [[ida-b7025b5]]
  ([ida-b7025b5-human-decisions-are-signed-acts.md](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md))
  — the human approval registry changes gate on, and the act a presence assertion carries.
- [[ida-a8243d2]]
  ([ida-a8243d2-agent-runs-act-under-delegated-authority.md](../ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md))
  and [[ida-45178f6]]
  ([ida-45178f6-agent-identity-is-provenance.md](../ideas/ida-45178f6-agent-identity-is-provenance.md))
  — agent runs hold no entry; the registry names the chain's top.
- [[ida-8482624]] ([ida-8482624-federation-groups.md](../ideas/ida-8482624-federation-groups.md)) —
  the group-instance binding the registry lives inside, and the cross-group identity mapping this
  gives a shape to.
- [[ida-23aa413]]
  ([ida-23aa413-project-surfaces-are-derived-views.md](../ideas/ida-23aa413-project-surfaces-are-derived-views.md))
  — the web surface that authenticates against the registry.
- [[dcr-62ecc36]] ([dcr-62ecc36-signal-contracts.md](./dcr-62ecc36-signal-contracts.md)) — the bus
  subject namespace registry-compiled credentials fill.
- [[bd-8a591dc]]
  ([bd-8a591dc-machine-credentials-never-expire.md](../bugs/bd-8a591dc-machine-credentials-never-expire.md)),
  [[bd-500adf7]] ([bd-500adf7-bus-shares-git-user.md](../bugs/bd-500adf7-bus-shares-git-user.md)),
  [[bd-d853d9c]] ([bd-d853d9c-bus-unauthenticated.md](../bugs/bd-d853d9c-bus-unauthenticated.md)) —
  the credential bugs this resolves, begins, and serves.
