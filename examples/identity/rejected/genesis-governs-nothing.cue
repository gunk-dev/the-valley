// The genesis entry governs the registry's own stream. One holding only a
// push grant leaves the registry in no hand at all.
package identity

boundaries: {
	"push": kind:     "git-push"
	"registry": kind: "registry"
}
genesis: "root"
principals: "root": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	grants: push: boundary: "push"
}
