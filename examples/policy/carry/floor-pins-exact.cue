// Carry case 1. The floor pins `exact` on the guarded class, so the class
// resolves to `exact`. The template adds nothing, and the project adds a path
// to the class and leaves its carry alone. The same project writing
// `exact: false` is rejected/project-drops-floor-exact.cue.
//
// Each case here is the three layers composed in one document, as
// schema/verification.cue composes them. cue-vet vets each one and checks
// the mode the class resolves to.
package verification

floor: {
	checks: "c-build": {runner: "nix", attribute: "c-build"}
	classes: guarded: {
		paths: "keys/**":    true
		requires: "c-build": true
		carry: exact:        true
	}
}

templates: plain: {}
type: "plain"

project: classes: guarded: paths: "grants/**": true
