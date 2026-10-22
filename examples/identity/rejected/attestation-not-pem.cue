// An attestation's certificates are PEM, one certificate each and nothing
// else. Base64 without its PEM lines is not one.
package identity

boundaries: "registry": kind: "registry"
genesis: "root"
principals: "root": {
	kind: "human"
	keys: [{
		class:  "ssh-ed25519"
		bound:  "hardware"
		public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIZNSh7kPQN53thBJoBVdGUpUJAkQAb4nsNo/pYJGVIX"
		attestation: {
			slot: "MIICJzCCAQ+gAwIBAgIQATCsihQMzzFrSCbhTl5GojANBgkqhkiG9w0BAQsFADAi"
			f9:   "MIIC9TCCAd2gAwIBAgIJAJXcF5k7Lbf8MA0GCSqGSIb3DQEBCwUAMCUxIzAhBgNV"
		}
	}]
	grants: govern: boundary: "registry"
}
