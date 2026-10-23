// An example registry enrolling a YubiKey approval key, written against
// schema/identity.cue. The principal and both keys are examples: copy the
// shape, never the values.
//
// example-operator holds two keys. The first stands for a key generated
// in PIV slot 9c of a YubiKey with touch policy always, with the
// attestation that proves it. The second stands for an ordinary key on a
// laptop. Both keys' private halves are derived from published seeds, so
// neither is a secret.
//
// The compiler refuses this registry. The attestation chains to a
// test-only root, "the-valley example attestation root, not Yubico", and
// not to Yubico Attestation Root 1, so it does not verify. An entry made
// from this one compiles only once both certificates are the ones the
// enrolling YubiKey wrote: `yubico-piv-tool -a attest -s 9c` for slot, and
// `yubico-piv-tool -a read-certificate -s f9` for f9. The identity-e2e
// check compiles this registry and holds the compiler to that refusal.
package identity

boundaries: {
	"push":     kind: "git-push"
	"registry": kind: "registry"
	"approve":  kind: "approve"
}

genesis: "example-operator"

principals: {
	"example-operator": {
		kind: "human"
		keys: [
			{ // the example YubiKey key, PIV slot 9c
				class:  "ssh-ed25519"
				bound:  "hardware"
				public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAID/3+vgQhY5cdQ4AqspQ1A2yW60nn4j1FOyx04WfuQjc"
				attestation: {
					slot: """
						-----BEGIN CERTIFICATE-----
						MIIBnzCCAVGgAwIBAgIBAzAFBgMrZXAwPTE7MDkGA1UEAxMydGhlLXZhbGxleSBl
						eGFtcGxlIGY5IGNlcnRpZmljYXRlLCBub3QgYSBZdWJpS2V5J3MwIBcNMjQxMjAx
						MDAwMDAwWhgPOTk5OTEyMzEyMzU5NTlaMEIxQDA+BgNVBAMTN3RoZS12YWxsZXkg
						ZXhhbXBsZSBzbG90IDljIGNlcnRpZmljYXRlLCBub3QgYSBZdWJpS2V5J3MwKjAF
						BgMrZXADIQA/9/r4EIWOXHUOAKrKUNQNslutJ5+I9RTssdOFn7kI3KNvMG0wHwYD
						VR0jBBgwFoAUyUoEX5UuH7szTIEPGV+za/s50oYwEQYKKwYBBAGCxAoDAwQDBQcE
						MBQGCisGAQQBgsQKAwcEBgIEALxhTjAQBgorBgEEAYLECgMIBAIDAjAPBgorBgEE
						AYLECgMJBAEDMAUGAytlcANBAL3VEws3t6SNeFJXeiEOMHpdJkaFtDLvNJT/II4E
						v0tMQ6pKimp/cI7lfdDBxASGFaft4vkUyg+3W5Qdadn9rQg=
						-----END CERTIFICATE-----
						"""
					f9: """
						-----BEGIN CERTIFICATE-----
						MIIBoTCCAVOgAwIBAgIBAjAFBgMrZXAwOjE4MDYGA1UEAxMvdGhlLXZhbGxleSBl
						eGFtcGxlIGF0dGVzdGF0aW9uIHJvb3QsIG5vdCBZdWJpY28wIBcNMjQxMjAxMDAw
						MDAwWhgPOTk5OTEyMzEyMzU5NTlaMD0xOzA5BgNVBAMTMnRoZS12YWxsZXkgZXhh
						bXBsZSBmOSBjZXJ0aWZpY2F0ZSwgbm90IGEgWXViaUtleSdzMCowBQYDK2VwAyEA
						Meen8osipGPgf/7tu2vDRTLuy64wglsPaCcoQx/mPfujeTB3MA4GA1UdDwEB/wQE
						AwICBDASBgNVHRMBAf8ECDAGAQH/AgEAMB0GA1UdDgQWBBTJSgRflS4fuzNMgQ8Z
						X7Nr+znShjAfBgNVHSMEGDAWgBQijekYIu2T50x1FfsEQsCgXG6MaTARBgorBgEE
						AYLECgMDBAMFBwQwBQYDK2VwA0EAwNl9U1R6NYF1Bhr5E0zTN2iIkONbtq8zqO5E
						gNmETkG/yax7c2cuGo6uw/9egP5AT1EfGH1quK7VVyW6peIbBw==
						-----END CERTIFICATE-----
						"""
				}
			},
			{ // the example laptop key
				class:  "ssh-ed25519"
				bound:  "host"
				public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDgoIskk25f2EYhLkPeJniiairVGv46CCYt2CUZ8EoxH"
			},
		]
		grants: {
			push: boundary:    "push"
			approve: boundary: "approve"
			govern: boundary:  "registry"
		}
	}
}
