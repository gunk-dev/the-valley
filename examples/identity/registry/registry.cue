// The worked identity registry: an example instance, written against
// schema/identity.cue. The compiler renders it into
// ../compiled/, which is what the enforcement boundaries read — so this
// pair is the schema's evidence, and the golden case of the identity-e2e
// check.
//
// The keys were generated for this example, and their private halves were
// discarded. Nothing in a registry is a secret: it holds public halves
// only, which is why it is an ordinary document in the instance repository
// rather than a provisioned file.
package identity

boundaries: {
	// The host serving the instance's repositories. sshd is the gate: a key
	// in the compiled authorized_keys may push, and one that is not there
	// cannot reach the repositories at all.
	"valley-host-push": kind: "git-push"

	// The integration path that admits a change to this document.
	"registry": kind: "registry"

	// The same host's pre-receive hook, deciding who may write an
	// integration request. A key it does not find among the holders cannot
	// file one.
	"valley-host-request": kind: "request"

	// The web edge at example.org: the publisher sends it what the read
	// grants here, and each project's own policy/read, let anyone read. A
	// read grant compiles into no artifact; the publisher reads it from
	// this document.
	"web": {
		kind:        "edge-read"
		destination: "example.org"
	}
}

// The reader with no credential may read app at the edge, by the
// publication identity app's policy/read declares. app is public there
// because its own policy/read names anyone too. site is not named, so it is
// public nowhere, whatever its policy says.
anyone: grants: read: {
	boundary: "web"
	projects: app: "9da6809f038df92138c131cd16bed76d"
}

genesis: "operator"

principals: {
	// The operator. Five keys across four machines, and the signing name
	// varies per key rather than per entry: two keys on the workstation sign
	// under that machine's attestations name, the plain key on
	// valley-host signs under the operator's own name, and the two that
	// sign nothing push and never attest. The operator files integration
	// requests, so the entry holds request beside push. The operator reads
	// every project at the edge whose own policy/read names the operator,
	// as any reader would.
	"operator": {
		kind: "human"
		keys: [
			{ // workstation, first system
				class:  "ssh-ed25519"
				bound:  "host"
				public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINeHRYwKqCETPj0ZZ/+B5c5Julf3dJZclYZaRWixQs1D"
				signs:  "workstation/attestations"
			},
			{ // workstation, second system
				class:  "ssh-ed25519"
				bound:  "host"
				public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIORtFu8o9epEZ6UPhN7EfghFnP0gUTrvuZxdSwlHGGWQ"
				signs:  "workstation/attestations"
			},
			{ // valley-host
				class:  "ssh-ed25519"
				bound:  "host"
				public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMsWYyDSCNBwb6MnvQ7sfu++kZold+0HuP5leY4IiKnV"
				signs:  "operator"
			},
			{ // cloud-shell
				class:  "ssh-ed25519"
				bound:  "host"
				public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFtx7r+B/QI75Oohpr5+TQIEmTBstSuby0rsG9d4T8vx"
			},
			{ // laptop
				class:  "ssh-ed25519"
				bound:  "host"
				public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA2HH5cIC6Nmby7aN2bEOxeF6pe4EYvtOLTiLdztbQFG"
			},
		]
		grants: {
			push: boundary:    "valley-host-push"
			request: boundary: "valley-host-request"
			govern: boundary:  "registry"
			read: {
				boundary: "web"
				projects: "all"
			}
		}
	}

	// A scratch machine: it clones and pushes topic branches, files no
	// request, decrypts nothing, and holds its raw key until the
	// certificate issuance service exists. Hence the expiry, which is what
	// makes the compilation a revocation already scheduled (bd-8a591dc).
	"agent-vm": {
		kind: "machine"
		keys: [{
			class:  "ssh-ed25519"
			bound:  "host"
			public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIO0AEJMcSrXCOYKlyVrdVWG07RF2SIM/8gaI/8oDEMRI"
		}]
		grants: push: boundary: "valley-host-push"
		expires: "2026-10-01"
	}

	// The host itself, signing the execution claims `attest run` produces on
	// it. It pushes nowhere, so it holds no grant — and it is still in the
	// compiled known-signers, because that artifact is every
	// attestation-capable key rather than the holders of some grant.
	"valley-host": {
		kind: "machine"
		keys: [{
			class:  "ssh-ed25519"
			bound:  "host"
			public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGRtI+7TYX67rzFGIp3ZJEVLAnaL4SA2n5ipYkFqpgKS"
			signs:  "valley-host/attestations"
		}]
		expires: "2027-06-01"
	}

	// The integrator, signing its transfer statements under its own name.
	// No push grant: it reaches the repositories through the git group on
	// the host, not through sshd.
	"integrator": {
		kind: "service"
		keys: [{
			class:  "ssh-ed25519"
			bound:  "host"
			public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEGTRJkpUO9Z+nujKT+u5Ho7jcovPYu070iz6poQIKoI"
			signs:  "integrator"
		}]
		expires: "2027-06-01"
	}
}
