// A grant may exist only where a boundary checks it, so a grant naming a
// boundary the instance does not run is refused rather than compiled into
// nothing.
package identity

boundaries: "registry": kind: "registry"
genesis: "root"
principals: "root": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	grants: {
		govern: boundary: "registry"
		publish: boundary: "bus"
	}
}
