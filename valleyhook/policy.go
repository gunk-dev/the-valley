package main

import (
	"fmt"
	"strings"

	"the-valley/note"
)

// pushPolicy is what a project declares about pushes to it, as the host
// declaration exports it (schema/valley.cue): the protected refs and their
// writers from its protection block, and its named ref grants. A project
// that declares no protection block protects nothing, and every other rule
// applies to it unchanged.
type pushPolicy struct {
	// Refs are the protected ref patterns. A ref one of them matches takes
	// a push only from one of Writers, on top of whatever else its
	// namespace requires.
	Refs    []string `json:"refs"`
	Writers []string `json:"writers"`

	// Grants open refs the namespace rules close, each to the principals
	// it names. Tags open only this way.
	Grants map[string]refGrant `json:"grants"`
}

// refGrant is one named grant: ref patterns, and who may write them.
type refGrant struct {
	Refs    []string `json:"refs"`
	Writers []string `json:"writers"`
}

// The namespaces whose rule is fixed rather than declared.
const (
	replacePrefix     = "refs/replace/"
	notesPrefix       = "refs/notes/"
	valleyPrefix      = "refs/the-valley/"
	attestationPrefix = "refs/the-valley/attestations/"
	requestPrefix     = "refs/the-valley/integration-requests/"
	branchPrefix      = "refs/heads/"

	// grantRequest is the grant that opens the integration-request
	// namespace (dcr-e544f20).
	grantRequest = "request"
)

// policy is everything one push is judged against.
type policy struct {
	project string
	push    pushPolicy
	grants  grants

	// verifiers are the keys the host accepts evidence from. An
	// attestation is accepted only under one of them.
	verifiers []note.VerifierKey

	// repo answers what the decision needs to know about the repository:
	// whether a ref is symbolic there, and what a pushed object holds.
	repo repository
}

// update is one line of what git hands a pre-receive hook.
type update struct {
	old, new, ref string
}

// decide returns "" when principal may make the write, and otherwise the
// sentence that refuses it. An empty principal is a key that names none:
// it is nobody's writer and holds no grant, so it writes only what anyone
// may.
//
// A write is allowed only if every rule that applies to the ref allows it,
// checked in this order:
//
//  1. Some refs no push may write, whatever anything declares: replacement
//     refs, notes, and the valley's own namespace apart from attestations
//     and integration requests.
//  2. A ref that is symbolic in the repository takes no push. git would
//     write the ref it points at, and that ref is not the one decided.
//  3. The ref's namespace has its own rule. Topic branches are open.
//     Attestations are create-only and must be what their name says.
//     Integration requests take the request grant. Every other ref, tags
//     included, takes a named grant that opens it to the principal.
//  4. A protected ref also takes a declared writer.
//
// Protection only ever adds a requirement. A writer of a pattern that
// covers integration requests still needs the request grant, and a writer
// of a tag pattern still needs a grant that opens the tag.
func (p policy) decide(principal string, u update) string {
	who := principal
	if who == "" {
		who = "<untagged key>"
	}
	refuse := func(verb, why string) string {
		return fmt.Sprintf("valley: %s may not %s %s — %s", who, verb, u.ref, why)
	}

	switch {
	case strings.HasPrefix(u.ref, replacePrefix):
		return refuse("write", "refs/replace/ is closed to every push: a replacement ref makes one object stand in for another wherever git looks it up, so it would change what every reader of the repository sees")
	case strings.HasPrefix(u.ref, notesPrefix):
		return refuse("write", "refs/notes/ is closed to every push: a note attaches text to an object that every reader's git shows beside it")
	case strings.HasPrefix(u.ref, valleyPrefix) &&
		!strings.HasPrefix(u.ref, attestationPrefix) && !strings.HasPrefix(u.ref, requestPrefix):
		return refuse("write", fmt.Sprintf("%s is written only by the machinery on the host, and no push may write it", namespace(u.ref)))
	}

	switch symbolic, err := p.repo.symbolic(u.ref); {
	case err != nil:
		return refuse("write", fmt.Sprintf("whether it is a symbolic ref could not be read: %v", err))
	case symbolic:
		return refuse("write", "it is a symbolic ref in this repository, and a push to it would write the ref it points at instead")
	}

	switch {
	case strings.HasPrefix(u.ref, branchPrefix):
		// Topic branches are open.

	case strings.HasPrefix(u.ref, attestationPrefix):
		if !isZero(u.old) {
			return refuse("update", "attestation refs are create-only")
		}
		if err := p.checkAttestation(u.ref, u.new); err != nil {
			return refuse("create", err.Error())
		}

	case strings.HasPrefix(u.ref, requestPrefix):
		if principal == "" || !p.grants.holds(principal, grantRequest) {
			holder := who + " holds none"
			if principal == "" {
				holder = "a key that names no principal holds no grant"
			}
			return refuse("write", fmt.Sprintf("%s takes writes only from a principal holding the %s grant, and %s", requestPrefix, grantRequest, holder))
		}

	default:
		names, opened := p.push.openedTo(u.ref)
		if principal == "" || !contains(opened, principal) {
			closed := namespace(u.ref)
			if len(opened) > 0 {
				return refuse("write", fmt.Sprintf("%s opens only through a named grant, and %s of %s opens this ref to %s only", closed, grantList(names), p.project, strings.Join(opened, ", ")))
			}
			return refuse("write", fmt.Sprintf("%s opens only through a named grant, and no grant of %s opens this ref", closed, p.project))
		}
	}

	if p.push.protects(u.ref) && (principal == "" || !contains(p.push.Writers, principal)) {
		return refuse("write", fmt.Sprintf("a protected ref of %s (%s)", p.project, p.push.whoMay()))
	}
	return ""
}

