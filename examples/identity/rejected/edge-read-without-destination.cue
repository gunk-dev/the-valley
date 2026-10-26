// An edge read boundary serves one destination, which it names.
package identity

boundaries: {
	"registry": kind: "registry"
	"web": kind:      "edge-read"
}
genesis: "root"
principals: "root": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	grants: {
		govern: boundary: "registry"
		read: {boundary: "web", projects: "all"}
	}
}
