// Carry case 3. No layer writes `carry`, so the guarded class resolves to
// `same-paths`. No layer writes `restack` either, so the cap is the schema's
// default of 64.
package verification

floor: {
	checks: "c-build": {runner: "nix", attribute: "c-build"}
	classes: guarded: {
		paths: "src/**":     true
		requires: "c-build": true
	}
}

templates: plain: classes: guarded: paths: "lib/**": bool | *true
type: "plain"

project: classes: guarded: paths: "cmd/**": true