// protects reports whether ref is one of the project's protected refs.
func (p pushPolicy) protects(ref string) bool {
	for _, pattern := range p.Refs {
		if match(pattern, ref) {
			return true
		}
	}
	return false
}

// whoMay is what a refusal says about who may write a protected ref
// instead. With no writer declared there is no one, so it says how a
// change lands.
func (p pushPolicy) whoMay() string {
	if len(p.Writers) == 0 {
		return "no writer is declared — changes land by integration request"
	}
	return "writers: " + strings.Join(p.Writers, ", ")
}

// openedTo lists the grants that open ref and the principals they open it
// to, both in name order so a refusal reads the same on every push.
func (p pushPolicy) openedTo(ref string) (names, who []string) {
	for _, name := range sortedKeys(p.Grants) {
		g := p.Grants[name]
		for _, pattern := range g.Refs {
			if match(pattern, ref) {
				names = append(names, name)
				for _, w := range g.Writers {
					if !contains(who, w) {
						who = append(who, w)
					}
				}
				break
			}
		}
	}
	return names, who
}

func grantList(names []string) string {
	if len(names) == 1 {
		return "the grant " + names[0]
	}
	return "the grants " + strings.Join(names, ", ")
}

// match reports whether ref matches a declared pattern. `*` matches any
// run of characters, path separators included, which is how the patterns
// are documented (schema/valley.cue). Every other character matches
// itself: the schema refuses the characters a shell glob would read
// specially, because git refuses them in a refname too.
func match(pattern, ref string) bool {
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

// namespace is the part of a refname a refusal names: refs/<kind>/, or
// refs/the-valley/<kind>/ for the valley's own refs.
func namespace(ref string) string {
	parts := strings.Split(ref, "/")
	n := 2
	if len(parts) > 1 && parts[1] == "the-valley" {
		n = 3
	}
	if len(parts) <= n {
		return ref
	}
	return strings.Join(parts[:n], "/") + "/"
}

func isZero(id string) bool {
	return id != "" && strings.Trim(id, "0") == ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
