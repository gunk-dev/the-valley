// Carry case 1, rejected. The floor pins `exact` on the guarded class, and
// the project writes `exact: false`. The floor's `true` is concrete, so the
// project's `false` conflicts with it and vet names the field.
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

project: classes: guarded: carry: exact: false
