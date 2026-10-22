package main

// The signer projects file restricts signers to projects. It is how a
// worker's key can be known to every integrator that shares the
// known-signers file while its evidence counts only in the projects it
// builds for.
//
// Each line names one signer, then the projects whose evidence it may
// sign, separated by spaces:
//
//	laddie-worker/attestations the-valley cosmo
//
// A signer the file names counts only in the projects on its line. In any
// other project the integrator classifies its notes as it classifies a
// stranger's: signed by no signer it holds. A line with a name and no
// project lets that signer count nowhere. A signer the file does not name
// is not restricted. Blank lines and lines that open with # are skipped.

import (
	"fmt"
	"os"
	"strings"

	"the-valley/note"
)

// signerProjects maps each restricted signer's name to the projects its
// evidence counts in. A nil map restricts nobody.
type signerProjects map[string]map[string]bool

// readSignerProjects reads the file at path. An empty path is no file, and
// restricts nobody. A file that cannot be read, or does not read as a
// signer projects file, is an error, so a controller given a broken file
// judges nothing rather than judging without it.
func readSignerProjects(path string) (signerProjects, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the signer projects file: %w", err)
	}
	s, err := parseSignerProjects(data)
	if err != nil {
		return nil, fmt.Errorf("the signer projects file %s does not read: %w", path, err)
	}
	return s, nil
}

// parseSignerProjects reads a signer projects file's contents. A line
// whose first word is not a name a verifier key can carry, or that names
// a signer an earlier line already named, fails the whole read, naming
// the line. Two lines for one signer would leave which of them restricts
// it to the reader's guess.
func parseSignerProjects(data []byte) (signerProjects, error) {
	s := signerProjects{}
	for i, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		name := fields[0]
		if err := note.CheckName(name); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		if _, dup := s[name]; dup {
			return nil, fmt.Errorf("line %d: %s is named on an earlier line too", i+1, name)
		}
		s[name] = map[string]bool{}
		for _, project := range fields[1:] {
			s[name][project] = true
		}
	}
	return s, nil
}

// counts says whether evidence signed under name counts in project.
func (s signerProjects) counts(name, project string) bool {
	projects, restricted := s[name]
	return !restricted || projects[project]
}
