package main

import (
	"fmt"
	"os"

	"the-valley/note"
)

// readSignerProjects reads the signer projects file at path
// (note/signerprojects.go), and returns what it says and the bytes it read.
// An empty path is no file, and restricts nobody. A file that cannot be read, or does not read as a signer
// projects file, is an error, so a controller given a broken file judges
// nothing rather than judging without it.
//
// The file may not name signer, the name the integrator signs under. A
// signer the file names signs check evidence and is restricted to
// projects; the integrator signs transfer statements, which clients
// believe in every project it serves. A worker or attester restricted
// under the integrator's own name would be both.
func readSignerProjects(path, signer string) (note.SignerProjects, []byte, error) {
	if path == "" {
		return nil, nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the signer projects file: %w", err)
	}
	s, err := note.ParseSignerProjects(data)
	if err != nil {
		return nil, nil, fmt.Errorf("the signer projects file %s does not read: %w", path, err)
	}
	if s.Restricts(signer) {
		return nil, nil, fmt.Errorf("the signer projects file %s names %s, the name the integrator signs under; a worker or attester restricted to projects signs under a name of its own", path, signer)
	}
	return s, data, nil
}
