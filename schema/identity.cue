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
// Two tools read this file. The compiler (identity/) renders a registry
// into the artifacts the enforcement boundaries check — the known-signers
// file the integrator and `attest verify` read, the git user's
// authorized_keys, the grants the pre-receive hook checks, and the two
// approval signers files. The read-access reader (readaccess/) takes the
// read grants out of it for the web publisher, which checks them at the
// edge read boundary.
// Expiry is enforced by both rather than here, because a document has no
// clock: each omits an expired entry, so access ends at the first
// compilation or publication after expiry.
//
// Like the host, event and policy schemas, this file is deliberately not
// Nix. A registry names principals, keys and boundaries; it never names
// derivations, systems or store paths, so it travels between machines
// unchanged.
//
//   Validate a registry against this schema:
//     cue vet -c schema/identity.cue examples/identity/registry/*.cue
package identity

import "strings"

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

	// Evidence that the private half was made on a YubiKey and cannot
	// leave it. The compiler verifies it against the Yubico roots it
	// carries, and a key whose attestation verifies and says every
	// signature needs a touch is a hardware approval key. Without one, a
	// key is never a hardware approval key, whatever bound says. A key
	// with one is held on a device, so it is bound to hardware.
	attestation?: #Attestation
	if attestation != _|_ {bound: "hardware"}
}

// A YubiKey PIV attestation of one key: two certificates, each PEM as the
// YubiKey writes it. The schema holds their shape; the compiler checks
// what they say.
#Attestation: {
	// The certificate the YubiKey wrote for the slot holding the key:
	// `yubico-piv-tool -a attest -s 9c` for slot 9c.
	slot: #Certificate

	// The YubiKey's own attestation certificate, which signed the slot's:
	// `yubico-piv-tool -a read-certificate -s f9`.
	f9: #Certificate
}

// One PEM certificate and nothing else.
#Certificate: =~"^-----BEGIN CERTIFICATE-----\n([A-Za-z0-9+/=]+\n)+-----END CERTIFICATE-----\n?$"

// A grant is a named permission.
#Grant: {
	// The enforcement boundary that checks it: a key of `boundaries`. A
	// grant may exist only where a boundary refuses the action when the
	// grant is absent (dcr-b87f6e8), and naming the boundary is what makes
	// that checkable — a grant naming a boundary the instance does not run
	// fails vet.
	boundary: #Name

	// The projects a read grant covers. A grant at a boundary of kind
	// "edge-read" names them, and a grant at any other boundary does not:
	// the edge read boundary is the one boundary that checks a project
	// scope. Every other boundary is compiled host-wide, and push cannot be
	// anything else, because per-project access is not honestly enforceable
	// over a shared git user (dcr-0f5d9b1). A scope there would be a scope
	// nothing checks. readScopes below holds each grant to this.
	projects?: #Scope
}

// The projects a read grant covers: "all" for every project, or each
// project's name with the publication identity its own policy/read declares
// (schema/read.cue). A project whose policy declares another identity is not
// covered, so a new project that reuses a name does not inherit the old
// one's grants. An empty set covers no project. Neither form lets anyone
// read a project whose own policy/read does not name them.
#Scope: "all" | close({[#ProjectName]: #Identity})

// A project's name and publication identity, as schema/read.cue holds them.
#ProjectName: =~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$" & strings.MaxRunes(64)
#Identity:    =~"^[0-9a-f]{32}$"

