// The identity registry of a valley instance: who the instance grants
// something to, with which keys, and for how long. Identity is a governed
// registry (dcr-b87f6e8) — one declared CUE document in the instance
// repository, changed only through the integration path — and this file is
// its schema. The schema is the substrate's floor over every instance
// registry, and a floor mandates properties, never names: mandatory expiry
// where a raw key is held, no external principal governing the registry,
// and the genesis entry governing it. Which principals exist, which
// boundaries the instance runs, and what a grant is called are instance
// content.
//
// One tool reads this file: the compiler (identity/), which renders a
// registry into the artifacts the enforcement boundaries check — the
// known-signers file the integrator and `attest verify` read, and the git
// user's authorized_keys. Expiry is enforced there rather than here,
// because a document has no clock: the compiler omits an expired entry, so
// access ends at the first convergence after expiry.
//
// Like the host, event and policy schemas, this file is deliberately not
// Nix. A registry names principals, keys and boundaries; it never names
// derivations, systems or store paths, so it travels between machines
// unchanged.
//
//   Validate a registry against this schema:
//     cue vet -c schema/identity.cue examples/identity/registry/*.cue
package identity

// A principal is a named actor the instance grants something to.
#Principal: {
	// human: a person. machine: a computer acting for itself. service: a
	// program the instance runs. An agent run is none of these and holds no
	// entry — a run acts under authority delegated from a principal
	// (ida-a8243d2) and is named by its provenance (ida-45178f6), so the
	// registry holds the top of that chain and never its links.
	kind: "human" | "machine" | "service"

	// The keys that prove possession, at least one. A bearer secret — a
	// credential whose possession is the entire proof — is not a key and has
	// no place here (dcr-b87f6e8).
	keys: [#Key, ...#Key]

	// What this principal may do, keyed by grant name. The name is instance
	// content; what makes a grant real is the boundary it names.
	grants: [#Name]: #Grant

	// The identity an external collaborator already controls — a domain
	// handle or a decentralized identifier. It is cited, never minted: the
	// registry decides what that identity may do here, and never becomes the
	// person's identity. null for a principal of this instance.
	external: #External | *null

	// The first day this entry no longer counts. The compiler compares it
	// against the day it runs and omits the entry from every artifact from
	// that day on, so an expiry is a revocation already scheduled.
	expires?: #Date

	// Mandatory expiry, the floor's first property. A machine or service
	// holds its raw key until the certificate issuance service arrives
	// (dcr-b87f6e8), so the key it holds is one with an end; an external
	// principal is not this instance's to vouch for indefinitely.
	if kind != "human" {expires: #Date}
	if external != null {expires: #Date}
}

// One key of a principal.
#Key: {
	// ed25519 is the only algorithm the note format signs or verifies with
	// (attest/sign.go) and the only one sshd is asked to accept here. A
	// passkey is the same class of credential (dcr-b87f6e8) and becomes a
	// second class the day a surface authenticates against one.
	class: "ssh-ed25519"

	// What the private half is bound to: hardware a person holds, or one
	// host. Nothing here is bound to a file that travels.
	bound: "hardware" | "host"

	// The public half, exactly as `authorized_keys` writes it — the type,
	// base64 of the SSH wire format, and an optional comment.
	public: =~"^ssh-ed25519 [A-Za-z0-9+/]+={0,2}( [^\\n]*)?$"

	// The name this key signs notes under, when it signs at all. Per key and
	// not per entry: the note format's key hash binds the name to the key
	// (attest/sign.go), so one person signing from two machines under two
	// names holds two verifier keys, and recording the wrong name compiles a
	// line no verifier will ever match. A key without it pushes and never
	// attests.
	signs?: #SignerName
}

// A grant is a named permission.
#Grant: {
	// The enforcement boundary that checks it: a key of `boundaries`. A
	// grant may exist only where a boundary refuses the action when the
	// grant is absent (dcr-b87f6e8), and naming the boundary is what makes
	// that checkable — a grant naming a boundary the instance does not run
	// fails vet.
	boundary: #Name

	// A grant carries its boundary and nothing else. The decision names a
	// grant's scope as its stream list; the one boundary that exists today
	// is host-level, because per-project access is not honestly enforceable
	// over a shared git user (dcr-0f5d9b1), so a stream list here would be a
	// scope nothing checks. It arrives with the first boundary that checks
	// one — the bus's per-subject authorization (bd-d853d9c).
}

// An enforcement boundary: the point in the running system that refuses an
// action when the grant is absent.
#Boundary: {
	// What the enforcement point is.
	//
	// "git-push" — sshd on a host serving the instance's repositories. The
	// compiler renders the git user's authorized_keys from the grants at
	// this boundary, tagging each key with its principal's name, and the
	// pre-receive hook reads that name off the key that pushed
	// (nix/valley-host.nix).
	//
	// "registry" — the integration path that admits a change to the registry
	// document itself. Governance of the registry's own stream is a grant
	// here, and the required checks of that path class include human
	// approval (ida-b7025b5).
	kind: "git-push" | "registry"
}

