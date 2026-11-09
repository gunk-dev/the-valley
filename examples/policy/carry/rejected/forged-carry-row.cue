// floor-pins-exact.cue, with one more project class whose pattern holds a
// newline and two tabs, spelling `carry guarded same-paths` after the
// newline. A reader of the policy as lines of tab-separated fields would
// take that for a looser carry mode than the floor's. The schema refuses a
// control character in a pattern.
package verification

floor: {
	checks: "c-build": {runner: "nix", attribute: "c-build"}
	classes: guarded: {
		paths: "keys/**":    true
		requires: "c-build": true
		carry: exact:        true
	}
}

templates: plain: {}
type: "plain"

project: classes: guarded: paths: "grants/**": true

project: classes: injected: paths: "unused\ncarry\tguarded\tsame-paths": true
