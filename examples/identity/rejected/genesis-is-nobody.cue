// The genesis entry is an entry: naming a principal the registry does not
// hold is a registry with no anchor.
package identity

boundaries: "registry": kind: "registry"
genesis: "founder"
principals: "root": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	grants: govern: boundary: "registry"
}
