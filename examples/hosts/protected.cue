// The host the flake's protect-e2e check drives: the four states a
// project's refs can be in. "sealed" is the norm — protected, with no
// writer, so nothing pushes to it and changes land by integration.
// "guarded" declares the writers exception over the default protected set,
// "released" names a wildcard pattern beside it, and "open" declares no
// protection at all and so has none.
//
// Which keys act as the "integrator" principal is machine integration
// (services.valley.authorizedKeys), not declared here — the name is what
// the identity registry will bind, and this declaration is what names it.
package valley

projects: {
	"sealed": protection: refs: ["refs/heads/main"]

	"guarded": protection: writers: ["integrator"]

	"released": protection: {
		refs: ["refs/heads/main", "refs/heads/release/*"]
		writers: ["integrator"]
	}

	"open": {}
}
