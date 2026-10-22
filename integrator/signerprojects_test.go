package main

// The signer projects file: which signers it restricts, to what, and the
// files it refuses to read. Whether a restricted signer's note is then
// accepted or called unknown is integrator-e2e's, over the real attest.

import (
	"strings"
	"testing"
)

func TestASignerCountsOnlyInTheProjectsListedForIt(t *testing.T) {
	s, err := parseSignerProjects([]byte(`# workers
laddie-worker/attestations the-valley cosmo

retired/attestations
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, project string
		counts        bool
	}{
		{"laddie-worker/attestations", "the-valley", true},
		{"laddie-worker/attestations", "cosmo", true},
		{"laddie-worker/attestations", "elsewhere", false},
		// A signer with no project counts nowhere.
		{"retired/attestations", "the-valley", false},
		// A signer the file does not name is not restricted.
		{"operator/attestations", "elsewhere", true},
	} {
		if got := s.counts(c.name, c.project); got != c.counts {
			t.Errorf("%s counts in %s: %v, want %v", c.name, c.project, got, c.counts)
		}
	}
	var none signerProjects
	if !none.counts("laddie-worker/attestations", "elsewhere") {
		t.Errorf("with no file, a signer is restricted")
	}
}

func TestAMalformedSignerProjectsFileDoesNotRead(t *testing.T) {
	for _, c := range []struct{ file, says string }{
		{"w/attestations the-valley\nw/attestations cosmo\n", "line 2: w/attestations is named on an earlier line too"},
		{"w/attestations+0a1b2c3d the-valley\n", "line 1: signer name \"w/attestations+0a1b2c3d\" holds a plus"},
		{"\xff the-valley\n", "line 1: signer name \"\\xff\" is not valid utf-8"},
	} {
		_, err := parseSignerProjects([]byte(c.file))
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%q: got %v, want an error saying %q", c.file, err, c.says)
		}
	}
	if _, err := readSignerProjects(t.TempDir() + "/missing"); err == nil {
		t.Errorf("a file that is not there was read")
	}
}
