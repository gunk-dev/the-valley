// anyone is the reader who presents no credential, so it holds no key.
package identity

boundaries: {
	"registry": kind: "registry"
	"web": {kind: "edge-read", destination: "example.org"}
}
genesis: "root"
principals: "root": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	grants: govern: boundary: "registry"
}
anyone: {
	keys: [{class: "ssh-ed25519", bound: "host", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	grants: read: {boundary: "web", projects: "all"}
}
