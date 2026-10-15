// The structured layer of a knowledge-graph node: what a node's YAML
// frontmatter must say. The prose body is not described here — it is prose,
// and the writing conventions that govern it (archive/.the-valley/README.md) are
// for a reader, not a validator.
//
// The convention this file makes checkable is documented in
// archive/.the-valley/README.md: one file per node, `<id>-<slug>.md`, in the
// directory its type names. The type table below carries the directory
// names so the linter and the schema agree on them by construction.
//
// One schema covers both graphs: the archived one under archive/.the-valley/,
// and the live plan graph at refs/the-valley/knots, where each node is a
// knot. The live graph's own fields are optional here, because the archive
// predates them. For the live graph the Go module knot/ is the authority:
// it writes and reads one canonical form of this YAML, and it holds a knot
// to rules this schema does not state, such as who may create one. The
// knot-agree check holds the two to agreeing on every rule they share.
//
// Like the host and event schemas, this file is deliberately not Nix. The
// knowledge lint in this repo is one consumer; any other tool that reads a
// graph reads the same file.
//
//   Validate one node's frontmatter:
//     cue vet -c -d '#Node' schema/node.cue <frontmatter>.yaml
package knowledge

// #Node is one node's frontmatter. The common fields are stated once and
// the per-type differences follow, because a node's type decides both its
// id prefix and the states it can be in.
#Node: {
	// The node's type. It fixes the id prefix, the status enum, and the
	// directory the file lives in.
	type: "outcome" | "bug" | "idea" | "decision"

	// The node's own id, which is also the leading token of its filename.
	id: #Id

	// The node's state. The values differ per type; see below.
	status: string

	// A one-line human title. Free prose, but never empty — the title is
	// how a node reads in a listing.
	title: string & !=""

	// The date the node was created, as YYYY-MM-DD.
	created: #Date

	// Where the content came from: a date and a venue, never a person
	// (archive/.the-valley/README.md, disembodied voice).
	source?: string

	// The live graph's fields. Every knot names the principal that created
	// it, which the push hook checks is the principal pushing it.
	created_by?: #PrincipalName

	// The outcome this node belongs to: a milestone, a work item, or a
	// residual's work item. It names an outcome in the same graph; the
	// lint resolves it.
	parent?: #OutcomeId

	// The principal this node waits on. It names who is responsible, and
	// grants nothing.
	owner?: #PrincipalName

	// Outcomes and bugs are work. They can be claimed by a run, link the
	// changes that do them, and name the project where the work lands
	// when that is not this one.
	if type == "outcome" || type == "bug" {
		claimed_by?: #PrincipalName
		claimed_at?: #Timestamp
		target?: #PrincipalName | #QualifiedId
	}

	// A decision links changes too: the change that records it in the
	// source once it is decided. An idea links none. It graduates into a
	// decision, and the decision is what crosses into the source.
	if type == "outcome" || type == "bug" || type == "decision" {
		changes?: [...#ChangeLink]
	}

	if type == "outcome" {
		id:     =~"^oc-"
		status: "open" | "in-progress" | "done" | "abandoned"

		// One of the typed edges: the nodes this outcome waits on. Any
		// node type may block an outcome or a bug. Outcomes and bugs are
		// the types that carry it, so the field is rejected on every
		// other type; an outcome always carries it, and a bug may. A
		// blocker clears when its own status is terminal for its type:
		// an idea at graduated, superseded or discarded; a decision at
		// decided or superseded; a bug at closed; an outcome at done or
		// abandoned (dcr-593d3d1). An entry is a node id in this graph,
		// or, in the live graph, <project>:<id> for a node in another
		// project's, which only the reader resolves.
		blocked_by!: [...#Ref]
	}

	if type == "bug" {
		id:     =~"^bd-"
		status: "open" | "closed"
		blocked_by?: [...#Ref]
	}

	if type == "idea" {
		id:     =~"^ida-"
		status: "raw" | "exploring" | "adopted" | "graduated" | "superseded" | "discarded"

		// An idea is transitional (dcr-593d3d1): its terminal states are
		// graduated, superseded and discarded, and `adopted` marks an idea
		// accepted and awaiting graduation. A graduated idea names where
		// its thinking now lives. That destination is its own field rather
		// than a reuse of `supersedes`, because the two edges point in
		// opposite directions — `supersedes` is carried by the surviving
		// node and names the replaced one, while graduation is declared by
		// the closing node and names its destination — and because the
		// destination may be a design document, which has no node id for
		// an id-typed edge to name. Required exactly when the status is
		// `graduated`, and rejected otherwise, so a graduation cannot be
		// half-declared.
		if status == "graduated" {
			graduated_into: #GraduationTarget
		}
	}

	if type == "decision" {
		id:     =~"^dcr-"
		status: "proposed" | "decided" | "superseded"
	}

	// The other typed edge: the node ids this node replaces. Ideas and
	// decisions carry it and no other type does, because those are the two
	// status enums with a `superseded` value for the replaced node to land
	// in — a bug is closed and an outcome is abandoned, and neither of
	// those is supersession. Optional, unlike blocked_by: most nodes
	// replace nothing, and there is no outcome-DAG reading of an empty
	// list to preserve.
	if type == "idea" || type == "decision" {
		supersedes?: [...#SupersededId]
	}
}

// A node id: a type prefix and the first 7 hex characters of the SHA-256 of
// the slug (archive/.the-valley/README.md). CUE cannot compute the hash, so only the
// shape is checked here; the lint re-derives it for the nodes the rule
// covers.
#Id: =~"^(oc|bd|ida|dcr)-[0-9a-f]{7}$"

// Where a graduated idea's thinking now lives: a decision node's id, or the
// repo-root-relative path of a design document.
#GraduationTarget: =~"^dcr-[0-9a-f]{7}$" | =~"^[a-zA-Z0-9._/-]+\\.md$"

// The id of a node something supersedes. Narrower than #Id, because only
// the two types with a `superseded` status can be superseded: an edge
// naming a bug or an outcome is wrong on shape, and saying so here is a
// better error than the lint's later complaint about a status.
#SupersededId: =~"^(ida|dcr)-[0-9a-f]{7}$"

#Date: =~"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"

// A UTC time to the second, as a claim records it.
#Timestamp: =~"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"

// An outcome's id: what a parent edge names.
#OutcomeId: =~"^oc-[0-9a-f]{7}$"

// A node in another project's graph: the project's name, a colon, and the
// node's id. Projects do not share an id space, so a reference that leaves
// the graph names the project.
#QualifiedId: =~"^[a-zA-Z0-9][a-zA-Z0-9._-]*:(oc|bd|ida|dcr)-[0-9a-f]{7}$"

// What a blocked_by entry names: a node here, or one in another project.
#Ref: #Id | #QualifiedId

// A principal's name, as schema/valley.cue states it (#PrincipalName). A
// project's name and a run's ID have the same shape.
#PrincipalName: =~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"

// A change that does a node's work: a change on a valley host,
// valley:<project>/<change ID>, or a pull request on GitHub.
#ChangeLink: =~"^valley:[a-zA-Z0-9][a-zA-Z0-9._-]*/[k-z]{32}$" | =~"^https://github\\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pull/[1-9][0-9]*$"

// #Graph is a whole graph: every node's frontmatter, keyed by the node
// file's path. The key is the path rather than the id so that a vet failure
// names the file a reader has to open. The lint builds one document in this
// shape and vets it in a single pass, which is why one run reports every
// node's errors instead of stopping at the first bad file.
#Graph: [string]: #Node

// The directory each type's nodes live in, relative to the graph root. The
// linter reads this table rather than carrying its own copy, so adding a
// node type is an edit to this file alone.
#Directories: {
	outcome:  "outcomes"
	bug:      "bugs"
	idea:     "ideas"
	decision: "decisions"
}
