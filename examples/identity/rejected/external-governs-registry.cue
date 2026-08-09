// No external principal holds registry governance: it would put the
// instance's own membership in a hand the instance does not control.
package identity

boundaries: "registry": kind: "registry"
genesis: "root"
principals: {
	"root": {
		kind: "human"
		keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
		grants: govern: boundary: "registry"
	}
	"visitor": {
		kind: "human"
		keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
		external: {kind: "did", handle: "did:example:123"}
		grants: govern: boundary: "registry"
		expires: "2027-01-01"
	}
}
