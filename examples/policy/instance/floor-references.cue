// The second document of the same instance floor: the reference-integrity
// half, added by a later change without touching floor-format.cue.
//
// It names the same class and the same coverage pattern that document
// already pinned. Agreement composes silently — `true` unified with `true`
// is `true` — and the class ends up requiring both checks. Disagreement
// does not compose: a document writing `false` for either field conflicts
// with the other document's `true`, and vet names both files.
package verification

floor: {
	checks: "link-check": {
		runner:    "nix"
		attribute: "link-check"
	}

	classes: prose: {
		paths: "**/*.md":       true
		requires: "link-check": true
	}
}
