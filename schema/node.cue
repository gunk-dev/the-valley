// The structured layer of a knowledge-graph node: what a node's YAML
// frontmatter must say. The prose body is not described here — it is prose,
// and the writing conventions that govern it (.the-valley/README.md) are
// for a reader, not a validator.
//
// The convention this file makes checkable is documented in
// .the-valley/README.md: one file per node, `<id>-<slug>.md`, in the
// directory its type names. The type table below carries the directory
// names so the linter and the schema agree on them by construction.
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
	// (.the-valley/README.md, disembodied voice).
	source?: string

	if type == "outcome" {
		id:     =~"^oc-"
		status: "open" | "in-progress" | "done" | "abandoned"

		// One of the two typed edges: the node ids this outcome waits
		// on. Any node type may block an outcome. Only outcomes carry
		// it, so the field is absent — and rejected — on every other
		// type. A blocker clears when its own status is terminal for
		// its type: an idea at graduated, superseded or discarded; a
		// decision at decided or superseded; a bug at closed; an
		// outcome at done or abandoned (dcr-593d3d1).
		blocked_by: [...#Id]
	}

	if type == "bug" {
		id:     =~"^bd-"
		status: "open" | "closed"
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
// the slug (.the-valley/README.md). CUE cannot compute the hash, so only the
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
