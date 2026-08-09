// A plus separates the fields of a verifier key, so a signing name holding
// one compiles a line nothing can parse (attest/sign.go).
package identity

boundaries: "registry": kind: "registry"
genesis: "root"
principals: "root": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA", signs: "root+1"}]
	grants: govern: boundary: "registry"
}
