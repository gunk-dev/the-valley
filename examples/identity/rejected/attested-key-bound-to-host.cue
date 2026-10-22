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
		public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIZNSh7kPQN53thBJoBVdGUpUJAkQAb4nsNo/pYJGVIX"
		attestation: {
			slot: """
				-----BEGIN CERTIFICATE-----
				MIICJzCCAQ+gAwIBAgIQATCsihQMzzFrSCbhTl5GojANBgkqhkiG9w0BAQsFADAi
				MSAwHgYDVQQDDBdZdWJpS2V5IFBJViBBdHRlc3RhdGlvbjAgFw0yNDEyMDEwMDAw
				MDBaGA85OTk5MTIzMTIzNTk1OVowJTEjMCEGA1UEAwwaWXViaUtleSBQSVYgQXR0
				ZXN0YXRpb24gOWMwKjAFBgMrZXADIQCGTUoe5D0Ded7YQSaAVXRlKVCQJEAG+J7D
				aP6WCRlSF6NOMEwwEQYKKwYBBAGCxAoDAwQDBQcEMBQGCisGAQQBgsQKAwcEBgIE
				AgaY5jAQBgorBgEEAYLECgMIBAIDAjAPBgorBgEEAYLECgMJBAEDMA0GCSqGSIb3
				DQEBCwUAA4IBAQBlCVM5odtfuW6cSdc+91ZuxH9UUkiwLbgTjsUbI+r8uEgQvuGe
				dayjAI5w9Z6snXiazWwrMWrJnuPelRqfs/UGvDhwyeVlh3B5F6HcG4r6OPtA+Dtg
				PFgPNfugE79yDzAh6FGSZ/nfhlfBcNLkhjh5VONWqDX5AU/WDIstg4B26N/RmUJP
				d1B7CFlQsjs7ncbHe8eP7E5MWtWEVtOMLTHMDB20wk9wCXX9qBBkOdbtjSG4Sgz6
				dgalpWbxhhwcGGAHIMO29AIr1cHV3ICChMD4j5hrwcK6x6MvPxCqPgUJSFwff7R+
				GDJAF3vFOrQDmqHLY8vKZluju2tdDJeqQsWh
				-----END CERTIFICATE-----
				"""
			f9: """
				-----BEGIN CERTIFICATE-----
				MIIC9TCCAd2gAwIBAgIJAJXcF5k7Lbf8MA0GCSqGSIb3DQEBCwUAMCUxIzAhBgNV
				BAMMGll1YmljbyBQSVYgQXR0ZXN0YXRpb24gQiAxMCAXDTI0MTIwMTAwMDAwMFoY
				Dzk5OTkxMjMxMjM1OTU5WjAiMSAwHgYDVQQDDBdZdWJpS2V5IFBJViBBdHRlc3Rh
				dGlvbjCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBALfGsJlUJlSFW9U1
				LiPJe2J0HEy0wacxa53c0UEexr/ZtgmrIf/CVh8kqE4qpMs5igeyqxqZqLjdNqZZ
				mdYFfHJL6AKSppCcWYnaHN6rSj6OgBpidg9uaQlNx+03fXn7GJHQhWYgvTX9qczm
				fkqh2rh4a14Bg2ipgBePwwvPUidHSdqytfMgq+H1YzbouamXyHdcpW/OPPOeoTwv
				+X4fr3bldpwABqHDT/mM/znh16dhQu7QlPFgnG3rNK6ddCIdVuFs+LqYlU7Ute+f
				QbVp8FiravxczUb1GRRKMNkWGtA6m5P87zyAw8vUbnMlsflj6paHAwOpzXfAT4/G
				k65UhqUCAwEAAaMpMCcwEQYKKwYBBAGCxAoDAwQDBQcEMBIGA1UdEwEB/wQIMAYB
				Af8CAQAwDQYJKoZIhvcNAQELBQADggEBAEsyUM3GxUCWSHliTfcF9+r5BEILfR/5
				2cdpRfaJWSdW+/bHUffdGWRD6PckY3AQBpgap3zBgzE5yq2Kv3A1bnAFo6DHjPCn
				waos9hFm/YVidxjWvi0brHwKHHGGQdSmQzLAG7/aabpfaVfEurQDBBUoZAnBxdMP
				tLaS+HGhAVbTgyh9glZrfqZi/aoM57JB5yBZ8GCzUazN6CdE5TnEs+ooVQXfQNP2
				wNkM2IMOE/CNXa/UVp9s3JH7hloW/VNoynK5SlMrJ+acoehM3fDM4NgIpE+q8ei8
				+sjUk8NEdie+vPIEgpS8ZEGX1030kuRfw47GG4CJEN9fbB2jaqzU/Mg=
				-----END CERTIFICATE-----
				"""
		}
	}]
	grants: govern: boundary: "registry"
}
