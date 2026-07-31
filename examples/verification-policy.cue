// A sample verification policy: qinling's, the docs-only project the
// staged rollout in dcr-f41f718 exercises first. It is a sample, not an
// installed file — the real one would live in qinling's own repository,
// because the policy travels with the project (ida-3e87f5c).
//
// Validate:  cue vet -c schema/verification.cue examples/verification-policy.cue
package verification

// prose-format ships today, as a nix flake check in this repo: the check
// is formatter idempotence, so it is deterministic and judgment-free
// (ida-1ec03b1). knowledge-lint does not exist yet in either repository —
// building it is the first step of the staged rollout, and naming it here
// is what makes the gap concrete rather than notional.
checks: {
	"prose-format": {
		runner:    "nix"
		attribute: "prose-format"
	}
	"knowledge-lint": {
		runner:    "nix"
		attribute: "knowledge-lint"
	}
}

// Two classes that overlap on purpose, so composition does real work here
// rather than only in the schema's comments. A knowledge node is markdown
// under `.the-valley/`, so it matches both classes and requires both
// checks; a README edit matches only `prose`, and the knowledge lint is
// not required — visible as an absence, not asserted anywhere.
classes: [
	{
		name: "knowledge"
		paths: [".the-valley/**"]
		requires: ["knowledge-lint"]
	},
	{
		name: "prose"
		paths: ["**/*.md"]
		requires: ["prose-format"]
	},
]

// qinling is markdown-only today, so any other path is the project growing
// past its policy. Paying both checks is the cheap failure; the expensive
// one is a path that quietly requires nothing.
unclassified: ["knowledge-lint", "prose-format"]
