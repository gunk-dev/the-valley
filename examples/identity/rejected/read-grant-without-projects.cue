// A read grant names the projects it covers. Every project is the explicit
// scope "all", never a grant that names nothing.
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
		read: boundary:   "web"
	}
}
