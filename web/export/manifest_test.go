package export_test

import (
	"bytes"
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"the-valley/note"
	"the-valley/web/export"
	"the-valley/web/export/exporttest"
)

// A manifest's text reads back as the manifest it was written from.
func TestManifestRoundTrip(t *testing.T) {
	f := exporttest.New(t)
	m, err := export.ParseManifest(f.Manifest.Text())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(m.Text(), f.Manifest.Text()) {
		t.Errorf("read back as\n%s\nfrom\n%s", m.Text(), f.Manifest.Text())
	}
	if got := strings.SplitN(string(f.Manifest.Text()), "\n", 2)[0]; got != export.ManifestType {
		t.Errorf("the first line is %q", got)
	}
}

// signText signs text as the fixture's publisher would, so a test can
// sign a manifest a writer would never produce.
func signText(t *testing.T, text string) []byte {
	t.Helper()
	signed, err := note.Sign([]byte(text), exporttest.SignerName, exporttest.Key())
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// variant is the fixture's manifest with one change, signed.
func variant(t *testing.T, f *exporttest.Fixture, change func(m *export.Manifest)) []byte {
	t.Helper()
	m := *f.Manifest
	m.Projects = append([]export.ManifestProject{}, f.Manifest.Projects...)
	change(&m)
	return f.Sign(t, &m)
}

func wantCode(t *testing.T, err error, code export.Code) {
	t.Helper()
	if got := export.CodeOf(err); got != code {
		t.Fatalf("refused with %q (%v), want %q", got, err, code)
	}
}

// Step 1: a manifest is read up to 64 KiB, and one longer is refused
// without being opened.
func TestVerifyManifestReadsAtMost64KiB(t *testing.T) {
	f := exporttest.New(t)
	long := append(bytes.Repeat([]byte("x"), export.MaxManifest+1-len(f.Signed)), f.Signed...)
	_, err := export.VerifyManifest(bytes.NewReader(long), f.Admission())
	wantCode(t, err, export.CodeTooLarge)
	// The cap is on bytes read: a reader that never ends is refused too.
	_, err = export.VerifyManifest(endless{}, f.Admission())
	wantCode(t, err, export.CodeTooLarge)
}

type endless struct{}

func (endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

// Step 2: the note opens under the pinned key alone.
func TestVerifyManifestOpensUnderThePinnedKey(t *testing.T) {
	f := exporttest.New(t)
	other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	byOther, err := f.Manifest.Sign(exporttest.SignerName, other)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(f.Signed, []byte("instance valley-host"), []byte("instance valley-hosT"), 1)
	for name, signed := range map[string][]byte{
		"signed by another key": byOther,
		"text changed":          tampered,
		"no signature":          f.Manifest.Text(),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := export.VerifyManifest(bytes.NewReader(signed), f.Admission())
			wantCode(t, err, export.CodeBadSignature)
		})
	}
}

// Step 3: the first line is exactly the manifest type; a validly signed
// note of any other type is refused.
func TestVerifyManifestRequiresTheTypeLine(t *testing.T) {
	f := exporttest.New(t)
	rest := strings.SplitN(string(f.Manifest.Text()), "\n", 2)[1]
	for _, first := range []string{"the-valley/attestation/v1", "the-valley/web-export/v2", "the-valley/web-export/v1 ", ""} {
		_, err := export.VerifyManifest(bytes.NewReader(signText(t, first+"\n"+rest)), f.Admission())
		wantCode(t, err, export.CodeBadManifest)
	}
}

// Step 4: the instance and the destination are the configured ones.
func TestVerifyManifestRequiresInstanceAndDestination(t *testing.T) {
	f := exporttest.New(t)
	other := variant(t, f, func(m *export.Manifest) { m.Instance = "other-host" })
	_, err := export.VerifyManifest(bytes.NewReader(other), f.Admission())
	wantCode(t, err, export.CodeWrongInstance)
	elsewhere := variant(t, f, func(m *export.Manifest) { m.Destination = "other.example.org" })
	_, err = export.VerifyManifest(bytes.NewReader(elsewhere), f.Admission())
	wantCode(t, err, export.CodeWrongDestination)
}

