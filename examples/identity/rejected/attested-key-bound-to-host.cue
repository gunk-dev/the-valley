// A key with an attestation is held on a device, so it is bound to
// hardware. An entry saying host for an attested key contradicts itself.
package identity

boundaries: "registry": kind: "registry"
genesis: "root"
principals: "root": {
	kind: "human"
	keys: [{
		class:  "ssh-ed25519"
		bound:  "host"
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
	}]
	grants: govern: boundary: "registry"
}
