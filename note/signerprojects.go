package note

// The signer projects file restricts signers to projects. It sits beside a
// known-signers file: the known-signers file says which keys may sign, and
// this file says, for the signers it names, in which projects their
// evidence counts. It is how a check worker's key can be known to every
// controller that shares the known-signers file while its evidence counts
// only in the projects it builds for.
//
// Each line names one signer, then the projects its evidence counts in,
// separated by spaces:
//
//	laddie-worker/attestations the-valley cosmo
//
// A signer the file names counts only in the projects on its line, and a
// line with a name and no project lets it count nowhere. A signer the file
// does not name is not restricted. Blank lines and lines that open with #
// are skipped.
//
// Every name and project is a word of printable ASCII: no space, no other
// whitespace, no control character and nothing outside ASCII. The host
// module holds what it renders to the same rule, so the file reads back as
// it was declared. A name is also one a verifier key can carry, so it
// holds no plus.

import (
	"fmt"
	"strings"
)

// SignerProjects maps each restricted signer's name to the projects its
// evidence counts in. A nil SignerProjects restricts nobody.
type SignerProjects map[string]map[string]bool

// ParseSignerProjects reads a signer projects file's contents. A word that
// is not printable ASCII, a name a verifier key cannot carry, or a signer
// named on two lines fails the whole read, naming the line. Two lines for
// one signer would leave which of them restricts it to the reader's guess.
func ParseSignerProjects(data []byte) (SignerProjects, error) {
	s := SignerProjects{}
	for i, line := range strings.Split(string(data), "\n") {
		var words []string
		for _, w := range strings.Split(line, " ") {
			if w != "" {
				words = append(words, w)
			}
		}
		if len(words) == 0 || strings.HasPrefix(words[0], "#") {
			continue
		}
		for _, w := range words {
			if err := checkWord(w); err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
		}
		name := words[0]
		if err := CheckName(name); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		if _, dup := s[name]; dup {
			return nil, fmt.Errorf("line %d: %s is named on an earlier line too", i+1, name)
		}
		s[name] = map[string]bool{}
		for _, project := range words[1:] {
			s[name][project] = true
		}
	}
	return s, nil
}

// checkWord holds a name or a project to printable ASCII.
func checkWord(w string) error {
	for _, c := range []byte(w) {
		if c <= ' ' || c > '~' {
			return fmt.Errorf("%q holds %#02x, and a name or project is printable ASCII with no space", w, c)
		}
	}
	return nil
}

// Counts says whether evidence signed under name counts in project.
func (s SignerProjects) Counts(name, project string) bool {
	projects, restricted := s[name]
	return !restricted || projects[project]
}

// Restricts says whether the file names name at all. The controllers that
// read the file never sign under a name it names.
func (s SignerProjects) Restricts(name string) bool {
	_, restricted := s[name]
	return restricted
}