// Step 5: the sequence is greater than the high-water mark. A replay and
// an older manifest are both refused as replayed, with the mark.
func TestVerifyManifestRefusesReplays(t *testing.T) {
	f := exporttest.New(t)
	for name, hw := range map[string]int64{"a replay": f.Manifest.Sequence, "an older manifest": f.Manifest.Sequence + 1} {
		t.Run(name, func(t *testing.T) {
			a := f.Admission()
			a.HighWater = hw
			_, err := export.VerifyManifest(bytes.NewReader(f.Signed), a)
			wantCode(t, err, export.CodeReplayed)
			if e := err.(*export.Error); e.HighWater != hw || e.Reply().HighWater != hw {
				t.Errorf("the refusal gives high water %d, want %d", e.HighWater, hw)
			}
		})
	}
	a := f.Admission()
	a.HighWater = f.Manifest.Sequence - 1
	if _, err := export.VerifyManifest(bytes.NewReader(f.Signed), a); err != nil {
		t.Errorf("a manifest one past the mark was refused: %v", err)
	}
}

// Step 6: the signing time is no more than three days old and no more
// than a minute ahead, both bounds inclusive.
func TestVerifyManifestBoundsTheSigningTime(t *testing.T) {
	f := exporttest.New(t)
	for _, c := range []struct {
		now time.Time
		ok  bool
	}{
		{exporttest.SignedAt.Add(export.MaxAge), true},
		{exporttest.SignedAt.Add(export.MaxAge + time.Nanosecond), false},
		{exporttest.SignedAt.Add(-export.MaxAhead), true},
		{exporttest.SignedAt.Add(-export.MaxAhead - time.Nanosecond), false},
	} {
		a := f.Admission()
		a.Now = c.now
		_, err := export.VerifyManifest(bytes.NewReader(f.Signed), a)
		if c.ok && err != nil {
			t.Errorf("at %s: %v", c.now, err)
		}
		if !c.ok {
			wantCode(t, err, export.CodeOutOfWindow)
		}
	}
}

// The steps run in their order: a manifest wrong in several ways is
// refused by the earliest step that applies.
func TestVerifyManifestRunsItsStepsInOrder(t *testing.T) {
	f := exporttest.New(t)
	late := f.Admission()
	late.Now = exporttest.SignedAt.Add(30 * 24 * time.Hour)
	late.HighWater = f.Manifest.Sequence
	late.Instance = "other-host"
	steps := []export.Code{export.CodeWrongInstance, export.CodeReplayed, export.CodeOutOfWindow}
	for _, want := range steps {
		_, err := export.VerifyManifest(bytes.NewReader(f.Signed), late)
		wantCode(t, err, want)
		switch want {
		case export.CodeWrongInstance:
			late.Instance = exporttest.Instance
		case export.CodeReplayed:
			late.HighWater = 0
		}
	}
	other := late
	other.Verifiers = []note.VerifierKey{note.NewVerifierKey("someone-else", ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, 32)).Public().(ed25519.PublicKey))}
	_, err := export.VerifyManifest(bytes.NewReader(f.Signed), other)
	wantCode(t, err, export.CodeBadSignature)
}

