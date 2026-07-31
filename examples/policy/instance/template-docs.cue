// A project-type template, in the same directory as the floor documents and
// distinguished from them by syntax alone: a definition contributes nothing
// to the composition until a project embeds it, where the floor documents'
// concrete fields load whether a project asks for them or not.
//
// Everything here is a default rather than a rule, so a documentation
// project gets the knowledge lint by embedding this definition and nothing
// else, and a project with reason to opt out can.
package verification

#docs: #Policy & {
	checks: "knowledge-lint": {
		runner:    "nix"
		attribute: "knowledge-lint"
	}

	// Knowledge nodes are markdown under `.the-valley/`, so they match the
	// floor's `prose` class as well as this one and require all three
	// checks.
	classes: knowledge: {
		paths: ".the-valley/**":    bool | *true
		requires: "knowledge-lint": bool | *true
	}
}
