// The project layer of a sample verification policy: what one project in
// the group says about itself, on top of the instance floor. It is a
// sample, not an installed file — the real one lives in the project's own
// repository, because everything above the floor travels with the project
// (ida-3e87f5c).
//
// Validate, composed with the instance layer:
//   cue vet -c schema/verification.cue \
//     examples/verification-instance.cue examples/verification-project.cue
package verification

// The project type this project starts from. On its own this line is a
// complete policy: it picks up the floor and the docs template's knowledge
// lint, and nothing below is required to make it valid.
type: "docs"

project: {
	// A check the floor and the template know nothing about. A project may
	// always add classes and checks; the open struct is what allows it.
	checks: "shellcheck": {
		runner:    "nix"
		attribute: "shellcheck"
	}
	classes: scripts: {
		paths: "bin/**":        true
		requires: "shellcheck": true
	}

	// The template offered the knowledge lint as a default, so this
	// project can decline it and vet still passes. Declining the floor's
	// prose-format the same way does not: writing
	// `classes: prose: requires: "prose-format": false` here conflicts with
	// the floor's concrete `true` and fails vet naming the field. That is
	// the difference the two forms of the value encode.
	classes: knowledge: requires: "knowledge-lint": false
}
