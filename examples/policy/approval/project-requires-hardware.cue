// Approval case 2. The floor says nothing about hardware keys, and the
// project requires one. A project may always add the requirement, so the
// composed policy requires a hardware key.
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

project: approval: hardware: true
