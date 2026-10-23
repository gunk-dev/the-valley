// An attestation's certificates are PEM, one certificate each and nothing
// else. Base64 without its PEM lines is not one.
package identity

boundaries: "registry": kind: "registry"
genesis: "root"
principals: "root": {
	kind: "human"
	keys: [{
		class:  "ssh-ed25519"
		bound:  "hardware"
		public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAID/3+vgQhY5cdQ4AqspQ1A2yW60nn4j1FOyx04WfuQjc"
		attestation: {
			slot: "MIIBnzCCAVGgAwIBAgIBAzAFBgMrZXAwPTE7MDkGA1UEAxMydGhlLXZhbGxleSBl"
			f9:   "MIIBoTCCAVOgAwIBAgIBAjAFBgMrZXAwOjE4MDYGA1UEAxMvdGhlLXZhbGxleSBl"
		}
	}]
	grants: govern: boundary: "registry"
}
