// A registry with a machine entry past its expiry, for the case the worked
// example cannot show: what a compilation does when an entry has aged out.
// The machine both signs and pushes, so its keys leave both artifacts at
// once, the omission is said out loud, and everything else still compiles.
// That is the enforcement dcr-b87f6e8 names — access ends at the first
// convergence after expiry — and it needs no revocation step at all.
package identity

boundaries: {
	"push": kind:     "git-push"
	"registry": kind: "registry"
}

genesis: "patrick"

principals: {
	"patrick": {
		kind: "human"
		keys: [{
			class:  "ssh-ed25519"
			bound:  "host"
			public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGCxVUxXoyFYV40QureqqSMSA17CvK9IrFB33BA6UOip"
			signs:  "patrick"
		}]
		grants: {
			push: boundary:   "push"
			govern: boundary: "registry"
		}
	}

	"stoned-flynn": {
		kind: "machine"
		keys: [{
			class:  "ssh-ed25519"
			bound:  "host"
			public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAvpmbUKEDVsejgv2vxWaY/t4xl0JNnjFswb9SxcG9GG"
			signs:  "stoned-flynn/attestations"
		}]
		grants: push: boundary: "push"
		expires: "2026-10-01"
	}
}
