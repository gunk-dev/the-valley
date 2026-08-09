// Roles are not schema (dcr-b87f6e8): a deployment names grant bundles as
// instance content, and an entry carries no role field.
package identity

boundaries: "registry": kind: "registry"
genesis: "root"
principals: "root": {
	kind: "human"
	role: "admin"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	grants: govern: boundary: "registry"
}
