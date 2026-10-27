// the-valley's project layer: what this one project declares above the
// floor. The floor is the owning valley's policy/instance; this
// layer binds the-valley alone, and a project can only add to its own
// layer (dcr-f41f718).
package verification

import (
	"list"
	"strings"
)

project: {
	// the-valley takes no knowledge lint of its own tree. The docs
	// template's lint checks a knowledge graph kept in the source, and
	// the-valley's source holds none: its plan graph is project storage, at
	// refs/the-valley/knots, and the push hook holds it to the knot
	// module's rules. The nodes under archive/.the-valley/ are history that
	// never changes, so linting them against today's schema protects
	// nothing. The lint itself stays covered: the knowledge-lint class
	// below requires its consumer and rejection checks.

	// Every check below is the flake check of the same name in
	// nix/checks.nix, built for the runner's own system. own-policy holds
	// this list to that: a name here that the flake does not define fails
	// it.
	let ownChecks = [
		"valleyhook-unit",
		"protect-e2e",
		"knots-e2e",
		"note-unit",
		"change-unit",
		"knot-unit",
		"knot-agree",
		"delta-unit",
		"derive-unit",
		"review-unit",
		"attest-unit",
		"attest-conformance",
		"attest-e2e",
		"identity-unit",
		"identity-e2e",
		"integrator-unit",
		"integrator-e2e",
		"restack-unit",
		"restack-e2e",
		"signers-e2e",
		"sigverify-unit",
		"sigverify-e2e",
		"webui-unit",
		"webui-server-unit",
		"readaccess-unit",
		"rebase-unit",
		"valley-go-unit",
		"valley-cli",
		"review-notes",
		"valley-request",
		"valley-status",
		"valley-ready",
		"valley-changes",
		"valley-knot",
		"valley-plan",
		"valley-darwin-eval",
		"selfupdate-unit",
		"valley-self-update",
		"policy-deriver",
		"own-policy",
		"module-eval",
		"cue-vet",
		"attest-schema",
		"identity-schema",
		"knowledge-lint-consumer",
		"knowledge-lint-rejects",
		"init-e2e",
		"mirror-e2e",
		"bus-e2e",
		"ssh-e2e",
		"publish-e2e",
		"worker-e2e",
		"webui-publisher-e2e",
	]
	for n in ownChecks {
		checks: (n): {
			runner:    "nix"
			attribute: n
		}
	}

	// The integrator's scenarios take about a minute, and ssh-e2e,
	// publish-e2e, worker-e2e and webui-publisher-e2e boot NixOS machines.
	// Every other check here takes seconds once its packages are built.
	checks: "integrator-e2e": cost:      "slow"
	checks: "ssh-e2e": cost:             "slow"
	checks: "publish-e2e": cost:         "slow"
	checks: "worker-e2e": cost:          "slow"
	checks: "webui-publisher-e2e": cost: "slow"

	// Each class below is one part of the tree and the checks that read it.
	// A path in two classes requires both sets. A Go module's unit check is
	// its package built again with its tests, which runs `go test`. The
	// package itself runs no tests (nix/packages.nix).

	// The pre-receive hook and the git user's login shell. protect-e2e
	// pushes to bare repositories wired with the hook a host renders,
	// valley-ready pushes change records through that hook, and knots-e2e
	// pushes the plan graph through it.
	classes: valleyhook: {
		paths: "valleyhook/**":      true
		requires: "valleyhook-unit": true
		requires: "protect-e2e":     true
		requires: "valley-ready":    true
		requires: "knots-e2e":       true
	}

	// The change record, which the hook guards, the CLI's Go verbs and the
	// rebase engine write, and the integrator and the review module read.
	// Each reads a record through it, so a change to it requires their tests
	// as well as its own.
	classes: change: {
		paths: "change/**":          true
		requires: "change-unit":     true
		requires: "review-unit":     true
		requires: "valleyhook-unit": true
		requires: "protect-e2e":     true
		requires: "rebase-unit":     true
		requires: "valley-go-unit":  true
		requires: "valley-ready":    true
		requires: "valley-changes":  true
		requires: "integrator-unit": true
		requires: "integrator-e2e":  true
	}

	// The plan graph's module, which the CLI's Go verbs write the graph
	// with and the hook validates it with. knot-agree holds its validator
	// to the node schema and the knowledge lint, valley-knot and
	// valley-plan drive the verbs that use it, and knots-e2e pushes
	// through the hook. The web export reads names and knot IDs by its
	// rules, and the web server reads a claim's age by them, so
	// webui-unit and webui-server-unit are here too.
	classes: knot: {
		paths: "knot/**":              true
		requires: "knot-unit":         true
		requires: "knot-agree":        true
		requires: "valley-go-unit":    true
		requires: "valley-knot":       true
		requires: "valley-plan":       true
		requires: "valleyhook-unit":   true
		requires: "knots-e2e":         true
		requires: "webui-unit":        true
		requires: "webui-server-unit": true
	}

	// valley-delta-v1, which the CLI's Go verbs compare a version with its
	// approved version through, and which the review module recomputes a
	// review's binding with. delta-unit holds it to its conformance vectors,
	// valley-go-unit builds the verbs with it, valley-ready and review-notes
	// drive review over the comparison, and review-unit binds reviews with
	// its bounded reading.
	classes: delta: {
		paths: "delta/**":          true
		requires: "delta-unit":     true
		requires: "valley-go-unit": true
		requires: "valley-ready":   true
		requires: "review-notes":   true
		requires: "review-unit":    true
	}

	// Review records: the record, its binding to the version it judged, and
	// its append-only history. review-unit runs its tests against real git.
	classes: review: {
		paths: "review/**":      true
		requires: "review-unit": true
	}

	// The deriver of required checks, and the composed-policy reader it reads
	// the whole policy through. derive-unit holds the deriver to bin/valley's,
	// and valley-go-unit builds the verb that prints its answer with both.
	classes: derive: {
		paths: "derive/**":         true
		paths: "composed/**":       true
		requires: "derive-unit":    true
		requires: "valley-go-unit": true
	}

	// The note envelope. The hook and attest both read notes through it,
	// the identity compiler reads its approval files back with it, and the
	// web export's manifest is a note, which the web server builds with,
	// so a change to it requires their tests as well as its own.
	classes: note: {
		paths: "note/**":              true
		requires: "note-unit":         true
		requires: "valleyhook-unit":   true
		requires: "protect-e2e":       true
		requires: "attest-unit":       true
		requires: "attest-e2e":        true
		requires: "identity-unit":     true
		requires: "webui-unit":        true
		requires: "webui-server-unit": true
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

	// The integrator, the end-to-end scenarios under integrator/e2e/, and the
	// verification schema, which the integrator composes every policy under
	// and both checks read.
	classes: integrator: {
		paths: "integrator/**":           true
		paths: "composed/**":             true
		paths: "schema/verification.cue": true
		requires: "integrator-unit":      true
		requires: "integrator-e2e":       true
	}

	// The security-key signature verifier.
	classes: sigverify: {
		paths: "sigverify/**":      true
		requires: "sigverify-unit": true
		requires: "sigverify-e2e":  true
	}

	// The web UI's module: the export contract, its shared fixture, the
	// publisher, and the server. The publisher's tests also read each
	// project's policy/read and the instance's registry under the read and
	// identity schemas. Changes require both checks.
	classes: web: {
		paths: "web/**":               true
		paths: "schema/read.cue":      true
		paths: "schema/identity.cue":  true
		requires: "webui-unit":        true
		requires: "webui-server-unit": true
	}

	// Read access: the two consents a reader needs, the evaluator that
	// intersects them, and the coverage report. readaccess-unit reads both
	// schemas, so a change to either requires it, and the web publisher
	// reads its consents through the module, so webui-unit runs too.
	classes: readaccess: {
		paths: "readaccess/**":       true
		paths: "schema/read.cue":     true
		paths: "schema/identity.cue": true
		requires: "readaccess-unit":  true
		requires: "webui-unit":       true
	}

	// The valley CLI: the script, its Go verbs (status/), the rebase engine
	// they rebase and read landings with (rebase/), and its completions.
	// valley-ready drives ready, withdraw, rebase and the verbs that take a
	// change's handle, valley-changes drives changes and pending,
	// valley-plan drives plan against the golden files under
	// status/testdata/plan/, and valley-self-update drives --version and
	// self-update. derive-unit holds the script's deriver to the Go one.
	// The web publisher reads the valley through plan, pending and knot
	// show, and its tests in webui-unit run the CLI itself.
	classes: cli: {
		paths: "bin/valley":            true
		paths: "status/**":             true
		paths: "rebase/**":             true
		paths: "completions/**":        true
		requires: "valley-go-unit":     true
		requires: "valley-cli":         true
		requires: "review-notes":       true
		requires: "valley-request":     true
		requires: "valley-status":      true
		requires: "valley-ready":       true
		requires: "valley-changes":     true
		requires: "valley-knot":        true
		requires: "valley-plan":        true
		requires: "valley-self-update": true
		requires: "policy-deriver":     true
		requires: "derive-unit":        true
		requires: "webui-unit":         true
	}

	// Self-update: its module (selfupdate/), the policy the package ships
	// for its smoke test, and the home-manager module that runs it on a
	// timer. valley-go-unit builds the verb with the module, and
	// valley-self-update drives the verb through the package and holds the
	// units the home-manager module renders.
	classes: selfupdate: {
		paths: "selfupdate/**":            true
		paths: "examples/policy/smoke/**": true
		paths: "nix/home-module.nix":      true
		requires: "selfupdate-unit":       true
		requires: "valley-go-unit":        true
		requires: "valley-self-update":    true
	}

	// The rebase engine, with its own tests over real repositories. The
	// verbs that run it are the cli class's, so its paths are there too.
	classes: rebase: {
		paths: "rebase/**":      true
		requires: "rebase-unit": true
	}

	// The restacker, with its unit tests and its planner end to end. It
	// reads origin through the rebase engine and the policy through the
	// composed-policy reader it shares with the integrator, so a change to
	// either requires its checks too.
	classes: restack: {
		paths: "restack/**":      true
		paths: "composed/**":     true
		paths: "rebase/**":       true
		requires: "restack-unit": true
		requires: "restack-e2e":  true
	}

	// What a client verifies an integrator's transfer statement with: the
	// host's publisher and the default name it publishes, the unit that
	// runs it, the client's reading of the signers, the instance discovery
	// it reads them through, and the snapshot that asks for them.
	// signers-e2e drives them together over the real integrator.
	classes: signers: {
		paths: "integrator/signers.go": true
		paths: "integrator/main.go":    true
		paths: "nix/valley-host.nix":   true
		paths: "status/signers.go":     true
		paths: "status/list.go":        true
		paths: "rebase/snapshot.go":    true
		requires: "signers-e2e":        true
	}

	// The web publisher's module, which runs the publisher on a timer in a
	// sandbox of its own. webui-publisher-e2e runs the unit it renders in a
	// NixOS machine. module-eval, in the nix class, holds what it renders.
	classes: "web-publisher": {
		paths: "nix/web-publisher.nix":  true
		requires: "webui-publisher-e2e": true
	}

	// The verification policy: this layer, the pointer to the owning
	// valley, the schema a policy is written in, and the example policies
	// the derivers and cue-vet read.
	classes: policy: {
		paths: "policy/**":               true
		paths: "schema/verification.cue": true
		paths: "examples/policy/**":      true
		requires: "policy-deriver":       true
		requires: "derive-unit":          true
		requires: "cue-vet":              true
		requires: "own-policy":           true
	}

	// The flake, the NixOS module, the packages and the check definitions,
	// and the host declarations the module checks are evaluated over.
	// own-policy is here because renaming or removing a flake check would
	// leave this layer naming a check that no longer exists. A change to
	// flake.nix or flake.lock requires these alone, so a nixpkgs bump boots no
	// machine.
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
	// graph to, and the example graphs it must accept and reject. The plan
	// graph's validator is held to the same two by knot-agree.
	classes: "knowledge-lint": {
		paths: "nix/knowledge-lint.*":       true
		paths: "schema/node.cue":            true
		paths: "examples/graphs/**":         true
		requires: "knowledge-lint-consumer": true
		requires: "knowledge-lint-rejects":  true
		requires: "knot-agree":              true
	}

	// The NixOS module and what its end-to-end checks run. The hook checks
	// follow the scripts out of the hosts in nix/checks/hosts.nix, which
	// are made of the three declarations named here; ssh-e2e, publish-e2e
	// and worker-e2e run the module in NixOS machines.
	classes: "host-module": {
		paths: "nix/valley-host.nix":          true
		paths: "nix/checks/hosts.nix":         true
		paths: "examples/hosts/protected.cue": true
		paths: "examples/hosts/bus.cue":       true
		paths: "examples/hosts/mirrors.cue":   true
		requires: "init-e2e":                  true
		requires: "mirror-e2e":                true
		requires: "bus-e2e":                   true
		requires: "protect-e2e":               true
		requires: "knots-e2e":                 true
		requires: "identity-e2e":              true
		requires: "ssh-e2e":                   true
		requires: "publish-e2e":               true
		requires: "worker-e2e":                true
		requires: "valley-changes":            true
	}

	// A check's own files require that check: its script,
	// nix/checks/<name>.sh, and its definition, nix/checks/<name>.nix,
	// where it has a file of its own. One class per check, named
	// check-<name>. prose-format is the floor's check, but its script is
	// here. own-policy fails if a script under nix/checks/ requires no
	// check.
	for n in list.Concat([ownChecks, ["prose-format"]]) {
		classes: ("check-\(n)"): {
			paths: ("nix/checks/\(n).sh"):  true
			paths: ("nix/checks/\(n).nix"): true
			requires: (n):                  true
		}
	}

	// The other files under nix/checks/, and the checks that read each: the
	// definitions of a subject's checks, and the helpers and rejection lists
	// a check is given. cli.nix, which defines every check of bin/valley,
	// is left to the nix class, as is nix/checks.nix, which wires them all.
	let checkFiles = {
		"attest.nix": ["attest-unit", "attest-conformance", "attest-e2e"]
		"delta.nix": ["delta-unit"]
		"derive.nix": ["derive-unit"]
		"review.nix": ["review-unit"]
		"identity.nix": ["identity-unit", "identity-e2e"]
		"integrator.nix": ["integrator-unit", "integrator-e2e"]
		"signers.nix": ["signers-e2e"]
		"sigverify.nix": ["sigverify-unit", "sigverify-e2e"]
		"web.nix": ["webui-unit", "webui-server-unit", "readaccess-unit"]
		"schema.nix": ["cue-vet", "attest-schema", "identity-schema"]
		"knowledge.nix": ["knowledge-lint-consumer", "knowledge-lint-rejects", "prose-format"]
		"hooks.nix": ["valleyhook-unit", "change-unit", "mirror-e2e", "protect-e2e", "init-e2e", "bus-e2e"]
		"host-module.nix": ["module-eval"]
		"fail-nth.sh": ["policy-deriver", "valley-request"]
		"review-notes-editor.sh": ["review-notes"]
		"cue-vet-rejected.txt": ["cue-vet"]
		"identity-rejected.txt": ["identity-schema"]
		"knowledge-lint-rejected.txt": ["knowledge-lint-rejects"]
	}
	for f, ns in checkFiles {
		classes: ("check-file-\(strings.Replace(f, ".", "-", -1))"): {
			paths: ("nix/checks/\(f)"): true
			requires: {for n in ns {(n): true}}
		}
	}

	// How far an approval carries across a rebase (#Carry in
	// schema/verification.cue). Every path not named here is same-paths.
	//
	// The hook, the integrator, the signature verifier and the identity
	// compiler enforce who may do what. An approval of a change to one of
	// them carries only while no landing has touched the same classes:
	// same-class. The identity compiler's class is its code and the schema
	// it holds a registry to. It requires nothing, because the identity and
	// schema classes already require the checks of its paths.
	classes: valleyhook: carry: "same-class": true
	classes: integrator: carry: "same-class": true
	classes: sigverify: carry: "same-class":  true
	classes: "identity-compiler": {
		paths: "identity/**":         true
		paths: "schema/identity.cue": true
		carry: "same-class":          true
	}

	// The policy decides what every change requires, so an approval of a
	// change to it carries across no landing at all: exact. That is this
	// layer, the pointer to the owning valley, and the schema a policy is
	// written in. The example policies are fixtures and stay same-paths. Like
	// identity-compiler, this class requires nothing: the policy class
	// requires the checks of its paths.
	//
	// This tree holds no registry, grant or secret data of its own. The
	// registries, grants and keys under examples/ and attest/conformance/
	// are test fixtures, and the instance's registry is in its instance
	// repository. So nothing else here is exact.
	classes: "policy-authority": {
		paths: "policy/**":               true
		paths: "schema/verification.cue": true
		carry: exact:                     true
	}

	// The restacker's cap is the schema's default of 64 automatic versions
	// per change.
}

// What no class above covers falls to the floor's `unclassified` rule and
// requires prose-format alone. That is the research artefacts under research/,
// the GitHub mirror's workflow under .github/, and editor settings: files
// no program here reads.
