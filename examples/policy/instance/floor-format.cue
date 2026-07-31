// One document of an instance floor: the formatting half. It is a sample,
// not an installed file — the real floor directory lives in the instance
// repository, which for the gunk-dev instance is qinling (dcr-f41f718).
//
// The floor is a directory, not a file. This document and its sibling
// floor-references.cue both contribute to the `prose` class, and the
// composition is unification, so the split carries no meaning of its own.
//
// Validate the whole arrangement — floor directory, template and project
// document — with:
//   cue vet -c schema/verification.cue \
//     examples/policy/instance/*.cue examples/policy/project/*.cue
package verification

floor: {
	checks: "prose-format": {
		runner:    "nix"
		attribute: "prose-format"
	}

	// Every markdown file in every project in the group is formatted, and
	// no project can decide otherwise: both the coverage and the
	// requirement are concrete.
	classes: prose: {
		paths: "**/*.md":         true
		requires: "prose-format": true
	}

	// A path no class covers is the policy falling behind the tree. The
	// floor answers that case for the whole group rather than leaving each
	// project to remember.
	unclassified: "prose-format": true
}
