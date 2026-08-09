// A stray top-level field is a fact the compiler would never read.
package identity

boundaries: "registry": kind: "registry"
genesis: "root"
principal: "root": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	grants: govern: boundary: "registry"
}
