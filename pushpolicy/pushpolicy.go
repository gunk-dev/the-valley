// Package pushpolicy reads a project's push policy: what the host
// declaration (schema/valley.cue) says about pushes to one project,
// exported as JSON by the host module (nix/valley-host.nix). The
// pre-receive hook reads it to decide every push, and a reader that
// shares this package reads the file exactly as the hook does.
//
// The package depends on the standard library and exactjson alone.
package pushpolicy

import (
	"fmt"
	"os"

	"the-valley/exactjson"
)

// Policy is a project's push policy: the protected refs and their writers
// from its protection block, its named ref grants, and whether it retains
// its change records. A project that declares no protection block
// protects nothing.
type Policy struct {
	// Refs are the protected ref patterns. A ref one of them matches takes
	// a push only from one of Writers, on top of whatever else its
	// namespace requires.
	Refs    []string `json:"refs"`
	Writers []string `json:"writers"`

	// Grants open refs the namespace rules close, each to the principals
	// it names. Tags open only this way.
	Grants map[string]Grant `json:"grants"`

	// RetainChangeRecords keeps every change record the project holds: no
	// push deletes one, whatever grant the pusher holds. A record's ref
	// then holds the change's whole history, so a reader can tell a
	// change that stayed ready from one withdrawn or unreadied and readied
	// again. Left out, it is false.
	RetainChangeRecords bool `json:"retainChangeRecords"`
}

// Grant is one named grant: ref patterns, and who may write them.
type Grant struct {
	Refs    []string `json:"refs"`
	Writers []string `json:"writers"`
}

// Read reads the push policy at path. A field this package does not know
// refuses the read: the policy is exported from the schema this package
// ships beside, so an unknown field means the two have drifted apart, and
// the field could be a restriction a reader would otherwise skip.
//
// Every key must spell its field exactly, and no key may appear twice in
// one object. encoding/json alone matches a key to a field without regard
// to case. A host declaration that reopens a schema definition could then
// name a writer under a field spelled another way, beside the writers a
// reviewer reads.
func Read(path string) (Policy, error) {
	var p Policy
	data, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	if err := exactjson.Decode(data, &p); err != nil {
		return Policy{}, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// Protects reports whether ref, a full ref name, is one of the project's
// protected refs.
func (p Policy) Protects(ref string) bool {
	for _, pattern := range p.Refs {
		if Match(pattern, ref) {
			return true
		}
	}
	return false
}

// Sealed reports whether ref, a full ref name, is protected and no push
// may write it: the policy declares no writer, so the ref moves only by a
// write made on the host.
func (p Policy) Sealed(ref string) bool {
	return p.Protects(ref) && len(p.Writers) == 0
}

// Match reports whether ref matches a declared pattern. `*` matches any
// run of characters, path separators included, which is how the patterns
// are documented (schema/valley.cue). Every other character matches
// itself: the schema refuses the characters a shell glob would read
// specially, because git refuses them in a refname too.
func Match(pattern, ref string) bool {
	p, r := 0, 0
	star, resume := -1, 0
	for r < len(ref) {
		switch {
		case p < len(pattern) && pattern[p] == '*':
			star, resume = p, r
			p++
		case p < len(pattern) && pattern[p] == ref[r]:
			p++
			r++
		case star >= 0:
			// Let the last star take one more character, and try the
			// rest of the pattern again from there.
			resume++
			p, r = star+1, resume
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}
