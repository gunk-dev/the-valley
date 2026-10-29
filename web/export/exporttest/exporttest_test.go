package exporttest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"testing"

	"the-valley/web/export"
	"the-valley/web/export/exporttest"
)

var update = flag.Bool("update", false, "rewrite testdata/export.json from the synthetic valley")

// The export built from the synthetic valley is the contract's expected
// output, byte for byte once indented: a change to the projection, or to
// the valley's JSON, shows here as a change to testdata/export.json.
func TestExportIsTheExpectedOne(t *testing.T) {
	f := exporttest.New(t)
	var got bytes.Buffer
	if err := json.Indent(&got, f.ExportJSON, "", "  "); err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile("testdata/export.json", got.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/export.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Errorf("the export differs from testdata/export.json; go test -run %s -update rewrites it, and the diff is the change:\n%s", t.Name(), got.Bytes())
	}
}

// The fixture is self-consistent: its manifest opens under its verifier
// and agrees with its export.json, its export directory serves main at the
// source tip, and it uploads through a store.
func TestFixtureIsConsistent(t *testing.T) {
	f := exporttest.New(t)
	ctx := context.Background()
	m, err := export.VerifyManifest(bytes.NewReader(f.Signed), f.Admission())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CheckExport(f.ExportJSON); err != nil {
		t.Fatal(err)
	}
	dir := export.Generation{Dir: f.Dir}
	for _, p := range f.Export.Projects {
		repo, _ := dir.Repo(p.Name)
		if ok, err := export.Reachable(ctx, repo, p.SourceTip, p.SourceTip); err != nil || !ok {
			t.Errorf("%s's tip is not reachable in the export directory: %v %v", p.Name, ok, err)
		}
	}
	s, err := export.OpenStore(exporttest.StorePath(t), func(err error) { t.Errorf("the store reported: %v", err) })
	if err != nil {
		t.Fatal(err)
	}
	g := f.Upload(t, s)
	if g.Sequence != exporttest.SignedAt.UnixNano() {
		t.Errorf("activated sequence %d, want the fixture's", g.Sequence)
	}
	e, err := g.Export()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Digest(); got != digest(t, f.Export) {
		t.Errorf("the activated export's digest differs from the fixture's")
	}
}

func digest(t *testing.T, e *export.Export) string {
	t.Helper()
	d, err := e.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return d
}
