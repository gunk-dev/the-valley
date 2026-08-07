// The host the flake's protect-e2e check drives: the three states a
// project's refs can be in. "guarded" takes the default protected set,
// "released" names a wildcard pattern beside it, and "open" declares no
// protection at all and so has none.
//
// Which keys act as the "integrator" principal is machine integration
// (services.valley.authorizedKeys), not declared here — the name is what
// the identity registry will bind, and this declaration is what names it.
package valley

projects: {
	"guarded": protection: writers: ["integrator"]

	"released": protection: {
		refs: ["refs/heads/main", "refs/heads/release/*"]
		writers: ["integrator"]
	}

	"open": {}
}
