// The verification policy of a valley project: which checks a change must
// carry, derived from the paths its diff touches. Policy is data, so it is
// a CUE document like the host config and the event vocabulary
// (ida-1ec03b1).
//
// A policy is composed from two documents, never one. The instance layer —
// the group's floor and its project-type templates — is carried in the
// instance repository. The project layer is carried in the project's own
// tree. The effective policy is the unification of the two, and because
// unification can only narrow, the instance layer can state things a
// project cannot take back. That is the whole of the mandatory-versus-
// overridable distinction; #Requires spells it out.
//
// This file is a draft. The decision it gives concrete form to
// (dcr-f41f718) is `proposed`, not decided: nothing reads this file, no
// enforcement point exists until Phase 3's integrator (design/roadmap.md),
// and the flake's cue-vet check deliberately does not cover it yet.
//
// Like the host and event schemas, this file is deliberately not Nix. A
// policy names checks; it never names derivations, systems, or store
// paths, so it travels between machines unchanged.
//
//   Compose an instance floor with a project policy and validate both:
//     cue vet -c schema/verification.cue \
//       examples/policy/instance/*.cue examples/policy/project/*.cue
package verification

// #Policy is one layer's contribution to a verification policy: the checks
// it can require, the path classes that require them, and what a path no
// class covers costs. Both layers are the same shape, because the
// composition is unification and unification needs both sides to agree on
// structure.
#Policy: {
	// checks maps a check's name to how that check is run. Classes
	// require checks by name; a name that is not a key here fails vet
	// (see the cross-check below), because a required check nobody can
	// run is the one failure this document must never have. Either layer
	// may contribute entries; two layers describing the same check must
	// describe it identically, or unification conflicts.
	checks: [#Name]: #Check

	// The path classes, keyed by class name. Matching is a set operation,
	// never first-match: a change's required checks are the union of
	// `requires` over every class that at least one changed path matches.
	// Classes may overlap on purpose — a knowledge node is both knowledge
	// and prose — and overlapping classes compose to the maximum of what
	// they ask for. That union is the whole of the "mixed commits take the
	// max" rule (ida-1ec03b1): there is no separate mixed case to get
	// wrong.
	//
	// The name is the key rather than a field, because classes compose
	// across the two layers and CUE unifies lists positionally — two lists
	// of different lengths simply conflict. Keying by name makes the
	// composition field-wise instead: when both layers name the same
	// class, it is one class, and its `paths` and `requires` are the union
	// of what each layer contributed.
	classes: [#Name]: #Class

	// The checks required when a changed path matches no class at all.
	// A path outside every class means the policy is behind the tree, so
	// the safe answer is the strictest set the project has rather than
	// the empty one. An empty set is allowed, and is a deliberate
	// statement that uncovered paths are unconstrained.
	unclassified: #Requires
}

// #Class is one path class and what touching it requires.
#Class: {
	// The paths this class covers, as a set of globs. Patterns are matched
	// against each changed path of the diff, relative to the repository
	// root: `*` matches within one path segment, `**` matches any number
	// of segments including none. Every side of the diff counts — an
	// added, modified, or deleted path, and both the old and the new path
	// of a rename — because a check's scope is what the tree looked like
	// as much as what it looks like now.
	//
	// A pattern's value says whether the class covers it, and it carries
	// the same mandatory bit as #Requires: a pattern the instance layer
	// pinned to `true` cannot be dropped by a project. Without that, a
	// project could narrow a floor class's coverage down to a path it
	// never touches and escape the floor everywhere else.
	paths: #Paths

	// The checks a change touching this class must carry. Every name must
	// be a key of the composed policy's `checks`.
	requires: #Requires
}

