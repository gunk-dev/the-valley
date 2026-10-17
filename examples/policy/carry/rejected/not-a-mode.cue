// Carry case 5. The project writes a carry key that is not a mode. A carry
// set is closed to the two modes stricter than the default, so vet names the
// field rather than reading the key as nothing.
package verification

floor: {
	checks: "c-build": {runner: "nix", attribute: "c-build"}
	classes: guarded: {
		paths: "src/**":     true
		requires: "c-build": true
	}
}

templates: plain: {}
type: "plain"

project: classes: guarded: carry: "same-file": true
