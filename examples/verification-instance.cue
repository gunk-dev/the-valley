// The instance layer of a sample verification policy: the floor every
// project in the group must clear, and the project-type templates a
// project can start from. It is a sample, not an installed file — the real
// one lives in the instance repository, which for the gunk-dev instance is
// qinling (dcr-f41f718).
//
// Validate, composed with a project policy:
//   cue vet -c schema/verification.cue \
//     examples/verification-instance.cue examples/verification-project.cue
package verification

// prose-format ships today, as a nix flake check in this repo: the check
// is formatter idempotence, so it is deterministic and judgment-free
// (ida-1ec03b1). knowledge-lint does not exist yet in either repository —
// building it is the first step of the staged rollout, and naming it here
// is what makes the gap concrete rather than notional.
floor: {
	checks: "prose-format": {
		runner:    "nix"
		attribute: "prose-format"
	}

	// Every markdown file in every project in the group is formatted, and
	// no project can decide otherwise: both the coverage and the
	// requirement are concrete, so a project supplying `false` for either
	// conflicts.
	classes: prose: {
		paths: "**/*.md":         true
		requires: "prose-format": true
	}

	// A path no class covers is the policy falling behind the tree. The
	// floor answers that case for the whole group rather than leaving each
	// project to remember.
	unclassified: "prose-format": true
}

// A project type for the group's documentation projects. Everything here
// is a default rather than a rule: a docs project gets the knowledge lint
// by saying `type: "docs"` and nothing else, and a project that has reason
// to opt out can.
templates: docs: {
	checks: "knowledge-lint": {
		runner:    "nix"
		attribute: "knowledge-lint"
	}

	// Knowledge nodes are markdown under `.the-valley/`, so they match the
	// floor's `prose` class as well as this one and require both checks.
	// The union of two overlapping classes is visible here rather than only
	// in the schema's comments.
	classes: knowledge: {
		paths: ".the-valley/**":    bool | *true
		requires: "knowledge-lint": bool | *true
	}
}