// #Requires is a set of check names written as a struct, with the value
// saying whether the check is required. The value's *form* is the one bit
// that separates a mandatory requirement from a suggested one, expressed
// in CUE's own semantics rather than in a flag this schema would have to
// interpret:
//
//   - `true` — concrete, and unification can only narrow, so a project
//     supplying `false` conflicts and `cue vet` fails naming the field.
//     This is what the instance floor writes for a check that is not
//     negotiable.
//   - `bool | *true` — a disjunction with a marked default, so it reads as
//     `true` when nobody says otherwise and unifies cleanly with a
//     project's `false`. This is what a project-type template writes for a
//     check that is a sensible starting point rather than a rule.
//
// A project may always ADD names here, because the struct is open below
// the keys the instance layer pinned; it can never remove one the floor
// made concrete.
#Requires: [#Name]: bool

// #Paths is the same set-as-struct encoding, keyed by glob rather than by
// check name, and read the same way: `true` pins the coverage, `bool |
// *true` offers it.
#Paths: [#Pattern]: bool

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

// #Templates is the instance layer's set of project types. A project
// selects one by name and then narrows it, so a project that is an
// ordinary instance of its type needs almost no configuration of its own.
#Templates: [#Name]: #Policy

// Names are lowercase, dash-separated, and short. They are quoted in
// integrator output and carried in attestation payloads, so they are held
// to the same shape as project names in schema/valley.cue.
#Name: =~"^[a-z0-9][a-z0-9-]*$"

// A path glob, relative to the repository root. A leading slash and a `..`
// segment are both rejected: a pattern that reaches outside the tree is
// describing something a diff cannot contain, and silently matching
// nothing is the expensive way to find that out.
#Pattern: =~"^[^/]" & !~"(^|/)\\.\\.(/|$)"

// ----------------------------------------------------------------------
// The composition.
//
// The two layers meet here. The schema cannot tell which file a field came
// from, so the tool that composes a policy is what makes the floor a
// floor: `floor` and `templates` must be read from the instance
// repository's integrated tip, never from the project's tree. A project
// that vendored a copy of the floor and edited the copy would unify
// perfectly well — unification forces nothing on its own. What forces is
// provenance: changing the floor is a change landing in the instance
// repository, under that repository's own policy.
//
// The two halves resolve at different times, on purpose. The floor
// resolves at head, because a floor a project could pin is not a floor.
// Templates may be pinned, because they are defaults and a stale default
// is harmless.

// The instance layer, read from the instance repository's integrated tip.
floor:     #Policy
templates: #Templates

// The project type that adds nothing, so that a project which declares no
// type still composes. It is defined here rather than left to each
// instance because it is a property of the composition, not of any group.
templates: none: {}

// The project layer, read from the project's own tree. Neither field has
// to be written: `type` defaults to the project type that adds nothing,
// and an unwritten `project` is three empty sets. A project with no policy
// document at all therefore composes — to the floor exactly, which is the
// point of having one.
type:    #Name | *"none"
project: #Policy

// The effective policy for this project: what the integrator derives a
// change's required checks from. Naming an unknown project type is an
// evaluation error rather than a silent fallback. The `let` exists so the
// cross-check below can read the composition without `policy` referring to
// itself; every error still surfaces under `policy`, where a reader is
// looking.
let composed = floor & templates[type] & project
policy: composed

// Every check a class requires, and every check `unclassified` names, must
// exist in the composed catalogue. A name that does not lands in `checks`
// as an incomplete #Check, so `cue vet -c` fails with an error naming the
// missing check. Without this, a typo is not a vet error — it is a check
// that silently stops being required. The cross-check runs against the
// composition rather than either layer, because a floor may legitimately
// require a check whose definition a template supplies.
policy: {
	for _, c in composed.classes for n, _ in c.requires {
		checks: (n): _
	}
	for n, _ in composed.unclassified {
		checks: (n): _
	}
}

// A file's top level cannot be closed (embedding a definition would open
// it instead — embedding lifts closedness), so reject stray top-level
// fields explicitly: anything but the composition's fields — a `class:`
// typo, a host-side concern like `enable:` — conflicts with this sentinel
// and fails vet with an error naming the field.
[!="floor" & !="templates" & !="type" & !="project" & !="policy"]: "INVALID: unknown top-level field; only \"floor\", \"templates\", \"type\", \"project\" and \"policy\" are allowed"
