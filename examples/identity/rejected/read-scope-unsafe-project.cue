// A read grant names projects by the project-name rule, each with a
// publication identity.
package identity

boundaries: {
	"registry": kind: "registry"
	"web": {kind: "edge-read", destination: "example.org"}
}
genesis: "root"
principals: "root": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	grants: {
		govern: boundary: "registry"
		read: {boundary: "web", projects: "../app": "9da6809f038df92138c131cd16bed76d"}
	}
}
