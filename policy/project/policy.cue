// the-valley's project layer: what this one project declares above the
// floor. The floor is the owning valley's (qinling policy/instance); this
// layer binds the-valley alone, and a project can only add to its own
// layer (dcr-f41f718).
package verification

project: {
	// the-valley keeps a knowledge graph, so it takes the lint the docs
	// template offers rather than declining it.
	#docs

	// The graph is archived under archive/.the-valley/, which the
	// template's `.the-valley/**` does not reach, so the knowledge class
	// adds the archived path. nix/checks/knowledge.nix lints that root.
	classes: knowledge: paths: "archive/.the-valley/**": true

	// Every check below is the flake check of the same name in
	// nix/checks.nix, built for the runner's own system. own-policy holds
	// this list to that: a name here that the flake does not define fails
	// it.
	for n in [
		"valleyhook-unit",
		"protect-e2e",
		"note-unit",
		"attest-unit",
		"attest-conformance",
		"attest-e2e",
		"identity-unit",
		"identity-e2e",
		"integrator-unit",
		"integrator-e2e",
		"sigverify-unit",
		"sigverify-e2e",
		"valley-status-unit",
		"valley-cli",
		"review-notes",
		"valley-request",
		"valley-status",
		"valley-darwin-eval",
		"policy-deriver",
		"own-policy",
		"module-eval",
		"cue-vet",
		"attest-schema",
		"identity-schema",
		"knowledge-lint-consumer",
		"knowledge-lint-rejects",
	] {
		checks: (n): {
			runner:    "nix"
			attribute: n
		}
	}

	// The integrator's scenarios take about a minute, where every other
	// check here takes seconds once its packages are built.
	checks: "integrator-e2e": cost: "slow"

	// Each class below is one part of the tree and the checks that read it.
	// A path in two classes owes both sets. A Go module's unit check is its
	// package, whose checkPhase runs `go test`.

	// The pre-receive hook and the git user's login shell. protect-e2e
	// pushes to bare repositories wired with the hook a host renders.
	classes: valleyhook: {
		paths: "valleyhook/**":      true
		requires: "valleyhook-unit": true
		requires: "protect-e2e":     true
	}

	// The note envelope. The hook and attest both read notes through it, so
	// a change to it owes their tests as well as its own.
	classes: note: {
		paths: "note/**":            true
		requires: "note-unit":       true
		requires: "valleyhook-unit": true
		requires: "protect-e2e":     true
		requires: "attest-unit":     true
		requires: "attest-e2e":      true
	}

	// The attestation helper, and the conformance vectors under
	// attest/conformance/ that hold its written form fixed.
	classes: attest: {
		paths: "attest/**":             true
		requires: "attest-unit":        true
		requires: "attest-conformance": true
		requires: "attest-e2e":         true
	}

	// The identity registry compiler, and the registries it is driven over.
	classes: identity: {
		paths: "identity/**":          true
		paths: "examples/identity/**": true
		requires: "identity-unit":     true
		requires: "identity-e2e":      true
	}

	// The integrator, and the end-to-end scenarios under integrator/e2e/.
	classes: integrator: {
		paths: "integrator/**":      true
		requires: "integrator-unit": true
		requires: "integrator-e2e":  true
	}

	// The security-key signature verifier.
	classes: sigverify: {
		paths: "sigverify/**":      true
		requires: "sigverify-unit": true
		requires: "sigverify-e2e":  true
	}

	// The valley CLI: the script, its status helper, and its completions.
	classes: cli: {
		paths: "bin/valley":            true
		paths: "status/**":             true
		paths: "completions/**":        true
		requires: "valley-status-unit": true
		requires: "valley-cli":         true
		requires: "review-notes":       true
		requires: "valley-request":     true
		requires: "valley-status":      true
		requires: "policy-deriver":     true
	}

	// The verification policy: this layer, the pointer to the owning
	// valley, the schema a policy is written in, and the example policies
	// the deriver and cue-vet read.
	classes: policy: {
		paths: "policy/**":               true
		paths: "schema/verification.cue": true
		paths: "examples/policy/**":      true
		requires: "policy-deriver":       true
		requires: "cue-vet":              true
		requires: "own-policy":           true
	}

	// The flake, the NixOS module, the packages and the check definitions,
	// and the host declarations the module checks are evaluated over.
	// own-policy is here because renaming or removing a flake check would
	// leave this layer naming a check that no longer exists.
	classes: nix: {
		paths: "nix/**":                true
		paths: "flake.nix":             true
		paths: "flake.lock":            true
		paths: "examples/host.cue":     true
		paths: "examples/hosts/**":     true
		requires: "module-eval":        true
		requires: "valley-darwin-eval": true
		requires: "cue-vet":            true
		requires: "own-policy":         true
	}

	// The schemas, and the example events, attestations and registries they
	// are vetted against.
	classes: schema: {
		paths: "schema/**":                true
		paths: "examples/events/**":       true
		paths: "examples/attestations/**": true
		paths: "examples/identity/**":     true
		requires: "cue-vet":               true
		requires: "attest-schema":         true
		requires: "identity-schema":       true
	}

	// The knowledge lint itself: its program, the node schema it holds a
	// graph to, and the example graphs it must accept and reject.
	classes: "knowledge-lint": {
		paths: "nix/knowledge-lint.*":       true
		paths: "schema/node.cue":            true
		paths: "examples/graphs/**":         true
		requires: "knowledge-lint":          true
		requires: "knowledge-lint-consumer": true
		requires: "knowledge-lint-rejects":  true
	}
}

// What no class above covers falls to the floor's `unclassified` rule and
// owes prose-format alone. That is the research artefacts under research/,
// the GitHub mirror's workflow under .github/, and editor settings: files
// no program here reads.
