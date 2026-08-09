// An external principal is not this instance's to vouch for indefinitely.
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
		external: {kind: "domain", handle: "someone.example"}
	}
}
