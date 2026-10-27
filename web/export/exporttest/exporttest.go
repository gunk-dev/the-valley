// Package exporttest is the web UI's shared fixture: a complete, valid
// export built from a small synthetic valley, with its signed manifest and
// the verifier key that opens it. The publisher's, the receiver's and the
// server's tests all build it, so they test against the same bytes.
//
// The synthetic valley has two projects. app is public; site is hidden.
// app has a milestone with five children, among them a claimed knot, an
// unclaimed knot in progress, a knot blocked by a local knot and by one of
// site's, a bug whose body and comment hold HTML, and comments by mapped
// and unmapped principals, one of them edited. It has one ready change, whose earlier version,
// draft, branch and repository URL the export drops. Each project has a
// real bare repository whose main has a few commits.
//
// The valley's JSON is literal, under valley/, in the shapes valley plan,
// valley pending and valley knot show print. testdata/export.json is the
// export built from it, kept as the contract's expected output.
package exporttest

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"embed"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"the-valley/note"
	"the-valley/web/export"
)

//go:embed valley
var valley embed.FS

const (
	// Instance and Destination are what the fixture's manifest is signed
	// for, and what a receiver admitting it is configured with.
	Instance    = "valley-host"
	Destination = "example.org"
	// SignerName is the export key's name in the manifest's signature.
	SignerName = "valley-host/web-export"
)

var (
	// Collected is the synthetic valley's plan time, and SignedAt the
	// manifest's signing time, whose Unix nanoseconds are its sequence.
	Collected = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	SignedAt  = time.Date(2026, 10, 25, 12, 0, 30, 0, time.UTC)

	// Principals is the publisher's display mapping. drive-by, who wrote
	// a bug and a comment, is not in it.
	Principals = export.Principals{"app-operator": "operator", "app-agent": "agent"}

	// Visibility exports app alone.
	Visibility = map[string]export.Visibility{"app": export.Public}
)

// Key is the fixture's export key. It is derived from a fixed seed, so its
// verifier is the same in every run.
func Key() ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed([]byte("the-valley web export test key!!"))
}

// Verifier is the key a receiver pins to admit the fixture's manifest.
func Verifier() note.VerifierKey {
	return note.NewVerifierKey(SignerName, Key().Public().(ed25519.PublicKey))
}

// Fixture is the synthetic valley and everything built from it.
type Fixture struct {
	// Inputs are the synthetic valley's JSON, its visibility, its source
	// tips and the display mapping: what the projection reads.
	Inputs export.Inputs
	// Repos are both projects' bare repositories, by project name.
	Repos map[string]string

	// Export is the export built from Inputs, and ExportJSON its bytes.
	Export     *export.Export
	ExportJSON []byte
	// Bundles are each exported project's bundle of main, by project.
	Bundles map[string]string
	// Manifest is the export's manifest, and Signed the manifest signed
	// with Key.
	Manifest *export.Manifest
	Signed   []byte

	// Dir is a complete export directory, laid out as a generation is:
	// export.json and repos/<project>.git.
	Dir string
}

