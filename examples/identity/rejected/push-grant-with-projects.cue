// Push is compiled host-wide: per-project access is not honestly
// enforceable over a shared git user (dcr-0f5d9b1). A project scope on a
// push grant would be a scope nothing checks.
package identity

boundaries: {
	"registry": kind: "registry"
	"push": kind:     "git-push"
}
genesis: "root"
principals: "root": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	grants: {
		govern: boundary: "registry"
		push: {boundary: "push", projects: app: "9da6809f038df92138c131cd16bed76d"}
	}
}
