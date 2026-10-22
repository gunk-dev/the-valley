// Approval case 1, rejected. The floor requires a hardware key, and the
// project writes `hardware: false`. The floor's `true` is concrete, so the
// project's `false` conflicts with it and vet names the field.
package verification

floor: {
	checks: "c-build": {runner: "nix", attribute: "c-build"}
	classes: guarded: {
		paths: "keys/**":    true
		requires: "c-build": true
	}
	approval: hardware: true
}

templates: plain: {}
type: "plain"

project: approval: hardware: false
