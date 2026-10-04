// The host the flake's protect-e2e check drives: the four states a
// project's refs can be in. "sealed" is the norm — protected, with no
// writer, so nothing pushes to it and changes land by integration.
// "guarded" declares the writers exception over the default protected set,
// "released" names a wildcard pattern beside it and grants release tags to
// the principal that cuts them, and "open" declares no protection at all,
// so it protects no ref and every other rule of the push policy still
// applies to it.
//
// Which keys act as the "integrator" principal is machine integration
// (services.valley.authorizedKeys), not declared here — the name is what
// the identity registry will bind, and this declaration is what names it.
package valley

projects: {
	"sealed": protection: refs: ["refs/heads/main"]

	"guarded": protection: writers: ["integrator"]

	// released's protection also covers its integration requests, so a
	// request there needs both a declared writer and the request grant.
	"released": protection: {
		refs: ["refs/heads/main", "refs/heads/release/*", "refs/the-valley/integration-requests/*"]
		writers: ["integrator"]
	}
	"released": grants: "release-tags": {
		refs: ["refs/tags/release/*"]
		writers: ["integrator"]
	}

	"open": {}
}
