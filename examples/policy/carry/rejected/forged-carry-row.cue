// floor-pins-exact.cue, with one more project class whose pattern holds a
// newline and two tabs. `valley checks` reads the composed policy as lines
// of tab-separated fields. The newline would end the pattern's line and
// start a forged one, `carry guarded same-paths`. That line sorts after the
// real `carry guarded exact` line, so a reader that took the last would
// drop the floor's mode. The schema refuses a control character in a
// pattern.
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