// The manifest's grammar: every record once where it must be, no unsafe
// name, and each field in its shape.
func TestParseManifestRefuses(t *testing.T) {
	f := exporttest.New(t)
	text := string(f.Manifest.Text())
	line := func(prefix string) string {
		for _, l := range strings.Split(text, "\n") {
			if strings.HasPrefix(l, prefix) {
				return l
			}
		}
		t.Fatalf("no line opens with %q", prefix)
		return ""
	}
	project, bundle := line("project app "), line("bundle app ")
	sha := strings.Fields(bundle)[2]
	replace := func(old, new string) string { return strings.Replace(text, old, new, 1) }
	cases := map[string]string{
		"a second instance":          text + "instance valley-host\n",
		"a second destination":       text + "destination example.org\n",
		"a second sequence":          text + "sequence 1\n",
		"a second file":              text + line("file ") + "\n",
		"a second project record":    text + project + "\n",
		"a second bundle record":     text + bundle + "\n",
		"a bundle with no project":   text + "bundle other " + sha + " 10\n",
		"a project with no bundle":   replace(bundle+"\n", ""),
		"no instance":                replace("instance valley-host\n", ""),
		"no file":                    replace(line("file ")+"\n", ""),
		"a dot-dot project":          strings.ReplaceAll(text, " app ", " .. "),
		"a dot project":              strings.ReplaceAll(text, " app ", " . "),
		"a project with a slash":     strings.ReplaceAll(text, " app ", " a/b "),
		"a project climbing out":     strings.ReplaceAll(text, " app ", " ../app "),
		"a hidden file's name":       strings.ReplaceAll(text, " app ", " .git "),
		"an instance with a slash":   replace("instance valley-host", "instance valley/host"),
		"another file":               replace("file export.json", "file other.json"),
		"a file climbing out":        replace("file export.json", "file ../export.json"),
		"an unknown record":          text + "comment hello\n",
		"a blank line":               replace("\nfile ", "\n\nfile "),
		"a field too many":           replace(project, project+" extra"),
		"two spaces":                 replace("instance valley-host", "instance  valley-host"),
		"a sequence of zero":         replace(line("sequence "), "sequence 0"),
		"a sequence with a sign":     replace(line("sequence "), "sequence +1"),
		"a sequence past int64":      replace(line("sequence "), "sequence 99999999999999999999"),
		"a size with a leading zero": replace(bundle, strings.Join(append(strings.Fields(bundle)[:3], "0"+strings.Fields(bundle)[3]), " ")),
		"a hash in uppercase":        replace(sha, strings.ToUpper(sha)),
		"a short hash":               replace(sha, sha[:63]),
		"a private project":          replace(" public ", " private "),
		"a short source tip":         replace(project, project[:len(project)-1]),
		"no closing newline":         strings.TrimSuffix(text, "\n"),
		"one hash with two sizes":    text + "project other public - " + f.Export.Projects[0].SourceTip + "\nbundle other " + sha + " 1\n",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := export.ParseManifest([]byte(text))
			wantCode(t, err, export.CodeBadManifest)
		})
	}
	// Records may come in any order, and a project with no graph has -.
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	reordered := append([]string{lines[0]}, append(lines[4:], lines[1:4]...)...)
	if _, err := export.ParseManifest([]byte(strings.Join(reordered, "\n") + "\n")); err != nil {
		t.Errorf("records in another order: %v", err)
	}
	if m, err := export.ParseManifest([]byte(strings.Replace(text, f.Export.Projects[0].GraphTip, "-", 1))); err != nil || m.Projects[0].GraphTip != "" {
		t.Errorf("a project with no graph: %v", err)
	}
}

// export.json agrees with the manifest: its hash and size, and every
// project with its visibility and both tips.
func TestCheckExport(t *testing.T) {
	f := exporttest.New(t)
	if _, err := f.Manifest.CheckExport(f.ExportJSON); err != nil {
		t.Fatal(err)
	}
	disagree := map[string]func(m *export.Manifest){
		"a source tip":   func(m *export.Manifest) { m.Projects[0].SourceTip = strings.Repeat("0", 40) },
		"a graph tip":    func(m *export.Manifest) { m.Projects[0].GraphTip = "" },
		"the project":    func(m *export.Manifest) { m.Projects[0].Name = "other" },
		"a project more": func(m *export.Manifest) { m.Projects = append(m.Projects, export.ManifestProject{Name: "zzz"}) },
	}
	for name, change := range disagree {
		t.Run(name, func(t *testing.T) {
			m := *f.Manifest
			m.Projects = append([]export.ManifestProject{}, f.Manifest.Projects...)
			change(&m)
			_, err := m.CheckExport(f.ExportJSON)
			wantCode(t, err, export.CodeBadExport)
		})
	}
	_, err := f.Manifest.CheckExport(append(append([]byte{}, f.ExportJSON...), ' '))
	wantCode(t, err, export.CodeHashMismatch)

	// A valid export.json that names the right hash but not the manifest's
	// projects is caught by the agreement, not the hash.
	other := *f.Export
	other.Groups, other.Projects = nil, nil
	data, err := other.Encode()
	if err != nil {
		t.Fatal(err)
	}
	m := *f.Manifest
	m.File = export.BodyOf(export.ExportFile, data)
	_, err = m.CheckExport(data)
	wantCode(t, err, export.CodeBadExport)
}
