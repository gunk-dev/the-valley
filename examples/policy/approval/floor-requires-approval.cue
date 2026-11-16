// Approval case 7. The floor requires a signed approval, so the composed
// policy does. The project adds a path to the floor's class and leaves the
// requirement alone. The same project writing `required: false` is
// rejected/project-drops-floor-required.cue.
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

project: classes: guarded: paths: "grants/**": true
