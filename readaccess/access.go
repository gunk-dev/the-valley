// Package readaccess decides who may read a project.
//
// Read access is a grant to a principal, and it takes two consents. A
// project's own policy/read (schema/read.cue) names the principals the
// project lets read it: its Consent. The instance's identity registry
// (schema/identity.cue) grants principals read at an edge read boundary,
// each grant for a set of projects or for every project: its Grants. A
// principal may read a project only where both say so. Anyone is the
// principal for a reader who presents no credential, so a project is public
// at a destination where both consents let Anyone read it.
//
// No principal is special. The operator reads a project as any other named
// reader does: by its policy/read and an instance grant.
//
// The package has two halves. Consent, Scope and Grants hold what the two
// consents say, and Grants' methods evaluate them, with no I/O. Reader
// reads them out of git commits with cue (read.go).
package readaccess

import "slices"

// Anyone is the reserved principal for a reader who presents no credential.
const Anyone = "anyone"

// Consent is a project's consent to read access: what its policy/read
// declares at one commit.
type Consent struct {
	// Project is the project the policy is for, and Identity is its
	// publication identity: 32 lowercase hex digits that an instance grant
	// naming the project names too.
	Project, Identity string

	// Readers are the principals the project lets read it, sorted, each
	// once. No reader lets no one read.
	Readers []string
}

// Names reports whether the consent names the principal as a reader.
func (c Consent) Names(principal string) bool {
	_, found := slices.BinarySearch(c.Readers, principal)
	return found
}

// Scope is the projects one read grant covers.
type Scope struct {
	// All covers every project, whatever its identity.
	All bool

	// Projects maps each project the grant covers to the publication
	// identity the grant names for it. An empty map covers nothing.
	Projects map[string]string
}

// Covers reports whether the scope covers the project with that
// publication identity.
func (s Scope) Covers(project, identity string) bool {
	if s.All {
		return true
	}
	id, ok := s.Projects[project]
	return ok && id == identity
}

// Grants are the instance's read grants at one destination: the scopes each
// principal holds there, by principal name, with the grants to a reader who
// presents no credential under Anyone. A principal with no entry holds no
// read grant there.
type Grants map[string][]Scope

// Covers reports whether a grant of the principal covers the project with
// that publication identity: the instance's consent.
func (g Grants) Covers(principal, project, identity string) bool {
	for _, s := range g[principal] {
		if s.Covers(project, identity) {
			return true
		}
	}
	return false
}

// Selects reports whether a grant of the principal could cover the project,
// whatever identity its policy/read declares: the grant covers every
// project, or names this one. A reader of consents need read the policy of
// no project that no grant selects.
func (g Grants) Selects(principal, project string) bool {
	for _, s := range g[principal] {
		if _, named := s.Projects[project]; s.All || named {
			return true
		}
	}
	return false
}

// May reports whether the principal may read the project the consent is
// for: the consent names the principal, and a grant of the principal covers
// the project with the consent's identity.
func (g Grants) May(principal string, c Consent) bool {
	return c.Names(principal) && g.Covers(principal, c.Project, c.Identity)
}

// Readers are the project's effective readers: the principals the consent
// names that a grant covers the project for, sorted.
func (g Grants) Readers(c Consent) []string {
	var readers []string
	for _, r := range c.Readers {
		if g.Covers(r, c.Project, c.Identity) {
			readers = append(readers, r)
		}
	}
	return readers
}