// New builds the fixture under a temporary directory of t's.
func New(t testing.TB) *Fixture {
	t.Helper()
	root := t.TempDir()
	f := &Fixture{Repos: map[string]string{}, Bundles: map[string]string{}}
	ctx := context.Background()

	in := export.Inputs{
		Plan:       read(t, "valley/plan.json"),
		Pending:    read(t, "valley/pending.json"),
		Visibility: Visibility,
		SourceTips: map[string]string{},
		Principals: Principals,
	}
	details, err := fs.Glob(valley, "valley/details/*/*.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range details {
		in.Details = append(in.Details, read(t, path))
	}

	for name, commits := range history {
		repo := filepath.Join(root, "valley", name+".git")
		git(t, "", nil, "init", "--quiet", "--bare", "--template=", "--initial-branch=main", repo)
		git(t, repo, fastImport(commits), "fast-import", "--quiet")
		f.Repos[name] = repo
		in.SourceTips[name] = strings.TrimSpace(string(git(t, repo, nil, "rev-parse", "refs/heads/main")))
	}
	// The source tip of a project that is not exported is never read.
	for name := range in.SourceTips {
		if _, ok := Visibility[name]; !ok {
			delete(in.SourceTips, name)
		}
	}
	f.Inputs = in

	f.Export, err = export.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	f.ExportJSON, err = f.Export.Encode()
	if err != nil {
		t.Fatal(err)
	}

	bodies := map[string]export.Body{}
	for _, p := range f.Export.Projects {
		path := filepath.Join(root, "bundles", p.Name+".bundle")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		git(t, f.Repos[p.Name], nil, "bundle", "create", "--quiet", path, "main")
		f.Bundles[p.Name] = path
		if bodies[p.Name], err = export.FileBody(p.Name, path); err != nil {
			t.Fatal(err)
		}
	}
	f.Manifest, err = export.NewManifest(Instance, Destination, SignedAt.UnixNano(), f.Export, f.ExportJSON, bodies)
	if err != nil {
		t.Fatal(err)
	}
	f.Signed = f.Sign(t, f.Manifest)

	f.Dir = filepath.Join(root, "export")
	if err := os.MkdirAll(filepath.Join(f.Dir, "repos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.Dir, export.ExportFile), f.ExportJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range f.Export.Projects {
		repo := filepath.Join(f.Dir, "repos", p.Name+".git")
		if err := export.AcceptBundle(ctx, f.Bundles[p.Name], repo, p.SourceTip); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// Sign signs a manifest with the fixture's key, so a test can sign a
// manifest it changed.
func (f *Fixture) Sign(t testing.TB, m *export.Manifest) []byte {
	t.Helper()
	signed, err := m.Sign(SignerName, Key())
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// Admission is what a receiver admits the fixture's manifest with: the
// pinned verifier, the instance and destination, no high-water mark, and a
// clock a second past the signing time.
func (f *Fixture) Admission() export.Admission {
	return export.Admission{
		Verifiers:   []note.VerifierKey{Verifier()},
		Instance:    Instance,
		Destination: Destination,
		Now:         SignedAt.Add(time.Second),
	}
}

// BodyBytes is the content of the body the manifest declares under name:
// export.json, or a project's bundle.
func (f *Fixture) BodyBytes(t testing.TB, name string) []byte {
	t.Helper()
	if name == export.ExportFile {
		return f.ExportJSON
	}
	path, ok := f.Bundles[name]
	if !ok {
		t.Fatalf("the fixture has no body %s", name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Upload runs the upload's three steps against the store, as a receiver
// does: it admits the signed manifest, stages it, stages every body the
// inventory needs, and activates it. It returns the active generation.
func (f *Fixture) Upload(t testing.TB, s *export.Store) export.Generation {
	t.Helper()
	hw, err := s.HighWater()
	if err != nil {
		t.Fatal(err)
	}
	a := f.Admission()
	a.HighWater = hw
	m, err := export.VerifyManifest(bytes.NewReader(f.Signed), a)
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.Stage(m)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := s.Inventory(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, need := range inv.Needs {
		body := f.BodyBytes(t, need.Name)
		req := httptest.NewRequest("PUT", export.BodyPath(m.Sequence, need.SHA256), bytes.NewReader(body))
		want := export.Body{Name: need.Name, SHA256: need.SHA256, Size: need.Size}
		if _, err := export.StageBody(st.Bodies(), want, 64<<20, req); err != nil {
			t.Fatal(err)
		}
	}
	g, err := s.Activate(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func read(t testing.TB, path string) []byte {
	t.Helper()
	data, err := valley.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// commit is one commit of a fixture repository's main: its time, its
// message, and the files it writes.
type commit struct {
	at      time.Time
	message string
	files   map[string]string
}

// history is each project's main, oldest commit first. Every commit's
// author, committer and time are fixed, so the tips are the same in every
// run.
var history = map[string][]commit{
	"app": {
		{time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), "app: the first README", map[string]string{
			"README.md": "# app\n\nA synthetic project for the web UI's tests.\n",
		}},
		{time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC), "app: a program", map[string]string{
			"src/main.go": "package main\n\nfunc main() {}\n",
		}},
		{time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC), "docs: a guide, and the README names it", map[string]string{
			"README.md":     "# app\n\nA synthetic project for the web UI's tests. See docs/guide.md.\n\n<script>alert(\"readme\")</script>\n",
			"docs/guide.md": "# Guide\n\nRead the plan, then the source.\n",
		}},
	},
	"site": {
		{time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), "site: hidden", map[string]string{
			"README.md": "# site\n\nThis project is not exported.\n",
		}},
	},
}

// fastImport is the git fast-import stream that writes commits to main.
func fastImport(commits []commit) []byte {
	var b bytes.Buffer
	data := func(s string) { fmt.Fprintf(&b, "data %d\n%s\n", len(s), s) }
	for _, c := range commits {
		who := fmt.Sprintf("Example Author <author@example.org> %d +0000", c.at.Unix())
		fmt.Fprintf(&b, "commit refs/heads/main\nauthor %s\ncommitter %s\n", who, who)
		data(c.message + "\n")
		var paths []string
		for path := range c.files {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			fmt.Fprintf(&b, "M 100644 inline %s\n", path)
			data(c.files[path])
		}
	}
	return b.Bytes()
}

// git runs git as the export package does: in dir unless it is "", with no
// GIT_ variable of the caller's and no system or global configuration.
func git(t testing.TB, dir string, stdin []byte, args ...string) []byte {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command(export.GitProgram, args...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	return out
}
