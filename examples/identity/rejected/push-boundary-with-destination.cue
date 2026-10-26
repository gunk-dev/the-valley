// Only an edge read boundary serves a destination. A destination on any
// other boundary would be a scope nothing checks.
package identity

boundaries: {
	"registry": kind: "registry"
	"push": {kind: "git-push", destination: "example.org"}
}
genesis: "root"
principals: "root": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	grants: {
		govern: boundary: "registry"
		push: boundary:   "push"
	}
}
