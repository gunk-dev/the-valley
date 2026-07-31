// The verification policy of a valley project: which checks a change must
// carry, derived from the paths its diff touches. Policy is data, so it is
// a CUE document like the host config and the event vocabulary
// (ida-1ec03b1).
//
// This file is a draft. The decision it gives concrete form to
// (dcr-f41f718) is `proposed`, not decided: nothing reads this file, no
// enforcement point exists until Phase 3's integrator (design/roadmap.md),
// and the flake's cue-vet check deliberately does not cover it yet.
//
// The policy lives in the project's own repository — the recommended fork
// of dcr-f41f718 — because a fresh clone must be able to say what its own
// changes must pass without asking a host, and because a project served by
// two hosts must mean the same thing on both. The host's half of that
// split is one field, sketched at the bottom of this file as
// #ProjectVerification and deliberately not applied to schema/valley.cue.
//
// Like the host and event schemas, this file is deliberately not Nix. A
// policy names checks; it never names derivations, systems, or store
// paths, so it travels between machines unchanged.
//
//   Validate a policy:
//     cue vet -c schema/verification.cue examples/verification-policy.cue
package verification

// #Policy is a complete verification policy: the checks a project can
// require, the path classes that require them, and what a path the policy
// does not cover costs.
#Policy: {
	// checks maps a check's name to how that check is run. Classes
	// require checks by name; a name that is not a key here fails vet
	// (see the cross-check below), because a required check nobody can
	// run is the one failure this document must never have.
	checks: [#Name]: #Check

	// The path classes. Matching is a set operation, never first-match:
	// a change's required checks are the union of `requires` over every
	// class that at least one changed path matches. Classes may overlap
	// on purpose — a knowledge node is both knowledge and prose — and
	// overlapping classes compose to the maximum of what they ask for.
	// That union is the whole of the "mixed commits take the max" rule
	// (ida-1ec03b1): there is no separate mixed case to get wrong.
	classes: [...#Class]

	// The checks required when a changed path matches no class at all.
	// A path outside every class means the policy is behind the tree, so
	// the safe answer is the strictest set the project has rather than
	// the empty one. An empty list is allowed, and is a deliberate
	// statement that uncovered paths are unconstrained.
	unclassified: [...#Name]
}

// #Class is one path class and what touching it requires.
#Class: {
	// The class's name. It exists to be quoted back — the integrator
	// says which class made a check required — and nothing references
	// it, so it need not be unique.
	name: #Name

	// The paths this class covers. Patterns are matched against each
	// changed path of the diff, relative to the repository root: `*`
	// matches within one path segment, `**` matches any number of
	// segments including none. Every side of the diff counts — an added,
	// modified, or deleted path, and both the old and the new path of a
	// rename — because a check's scope is what the tree looked like as
	// much as what it looks like now.
	paths: [...#Pattern]

	// The checks a change touching this class must carry. Every name
	// must be a key of the policy's `checks`.
	requires: [...#Name]
}

// #Check is how one named check is run. In the reference implementation a
// check is a flake check: `nix build .#checks.<system>.<attribute>`, where
// the system is the runner's own and never the policy's — a policy that
// named a system would stop travelling.
#Check: {
	// The runner kind. "nix" is the only runner today; another kind
	// would widen this to a disjunction, deliberately not before an
	// implementation exists.
	runner: "nix"

	// The flake check attribute this check names.
	attribute: #Name
}

// Names are lowercase, dash-separated, and short. They are quoted in
// integrator output and carried in attestation payloads, so they are held
// to the same shape as project names in schema/valley.cue.
#Name: =~"^[a-z0-9][a-z0-9-]*$"

// A path glob, relative to the repository root. A leading slash and a `..`
// segment are both rejected: a pattern that reaches outside the tree is
// describing something a diff cannot contain, and silently matching
// nothing is the expensive way to find that out.
#Pattern: =~"^[^/]" & !~"(^|/)\\.\\.(/|$)"

// The top level of this package is itself a policy: a policy file
// evaluated together with this schema is validated against #Policy —
// unknown fields, malformed names, and unsafe patterns are rejected.
checks:       #Policy.checks
classes:      #Policy.classes
unclassified: #Policy.unclassified

// Every check a class requires, and every check `unclassified` names, must
// exist in the catalogue. A name that does not lands in `checks` as an
// incomplete #Check, so `cue vet -c` fails with an error naming the
// missing check. Without this, a typo is not a vet error — it is a check
// that silently stops being required.
for c in classes for n in c.requires {
	checks: (n): _
}
for n in unclassified {
	checks: (n): _
}

// A file's top level cannot be closed (embedding #Policy would open it
// instead — embedding lifts closedness), so reject stray top-level fields
// explicitly: anything but the #Policy fields — a `class:` typo, a
// host-side concern like `enable:` — conflicts with this sentinel and
// fails vet with an error naming the field.
[!="checks" & !="classes" & !="unclassified"]: "INVALID: unknown top-level field; only \"checks\", \"classes\" and \"unclassified\" are allowed"

// ----------------------------------------------------------------------
// Sketched, deliberately not applied.
//
// The host's half of the split dcr-f41f718 recommends. It belongs on
// #Project in schema/valley.cue, as `verification?: #ProjectVerification`,
// and it is not added there because no enforcement point reads it yet;
// adding a field the host cannot act on would misrepresent the boundary.
// It is written out here so the proposal can be read rather than imagined.
//
// The host declares *that* a project is verified, never *what* it must
// pass. A host that restated the checks would be back to owning policy the
// project should own — but a host that declares nothing cannot refuse to
// serve a project whose in-repo policy has gone empty, which is exactly
// the gate cosmo loses by migrating.
#ProjectVerification: {
	// Whether the host requires this project's changes to be verified.
	// The block is optional on #Project, so a declaration written before
	// this field existed evaluates exactly as it did before; a
	// declaration that names the block and disables it is saying so on
	// purpose, in the host's history.
	enable: bool | *true

	// Where the required-check policy is read from. "in-repo" — the
	// project's own policy document, this schema — is the only source
	// today; a host-declared policy would widen this to a disjunction,
	// deliberately not before there is a reason to want one.
	source: "in-repo"
}
