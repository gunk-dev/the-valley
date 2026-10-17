// The project layer of the policy self-update smoke-tests a build with
// (../instance/floor.cue says how).
package verification

project: {
	checks: "smoke-project": {
		runner:    "nix"
		attribute: "smoke-project"
	}
	classes: code: {
		paths: "src/**":           true
		requires: "smoke-project": true
	}
}
