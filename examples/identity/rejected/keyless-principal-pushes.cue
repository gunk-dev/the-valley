// A principal with no keys reads through read links alone. A push grant
// would give a bearer link's holder more than reading.
package identity

boundaries: {
	"registry": kind: "registry"
	"push": kind:     "git-push"
	"web": {kind: "edge-read", destination: "example.org"}
}
genesis: "root"
principals: "root": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	grants: govern: boundary: "registry"
}
principals: "alice": {
	kind: "human"
	keys: []
	grants: {
		read: {boundary: "web", projects: "all"}
		push: boundary: "push"
	}
}
