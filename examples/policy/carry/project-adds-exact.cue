// Carry case 4. The floor pins `same-class` on the guarded class, and the
// project adds `exact`, which is stricter. The class resolves to `exact`. A
// project may always add a stricter key. The project also lowers the
// restacker's cap to 16.
package verification

floor: {
	checks: "c-build": {runner: "nix", attribute: "c-build"}
	classes: guarded: {
		paths: "auth/**":    true
		requires: "c-build": true
		carry: "same-class": true
	}
}

templates: plain: {}
type: "plain"

project: {
	classes: guarded: carry: exact: true
	restack: maxAutomaticVersions: 16
}
