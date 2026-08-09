// The worked identity registry: the gunk-dev instance as it stands, written
// against schema/identity.cue. The compiler renders it into
// ../compiled/, which is what the enforcement boundaries read — so this
// pair is the schema's evidence, and the golden case of the identity-e2e
// check.
//
// The keys are the real published ones. Nothing here is a secret: a
// registry holds public halves only, which is why it is an ordinary
// document in the instance repository rather than a provisioned file.
package identity

boundaries: {
	// The host serving the instance's repositories. sshd is the gate: a key
	// in the compiled authorized_keys may push, and one that is not there
	// cannot reach the repositories at all.
	"classic-laddie-push": kind: "git-push"

	// The integration path that admits a change to this document.
	"registry": kind: "registry"
}

genesis: "patrick"

principals: {
	// The operator. Five keys across four machines, and the signing name
	// varies per key rather than per entry: two keys on makers-mark sign
	// under that machine's attestations name, the plain key on
	// classic-laddie signs under the operator's own name, and the two that
	// sign nothing push and never attest.
	"patrick": {
		kind: "human"
		keys: [
			{ // makers-mark, ubuntu
				class:  "ssh-ed25519"
				bound:  "host"
				public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILc8u2oEFD+sn9vmX0gEbf62V4fmHGSvu10ENPkci3Yd"
				signs:  "makers-mark/attestations"
			},
			{ // makers-mark, nixos
				class:  "ssh-ed25519"
				bound:  "host"
				public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8B2eVhu/TpXZPyOt/6w0ELdtO6X6cTiWz3CvofxDCR"
				signs:  "makers-mark/attestations"
			},
			{ // classic-laddie
				class:  "ssh-ed25519"
				bound:  "host"
				public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGCxVUxXoyFYV40QureqqSMSA17CvK9IrFB33BA6UOip"
				signs:  "patrick"
			},
			{ // cloud-ssh
				class:  "ssh-ed25519"
				bound:  "host"
				public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHo0Oc728AfV2EMn30DhTWSqdWhmY8xR6np/qf6U7xvn"
			},
			{ // weller
				class:  "ssh-ed25519"
				bound:  "host"
				public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICkzfSlbK9YX3KztdvtgfyJelixdI6QN3c41eme9HOWv"
			},
		]
		grants: {
			push: boundary:   "classic-laddie-push"
			govern: boundary: "registry"
		}
	}

	// A scratch machine: it clones and pushes, decrypts nothing, and holds
	// its raw key until the certificate issuance service exists. Hence the
	// expiry, which is what makes the compilation a revocation already
	// scheduled (bd-8a591dc).
	"stoned-flynn": {
		kind: "machine"
		keys: [{
			class:  "ssh-ed25519"
			bound:  "host"
			public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAvpmbUKEDVsejgv2vxWaY/t4xl0JNnjFswb9SxcG9GG"
		}]
		grants: push: boundary: "classic-laddie-push"
		expires: "2026-10-01"
	}

	// The host itself, signing the execution claims `attest run` produces on
	// it. It pushes nowhere, so it holds no grant — and it is still in the
	// compiled known-signers, because that artifact is every
	// attestation-capable key rather than the holders of some grant.
	"classic-laddie": {
		kind: "machine"
		keys: [{
			class:  "ssh-ed25519"
			bound:  "host"
			public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIE7/mipW9wcQwVlDmEqBZksGDO3BEG94gb6VBuyDJUgk"
			signs:  "classic-laddie/attestations"
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
			public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINyd1zTLxcPuMDojZpjOdUxGj6AYl6RP7yiWb3Cd/fOQ"
			signs:  "integrator"
		}]
		expires: "2027-06-01"
	}
}