// The reserved principal for a reader who presents no credential. It is a
// variant of its own, never an entry under principals: it holds no key, no
// credential and no external identity, and it does not expire. It holds
// read grants alone, so it never pushes, approves, restacks or governs the
// registry, and it is never the genesis entry. A key sshd admits without a
// principal tag pushes as no principal, and is not anyone.
#Anyone: {
	grants: [#Name]: #Grant
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
	//
	// "approve" — the pre-receive hook (valleyhook/) on a host serving the
	// instance's repositories, checking writes to the integration-request
	// namespace (refs/the-valley/integration-requests/*). This is the
	// approve grant, the request verb of dcr-e544f20. "request" is the same
	// kind under the name it had first, and is also accepted: a grant at a
	// boundary of either kind is the approve grant. The compiler writes
	// each holder into the grants file the hook reads, and the hook refuses
	// a principal it does not name there: it cannot file, replace or
	// withdraw a request. The grant rides on push — only a key sshd admits
	// reaches the hook — so a principal that files requests holds a
	// "git-push" grant too.
	//
	// "restack" — the same pre-receive hook, checking the two writes the
	// host restacker needs beyond an ordinary push: adding one version to
	// another principal's ready change record, on the branch and under the
	// title of its active version, and writing restack reports
	// (refs/the-valley/restack/*). The compiler writes each holder into the
	// grants file as "restack <principal>". The grant opens nothing else:
	// no other record transition, and no integration request. It rides on
	// push too.
	//
	// "edge-read" — the edge read boundary: the web publisher on a host
	// serving the instance's repositories (web/cmd/valley-web-publish),
	// and the web server at one destination, which serves what the
	// publisher sends it. A grant here is a read grant. It lets its holder
	// read the projects its scope covers, wherever a project's own
	// policy/read names the holder too (schema/read.cue). The publisher
	// sends the destination a project only when both consents let anyone
	// read it, and the server there authenticates no reader, so a read
	// grant to a named principal opens nothing at the edge.
	kind: "git-push" | "registry" | "approve" | "request" | "restack" | "edge-read"

	// The destination an edge read boundary serves: the name the publisher
	// is given as --destination and signs every manifest for. An edge read
	// boundary names one, and no other boundary does; readScopes below holds
	// each boundary to this.
	destination?: #Name
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
	if anyone != _|_ for _, g in anyone.grants {(g.boundary): _}
}

// The entries. anyone is reserved for the variant below, so an entry under
// that name fails vet naming principals.anyone.
principals: [#Name]: #Principal
principals: anyone?: "INVALID: anyone is reserved for the reader with no credential; its grants go under the top-level anyone"

// The read grants of a reader who presents no credential (#Anyone).
anyone?: #Anyone

// The genesis entry: created at instance birth, the anchor later boundaries
// verify against, and the one structural requirement of a registry.
genesis: #Name & !="anyone"

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

// Derived, and empty in every valid registry: the boundaries and grants
// whose read scope does not fit. An edge read boundary names its
// destination, and no other boundary names one. A grant at an edge read
// boundary names the projects it covers, and no other grant names any.
// anyone holds grants at edge read boundaries alone. Each boundary or grant
// that breaks a rule lands here as a sentinel string against a `bool` and
// fails vet naming it.
readScopes: [string]: bool
readScopes: {
	for bn, b in boundaries {
		if b.kind == "edge-read" && b.destination == _|_ {
			"\(bn)": "INVALID: an edge-read boundary names its destination"
		}
		if b.kind != "edge-read" && b.destination != _|_ {
			"\(bn)": "INVALID: only an edge-read boundary names a destination"
		}
	}
	for pn, p in principals for gn, g in p.grants {
		if boundaries[g.boundary].kind == "edge-read" && g.projects == _|_ {
			"\(pn).\(gn)": "INVALID: a grant at an edge-read boundary names the projects it covers"
		}
		if boundaries[g.boundary].kind != "edge-read" && g.projects != _|_ {
			"\(pn).\(gn)": "INVALID: only a grant at an edge-read boundary names projects"
		}
	}
	if anyone != _|_ for gn, g in anyone.grants {
		if boundaries[g.boundary].kind != "edge-read" {
			"anyone.\(gn)": "INVALID: anyone holds read grants alone, at edge-read boundaries"
		}
		if boundaries[g.boundary].kind == "edge-read" && g.projects == _|_ {
			"anyone.\(gn)": "INVALID: a grant at an edge-read boundary names the projects it covers"
		}
	}
}

// A file's top level cannot be closed (embedding a definition would open it
// instead), so reject stray top-level fields explicitly: anything but the
// registry's own fields conflicts with this sentinel and fails vet with an
// error naming the field.
[
	!="boundaries" &
	!="principals" &
	!="anyone" &
	!="genesis" &
	!="genesisGovernance" &
	!="externalGovernance" &
	!="readScopes",
]: "INVALID: unknown top-level field; only \"boundaries\", \"principals\", \"anyone\" and \"genesis\" are allowed"
