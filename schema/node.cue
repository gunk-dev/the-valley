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

		// The one typed edge that exists today: the node ids this
		// outcome waits on. Any node type may block an outcome. Only
		// outcomes carry it, so the field is absent — and rejected — on
		// every other type.
		blocked_by: [...#Id]
	}

	if type == "bug" {
		id:     =~"^bd-"
		status: "open" | "closed"
	}

	if type == "idea" {
		id:     =~"^ida-"
		status: "raw" | "exploring" | "adopted" | "superseded"
	}

	if type == "decision" {
		id:     =~"^dcr-"
		status: "proposed" | "decided" | "superseded"
	}
}

// A node id: a type prefix and a short hash of the slug. The hash is not
// re-derived here — the convention names an example algorithm rather than
// pinning one, so only the shape is checkable.
#Id: =~"^(oc|bd|ida|dcr)-[0-9a-f]{7}$"

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
