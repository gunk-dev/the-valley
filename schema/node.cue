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
// A graph holds four types of node: outcomes, bugs, ideas and questions.
// The plan graph at refs/the-valley/knots is one such graph, where each
// node is a knot, and a project may keep another in its tree, under
// .the-valley/. The plan graph's own fields are optional here, because a
// graph in a tree need not carry them. For the plan graph the Go module
// knot/ is the authority: it writes and reads one canonical form of this
// YAML, and it holds a knot to rules this schema does not state, such as
// who may create one. The knot-agree check holds the two to agreeing on
// every rule they share. The nodes under archive/.the-valley/ follow an
// earlier form of this schema, and nothing checks them against this one.
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
	type: "outcome" | "bug" | "idea" | "question"

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

	// A question links changes too: the change that records its answer in
	// the source's docs. An idea links none. It graduates into an outcome
	// or a question, and that is what links the change.
	if type == "outcome" || type == "bug" || type == "question" {
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
		// an idea at graduated, superseded or discarded; a question at
		// answered or dropped; a bug at closed; an outcome at done or
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
		// it went: an outcome, when it is taken on as work, or a question,
		// when it needs settling. That destination is its own field rather
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

		// The other typed edge: the ideas this idea replaces. Ideas carry
		// it and no other type does, because theirs is the one status enum
		// with a `superseded` value for the replaced node to land in — a
		// bug is closed, an outcome is abandoned and a question is
		// dropped, and none of those is supersession. Optional, unlike
		// blocked_by: most ideas replace nothing, and there is no
		// outcome-DAG reading of an empty list to preserve.
		supersedes?: [...#SupersededId]
	}

	// A question is something work can wait on until it is answered. Its
	// owner names who has to answer. It is open until it is answered or
	// dropped, and it is never ready: it is answered, not picked up as
	// work.
	if type == "question" {
		id:     =~"^qn-"
		status: "open" | "answered" | "dropped"
	}
}

// A node id: a type prefix and the first 7 hex characters of the SHA-256 of
// the slug (archive/.the-valley/README.md). CUE cannot compute the hash, so only the
// shape is checked here; the lint re-derives it for the nodes the rule
// covers.
#Id: =~"^(oc|bd|ida|qn)-[0-9a-f]{7}$"

// Where a graduated idea went: an outcome's or a question's id, or the
// repo-root-relative path of a design document.
#GraduationTarget: =~"^(oc|qn)-[0-9a-f]{7}$" | =~"^[a-zA-Z0-9._/-]+\\.md$"

// The id of a node something supersedes. Narrower than #Id, because only
// an idea has a `superseded` status: an edge naming an outcome, a bug or
// a question is wrong on shape, and saying so here is a better error than
// the lint's later complaint about a status.
#SupersededId: =~"^ida-[0-9a-f]{7}$"

#Date: =~"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"

// A UTC time to the second, as a claim records it.
#Timestamp: =~"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"

// An outcome's id: what a parent edge names.
#OutcomeId: =~"^oc-[0-9a-f]{7}$"

// A node in another project's graph: the project's name, a colon, and the
// node's id. Projects do not share an id space, so a reference that leaves
// the graph names the project.
#QualifiedId: =~"^[a-zA-Z0-9][a-zA-Z0-9._-]*:(oc|bd|ida|qn)-[0-9a-f]{7}$"

// What a blocked_by entry names: a node here, or one in another project.
#Ref: #Id | #QualifiedId

// A principal's name, as schema/valley.cue states it (#PrincipalName). A
// project's name and a run's ID have the same shape.
#PrincipalName: =~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"

// A change that does a node's work, or records a question's answer: a
// change on a valley host, valley:<project>/<change ID>, or a pull request
// on GitHub.
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
	question: "questions"
}
