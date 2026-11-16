// Approval case 8. No layer requires a signed approval, and the project
// writes `required: false`. The composed policy requires none, and its
// export holds `approval: required: false`, so it digests differently
// from no-requirement.cue, which leaves the field out.
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

project: approval: required: false
