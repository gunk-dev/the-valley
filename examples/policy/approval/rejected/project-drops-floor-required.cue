// Approval case 7, rejected. The floor requires a signed approval, and the
// project writes `required: false`. The floor's `true` is concrete, so the
// project's `false` conflicts with it and vet names the field.
package verification

floor: {
	checks: "c-build": {runner: "nix", attribute: "c-build"}
	classes: guarded: {
		paths: "keys/**":    true
		requires: "c-build": true
	}
	approval: required: true
}

templates: plain: {}
type: "plain"

project: approval: required: false
