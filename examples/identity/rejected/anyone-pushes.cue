// anyone holds read grants alone. A push grant would admit a reader who
// presents no credential to a boundary that acts on who pushed.
package identity

boundaries: {
	"registry": kind: "registry"
	"push": kind:     "git-push"
}
genesis: "root"
principals: "root": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	grants: govern: boundary: "registry"
}
anyone: grants: push: boundary: "push"
