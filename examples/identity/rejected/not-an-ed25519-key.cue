// ed25519 is the only algorithm the note format signs with and the only one
// an entry may carry.
package identity

boundaries: "registry": kind: "registry"
genesis: "root"
principals: "root": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQ"}]
	grants: govern: boundary: "registry"
}
