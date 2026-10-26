// The genesis entry anchors the registry and governs it. The reader with no
// credential can be neither.
package identity

boundaries: "registry": kind: "registry"
genesis: "anyone"
principals: "root": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	grants: govern: boundary: "registry"
}
