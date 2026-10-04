package main

import (
	"fmt"
	"strings"
)

// protection is a project's protection block, as the host declaration
// exports it (schema/valley.cue, #Protection).
type protection struct {
	// Refs are the protected ref patterns. Only Writers may write a ref
	// one of them matches.
	Refs    []string `json:"refs"`
	Writers []string `json:"writers"`

	// Allow opens refs the default rules close, each to the principals it
	// names.
	Allow []opening `json:"allow"`
}

// opening is one allow entry: ref patterns, and who may write them.
type opening struct {
	Refs    []string `json:"refs"`
	Writers []string `json:"writers"`
}

// The namespaces whose rule is fixed rather than declared.
const (
	replacePrefix     = "refs/replace/"
	attestationPrefix = "refs/the-valley/attestations/"
	requestPrefix     = "refs/the-valley/integration-requests/"
	branchPrefix      = "refs/heads/"

	// grantRequest is the grant that opens the integration-request
	// namespace (dcr-e544f20).
	grantRequest = "request"
)

// policy is everything one push is judged against.
type policy struct {
	project    string
	protection protection
	grants     grants
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
// The rules are tried in order and the first one whose namespace matches
// decides. The order is what keeps a declaration from opening more than it
// says: replacement refs are refused before anything else is consulted, a
// protected ref stays its writers' alone, and an allow entry is consulted
// only for refs the fixed rules have not already answered for.
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

	case strings.HasPrefix(u.ref, attestationPrefix):
		// An all-zero old id is a creation, at any hash length.
		if !isZero(u.old) {
			return refuse("update", "attestation refs are create-only")
		}
		return ""
	}

	if p.protection.protects(u.ref) {
		if principal != "" && contains(p.protection.Writers, principal) {
			return ""
		}
		return refuse("write", fmt.Sprintf("a protected ref of %s (%s)", p.project, p.protection.whoMay()))
	}

	if strings.HasPrefix(u.ref, requestPrefix) {
		if principal != "" && p.grants.holds(principal, grantRequest) {
			return ""
		}
		holder := who + " holds none"
		if principal == "" {
			holder = "a key that names no principal holds no grant"
		}
		return refuse("write", fmt.Sprintf("%s takes writes only from a principal holding the %s grant, and %s", requestPrefix, grantRequest, holder))
	}

	opened := p.protection.openedTo(u.ref)
	if principal != "" && contains(opened, principal) {
		return ""
	}
	if strings.HasPrefix(u.ref, branchPrefix) {
		return ""
	}

	closed := namespace(u.ref)
	if len(opened) > 0 {
		return refuse("write", fmt.Sprintf("%s is closed to pushes, and the protection of %s opens this ref to %s only", closed, p.project, strings.Join(opened, ", ")))
	}
	return refuse("write", fmt.Sprintf("%s is closed to pushes, and no allow entry in the protection of %s opens this ref", closed, p.project))
}

// protects reports whether ref is one of the project's protected refs.
func (p protection) protects(ref string) bool {
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
func (p protection) whoMay() string {
	if len(p.Writers) == 0 {
		return "no writer is declared — changes land by integration request"
	}
	return "writers: " + strings.Join(p.Writers, ", ")
}

// openedTo lists the principals the allow entries open ref to, in the
// order they are declared.
func (p protection) openedTo(ref string) []string {
	var who []string
	for _, o := range p.Allow {
		for _, pattern := range o.Refs {
			if match(pattern, ref) {
				for _, w := range o.Writers {
					if !contains(who, w) {
						who = append(who, w)
					}
				}
				break
			}
		}
	}
	return who
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

// namespace is the part of a refname a refusal names as closed:
// refs/<kind>/, or refs/the-valley/<kind>/ for the valley's own refs.
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
