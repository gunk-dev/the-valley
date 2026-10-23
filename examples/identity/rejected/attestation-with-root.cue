// An attestation carries the slot certificate and the f9 certificate, and
// nothing else. The roots it must verify to are the compiler's, so an
// entry cannot supply one of its own, even a copy of Yubico's.
package identity

boundaries: "registry": kind: "registry"
genesis: "holder"
principals: "holder": {
	kind: "human"
	keys: [{
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
			root: """
				-----BEGIN CERTIFICATE-----
				MIIDPjCCAiagAwIBAgIUXzeiEDJEOTt14F5n0o6Zf/bBwiUwDQYJKoZIhvcNAQEN
				BQAwJDEiMCAGA1UEAwwZWXViaWNvIEF0dGVzdGF0aW9uIFJvb3QgMTAgFw0yNDEy
				MDEwMDAwMDBaGA85OTk5MTIzMTIzNTk1OVowJDEiMCAGA1UEAwwZWXViaWNvIEF0
				dGVzdGF0aW9uIFJvb3QgMTCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEB
				AMZ6/TxM8rIT+EaoPvG81ontMOo/2mQ2RBwJHS0QZcxVaNXvl12LUhBZ5LmiBScI
				Zd1Rnx1od585h+/dhK7hEm7JAALkKKts1fO53KGNLZujz5h3wGncr4hyKF0G74b/
				U3K9hE5mGND6zqYchCRAHfrYMYRDF4YL0X4D5nGdxvppAy6nkEmtWmMnwO3i0TAu
				csrbE485HvGM4r0VpgVdJpvgQjiTJCTIq+D35hwtT8QDIv+nGvpcyi5wcIfCkzyC
				imJukhYy6KoqNMKQEdpNiSOvWyDMTMt1bwCvEzpw91u+msUt4rj0efnO9s0ZOwdw
				MRDnH4xgUl5ZLwrrPkfC1/0CAwEAAaNmMGQwHQYDVR0OBBYEFNLu71oijTptXCOX
				PfKF1SbxJXuSMB8GA1UdIwQYMBaAFNLu71oijTptXCOXPfKF1SbxJXuSMBIGA1Ud
				EwEB/wQIMAYBAf8CAQMwDgYDVR0PAQH/BAQDAgGGMA0GCSqGSIb3DQEBDQUAA4IB
				AQC3IW/sgB9pZ8apJNjxuGoX+FkILks0wMNrdXL/coUvsrhzsvl6mePMrbGJByJ1
				XnquB5sgcRENFxdQFma3mio8Upf1owM1ZreXrJ0mADG2BplqbJnxiyYa+R11reIF
				TWeIhMNcZKsDZrFAyPuFjCWSQvJmNWe9mFRYFgNhXJKkXIb5H1XgEDlwiedYRM7V
				olBNlld6pRFKlX8ust6OTMOeADl2xNF0m1LThSdeuXvDyC1g9+ILfz3S6OIYgc3i
				roRcFD354g7rKfu67qFAw9gC4yi0xBTPrY95rh4/HqaUYCA/L8ldRk6H7Xk35D+W
				Vpmq2Sh/xT5HiFuhf4wJb0bK
				-----END CERTIFICATE-----
				"""
		}
	}]
	grants: govern: boundary: "registry"
}