// An identity the registry cites rather than mints.
#External: {
	// What kind of identity is being cited.
	kind: "domain" | "did"

	// The handle itself: a domain name, or a decentralized identifier. It is
	// verified at onboarding and re-verifiable by anyone.
	handle: =~"^[^\\s]+$"
}

// A calendar day, UTC.
#Date: =~"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"

// Principal, grant and boundary names. Held to the shape a principal name
// must have to ride into the pre-receive hook's environment
// (nix/valley-host.nix), because that is where a compiled name ends up.
#Name: =~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"

// A name a key signs under, held to what a signature line can carry
// (attest/sign.go): no space, because a space separates the name from the
// signature, and no plus, because a plus separates the fields of a verifier
// key.
#SignerName: =~"^[^\\s+]+$"

// ----------------------------------------------------------------------
// The registry.

// The enforcement boundaries this instance runs. Every grant names one of
// these, and the cross-check below adds any name a grant used, so a grant
// at a boundary the instance does not run fails vet as an incomplete
// boundary rather than compiling into nothing.
boundaries: [#Name]: #Boundary
boundaries: {
	for _, p in principals for _, g in p.grants {(g.boundary): _}
}

// The entries.
principals: [#Name]: #Principal

// The genesis entry: created at instance birth, the anchor later boundaries
// verify against, and the one structural requirement of a registry.
genesis: #Name

// Derived: the genesis entry's grants that govern the registry's own
// stream. A registry whose genesis governs nothing fails vet with an error
// naming this field — there would be no anchor, and no hand the registry
// itself is in.
genesisGovernance: [
	for gn, g in principals[genesis].grants
	if boundaries[g.boundary].kind == "registry" {gn},
]
genesisGovernance: [_, ...]

// Derived, and empty in every valid registry: the grants at a registry
// boundary held by a cited identity. The floor forbids them — an external
// principal governing the registry would put the instance's own membership
// in a hand the instance does not control — so each one lands here as a
// sentinel string against a `bool` and fails vet naming the principal and
// the grant.
externalGovernance: [string]: bool
externalGovernance: {
	for pn, p in principals if p.external != null
	for gn, g in p.grants if boundaries[g.boundary].kind == "registry" {
		"\(pn).\(gn)": "INVALID: an external principal may not hold registry governance"
	}
}

// A file's top level cannot be closed (embedding a definition would open it
// instead), so reject stray top-level fields explicitly: anything but the
// registry's own fields conflicts with this sentinel and fails vet with an
// error naming the field.
[
	!="boundaries" &
	!="principals" &
	!="genesis" &
	!="genesisGovernance" &
	!="externalGovernance"
]: "INVALID: unknown top-level field; only \"boundaries\", \"principals\" and \"genesis\" are allowed"
