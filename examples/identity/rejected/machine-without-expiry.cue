// A machine holds its raw key, so its entry ends on a day: mandatory expiry
// is the floor's first property (dcr-b87f6e8, bd-8a591dc).
package identity

boundaries: "registry": kind: "registry"
genesis: "root"
principals: {
	"root": {
		kind: "human"
		keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
		grants: govern: boundary: "registry"
	}
	"runner": {
		kind: "machine"
		keys: [{class: "ssh-ed25519", bound: "host", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	}
}
