// Approval case 1. The floor requires a hardware key, so the composed policy
// does. The project adds a path to the floor's class and leaves the
// requirement alone. The same project writing `hardware: false` is
// rejected/project-drops-floor-hardware.cue.
//
// Each case here is the three layers composed in one document, as
// schema/verification.cue composes them. cue-vet vets each one and checks
// what the composed policy says about hardware keys.
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

project: classes: guarded: paths: "grants/**": true
