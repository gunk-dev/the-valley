package note

// The signer projects file: which signers it restricts, to what, and the
// files it refuses to read. Whether a restricted signer's note is then
// accepted or called unknown is integrator-e2e's, over the real attest.

import (
	"strings"
	"testing"
)

func TestASignerCountsOnlyInTheProjectsListedForIt(t *testing.T) {
	s, err := ParseSignerProjects([]byte(`# workers
worker/attestations  the-valley app

retired/attestations
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, project string
		counts        bool
	}{
		{"worker/attestations", "the-valley", true},
		{"worker/attestations", "app", true},
		{"worker/attestations", "elsewhere", false},
		// A signer with no project counts nowhere.
		{"retired/attestations", "the-valley", false},
		// A signer the file does not name is not restricted.
		{"operator/attestations", "elsewhere", true},
	} {
		if got := s.Counts(c.name, c.project); got != c.counts {
			t.Errorf("%s counts in %s: %v, want %v", c.name, c.project, got, c.counts)
		}
	}
	if !s.Restricts("retired/attestations") || s.Restricts("operator/attestations") {
		t.Errorf("the file restricts the wrong signers")
	}
	var none SignerProjects
	if !none.Counts("worker/attestations", "elsewhere") || none.Restricts("worker/attestations") {
		t.Errorf("with no file, a signer is restricted")
	}
}

// Only a space separates words. Any other whitespace, Unicode's included,
// is refused inside a word rather than read as a separator, so
// "guarded<no-break space>sealed" is never read as two projects. The host
// module refuses the same words (nix/checks/hosts.nix).
func TestAMalformedSignerProjectsFileDoesNotRead(t *testing.T) {
	for _, c := range []struct{ file, says string }{
		{"w/attestations the-valley\nw/attestations app\n", "line 2: w/attestations is named on an earlier line too"},
		{"w/attestations+0a1b2c3d the-valley\n", `line 1: signer name "w/attestations+0a1b2c3d" holds a plus`},
		{"w/attestations guarded\u00a0sealed\n", "holds 0xc2"},
		{"w/attestations guarded\u2003sealed\n", "holds 0xe2"},
		{"w/attestations\u3000the-valley\n", "holds 0xe3"},
		{"w/attestations\tthe-valley\n", "holds 0x09"},
		{"w/attestations the-valley\r\n", "holds 0x0d"},
		{"w\u00e9/attestations the-valley\n", "holds 0xc3"},
	} {
		_, err := ParseSignerProjects([]byte(c.file))
		if err == nil || !strings.HasPrefix(err.Error(), "line ") || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%q: got %v, want an error saying %q", c.file, err, c.says)
		}
	}
}
