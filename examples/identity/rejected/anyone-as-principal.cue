// anyone is reserved for the reader with no credential. An entry under
// principals by that name would give that reader keys and grants of every
// kind.
package identity

boundaries: "registry": kind: "registry"
genesis: "root"
principals: {
	"root": {
		kind: "human"
		keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
		grants: govern: boundary: "registry"
	}
	"anyone": {
		kind: "human"
		keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
		grants: govern: boundary: "registry"
	}
}
