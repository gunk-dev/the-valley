package export_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"the-valley/note"
	"the-valley/webui/export"
	"the-valley/webui/export/exporttest"
)

// roleEnv names the part a run of the test binary plays when it is not
// running the tests: a receiver that commits the upload staged in a state
// directory, or the git that receiver runs.
const roleEnv = "EXPORT_TEST_ROLE"

func TestMain(m *testing.M) {
	switch os.Getenv(roleEnv) {
	case "receiver":
		os.Exit(receiver())
	case "git":
		os.Exit(gitThatCrashesOnFetch())
	}
	os.Exit(m.Run())
}

// receiver admits the signed manifest at $EXPORT_TEST_MANIFEST, stages it
// in the store at $EXPORT_TEST_STATE, and commits it, with the test binary
// as its git.
func receiver() int {
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	signed, err := os.ReadFile(os.Getenv("EXPORT_TEST_MANIFEST"))
	if err != nil {
		return fail(err)
	}
	s, err := export.OpenStore(os.Getenv("EXPORT_TEST_STATE"), func(err error) { fmt.Fprintln(os.Stderr, err) })
	if err != nil {
		return fail(err)
	}
	hw, err := s.HighWater()
	if err != nil {
		return fail(err)
	}
	m, err := export.VerifyManifest(bytes.NewReader(signed), export.Admission{
		Verifiers:   []note.VerifierKey{exporttest.Verifier()},
		Instance:    exporttest.Instance,
		Destination: exporttest.Destination,
		HighWater:   hw,
		Now:         exporttest.SignedAt.Add(time.Second),
	})
	if err != nil {
		return fail(err)
	}
	st, err := s.Stage(m)
	if err != nil {
		return fail(err)
	}
	if export.GitProgram, err = os.Executable(); err != nil {
		return fail(err)
	}
	os.Setenv(roleEnv, "git")
	if _, err := s.Activate(context.Background(), st); err != nil {
		return fail(err)
	}
	return 0
}

// gitThatCrashesOnFetch is the git at $EXPORT_TEST_GIT, except that when
// it is asked to fetch it kills the receiver that runs it, as a crash
// would, and fetches nothing.
func gitThatCrashesOnFetch() int {
	if slices.Contains(os.Args[1:], "fetch") {
		syscall.Kill(os.Getppid(), syscall.SIGKILL)
		return 1
	}
	git := os.Getenv("EXPORT_TEST_GIT")
	err := syscall.Exec(git, append([]string{git}, os.Args[1:]...), os.Environ())
	fmt.Fprintln(os.Stderr, err)
	return 1
}

// upload is a signed manifest and the bytes of every body it declares, by
// SHA-256.
type upload struct {
	m      *export.Manifest
	bodies map[string][]byte
}

func fixtureUpload(t *testing.T, f *exporttest.Fixture) upload {
	t.Helper()
	u := upload{m: f.Manifest, bodies: map[string][]byte{}}
	for _, b := range f.Manifest.Bodies() {
		u.bodies[b.SHA256] = f.BodyBytes(t, b.Name)
	}
	return u
}

// later is the fixture's upload signed later by d, with an export.json
// collected at that time: the same valley, read again.
func later(t *testing.T, f *exporttest.Fixture, d time.Duration) upload {
	t.Helper()
	e, err := export.Decode(f.ExportJSON)
	if err != nil {
		t.Fatal(err)
	}
	e.Collected = exporttest.Collected.Add(d).Format(time.RFC3339)
	data, err := e.Encode()
	if err != nil {
		t.Fatal(err)
	}
	bundles := map[string]export.Body{}
	for _, p := range f.Manifest.Projects {
		bundles[p.Name] = p.Bundle
	}
	m, err := export.NewManifest(exporttest.Instance, exporttest.Destination, exporttest.SignedAt.Add(d).UnixNano(), e, data, bundles)
	if err != nil {
		t.Fatal(err)
	}
	u := fixtureUpload(t, f)
	u.m = m
	u.bodies[m.File.SHA256] = data
	return u
}

// send runs the upload's steps against the store: it admits and stages
// the manifest, puts every body the inventory needs, and commits.
func send(t *testing.T, f *exporttest.Fixture, s *export.Store, u upload) (export.Generation, error) {
	t.Helper()
	st := stage(t, f, s, u)
	inv, err := s.Inventory(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range inv.Needs {
		putNeed(t, st, u, n)
	}
	return s.Activate(context.Background(), st)
}

func stage(t *testing.T, f *exporttest.Fixture, s *export.Store, u upload) export.Staging {
	t.Helper()
	hw, err := s.HighWater()
	if err != nil {
		t.Fatal(err)
	}
	a := f.Admission()
	a.HighWater = hw
	a.Now = u.m.SignedAt()
	m, err := export.VerifyManifest(bytes.NewReader(f.Sign(t, u.m)), a)
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.Stage(m)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func putNeed(t *testing.T, st export.Staging, u upload, n export.Need) {
	t.Helper()
	body := u.bodies[n.SHA256]
	req := httptest.NewRequest("PUT", export.BodyPath(u.m.Sequence, n.SHA256), bytes.NewReader(body))
	if _, err := export.StageBody(st.Bodies(), export.Body{Name: n.Name, SHA256: n.SHA256, Size: n.Size}, 64<<20, req); err != nil {
		t.Fatal(err)
	}
}

// openStore opens the store at dir. Nothing should go wrong in it, so
// whatever it reports fails the test.
func openStore(t *testing.T, dir string) *export.Store {
	t.Helper()
	s, err := export.OpenStore(dir, func(err error) { t.Errorf("the store reported: %v", err) })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func generations(t *testing.T, s *export.Store) []string {
	t.Helper()
	return entries(t, filepath.Join(s.Dir(), "exports"))
}

func seqName(u upload) string { return strconv.FormatInt(u.m.Sequence, 10) }

func TestStoreServesNothingBeforeTheFirstActivation(t *testing.T) {
	s := openStore(t, t.TempDir())
	_, release, err := s.Open()
	release()
	if !errors.Is(err, export.ErrNoExport) {
		t.Errorf("Open gave %v, want ErrNoExport", err)
	}
	if hw, err := s.HighWater(); hw != 0 || err != nil {
		t.Errorf("high water %d, %v", hw, err)
	}
}

// An activated upload is the directory layout the package states: the
// generation under exports/, high-water, and active pointing at it.
func TestStoreActivates(t *testing.T) {
	f := exporttest.New(t)
	s := openStore(t, t.TempDir())
	g := f.Upload(t, s)
	seq := strconv.FormatInt(f.Manifest.Sequence, 10)
	if target, err := os.Readlink(filepath.Join(s.Dir(), "active")); err != nil || target != "exports/"+seq {
		t.Errorf("active points at %q, %v", target, err)
	}
	if hw, _ := os.ReadFile(filepath.Join(s.Dir(), "high-water")); string(hw) != seq+"\n" {
		t.Errorf("high-water holds %q", hw)
	}
	if got := entries(t, g.Dir); fmt.Sprint(got) != "[export.json repos]" {
		t.Errorf("the generation holds %v", got)
	}
	if got := entries(t, filepath.Join(g.Dir, "repos")); fmt.Sprint(got) != "[app.git]" {
		t.Errorf("the generation's repositories are %v", got)
	}
	if got := entries(t, filepath.Join(s.Dir(), "staging")); len(got) != 0 {
		t.Errorf("staging still holds %v", got)
	}
	served, release, err := s.Open()
	defer release()
	if err != nil || served != g {
		t.Fatalf("Open gave %v, %v; want %v", served, err, g)
	}
	data, err := os.ReadFile(served.ExportPath())
	if err != nil || !bytes.Equal(data, f.ExportJSON) {
		t.Errorf("the served export.json differs: %v", err)
	}
	if _, ok := served.Repo("../app"); ok {
		t.Error("Repo took a name that is not a project's")
	}
}

// After an activation, the same manifest, or an older one, is refused.
func TestStoreRefusesReplays(t *testing.T) {
	f := exporttest.New(t)
	s := openStore(t, t.TempDir())
	f.Upload(t, s)
	_, err := s.Stage(f.Manifest)
	wantCode(t, err, export.CodeReplayed)
	hw, _ := s.HighWater()
	a := f.Admission()
	a.HighWater = hw
	_, err = export.VerifyManifest(bytes.NewReader(f.Signed), a)
	wantCode(t, err, export.CodeReplayed)
}

// The inventory names what is needed, and a manifest posted again finds
// what was already sent.
func TestStoreInventory(t *testing.T) {
	f := exporttest.New(t)
	s := openStore(t, t.TempDir())
	u := fixtureUpload(t, f)
	st := stage(t, f, s, u)
	inv, err := s.Inventory(st)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Active != nil || len(inv.Holds) != 0 || len(inv.Needs) != 2 || inv.Needs[0].Name != export.ExportFile || inv.Needs[1].Name != "app" {
		t.Fatalf("a fresh store's inventory is %+v", inv)
	}
	putNeed(t, st, u, inv.Needs[0])
	again := stage(t, f, s, u)
	if again.Dir != st.Dir {
		t.Errorf("the manifest posted again was staged in %s, not %s", again.Dir, st.Dir)
	}
	inv, _ = s.Inventory(again)
	if len(inv.Needs) != 1 || inv.Needs[0].Name != "app" {
		t.Errorf("after export.json the inventory needs %+v", inv.Needs)
	}
}

// A later upload of a valley whose main did not move needs export.json
// alone, and the new generation carries the held repository.
func TestStoreCarriesHeldRepositories(t *testing.T) {
	f := exporttest.New(t)
	s := openStore(t, t.TempDir())
	f.Upload(t, s)
	u := later(t, f, time.Minute)
	st := stage(t, f, s, u)
	inv, err := s.Inventory(st)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := f.Export.Digest()
	tip := f.Export.Projects[0].SourceTip
	if inv.Active == nil || inv.Active.Sequence != f.Manifest.Sequence || inv.Active.Digest != digest {
		t.Errorf("the inventory's active export is %+v", inv.Active)
	}
	if len(inv.Holds) != 1 || inv.Holds[0] != (export.Held{Project: "app", SourceTip: tip}) {
		t.Errorf("the inventory holds %+v", inv.Holds)
	}
	if len(inv.Needs) != 1 || inv.Needs[0].Name != export.ExportFile {
		t.Fatalf("the inventory needs %+v", inv.Needs)
	}
	putNeed(t, st, u, inv.Needs[0])
	g, err := s.Activate(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := g.Repo("app")
	if ok, err := export.Reachable(context.Background(), repo, tip, tip); !ok || err != nil {
		t.Errorf("the carried repository does not hold main at its tip: %v", err)
	}
	if e, err := g.Export(); err != nil || e.Collected == f.Export.Collected {
		t.Errorf("the new generation serves %v, %v", e, err)
	}
}

// A commit before every body has verified is refused with what is still
// needed, and activates nothing.
func TestStoreRefusesIncompleteCommits(t *testing.T) {
	f := exporttest.New(t)
	s := openStore(t, t.TempDir())
	st := stage(t, f, s, fixtureUpload(t, f))
	_, err := s.Activate(context.Background(), st)
	wantCode(t, err, export.CodeIncomplete)
	if needs := err.(*export.Error).Reply().Needs; len(needs) != 2 {
		t.Errorf("the refusal names %+v as needed", needs)
	}
	_, release, err := s.Open()
	release()
	if !errors.Is(err, export.ErrNoExport) {
		t.Errorf("an incomplete commit activated something: %v", err)
	}
}

// A bundle the acceptance procedure refuses at commit is removed from
// staging, so the inventory names it as needed again, and nothing is
// activated.
func TestStoreDropsABundleThatFails(t *testing.T) {
	f := exporttest.New(t)
	s := openStore(t, t.TempDir())

	// site's bundle, declared as app's: its main is not app's source tip.
	u := fixtureUpload(t, f)
	m := *u.m
	m.Projects = []export.ManifestProject{m.Projects[0]}
	wrong := filepath.Join(t.TempDir(), "site.bundle")
	gitBundle(t, f.Repos["site"], wrong)
	data, _ := os.ReadFile(wrong)
	m.Projects[0].Bundle = export.BodyOf("app", data)
	u.m = &m
	u.bodies[m.Projects[0].Bundle.SHA256] = data

	st := stage(t, f, s, u)
	inv, _ := s.Inventory(st)
	if len(inv.Needs) != 2 {
		t.Fatalf("the inventory needs %+v", inv.Needs)
	}
	for _, n := range inv.Needs {
		putNeed(t, st, u, n)
	}
	_, err := s.Activate(context.Background(), st)
	wantCode(t, err, export.CodeBadBundle)
	inv, _ = s.Inventory(st)
	if len(inv.Needs) != 1 || inv.Needs[0].Name != "app" {
		t.Errorf("after the refusal the inventory needs %+v", inv.Needs)
	}
	_, release, err := s.Open()
	release()
	if !errors.Is(err, export.ErrNoExport) {
		t.Errorf("a refused bundle activated something: %v", err)
	}
}

// A receiver that crashes after git init has made a project's repository
// and before git fetch has filled it leaves no repository the store takes
// for an accepted one. The upload posted again still has its bundle
// staged, and its commit builds the repository again.
func TestStoreRebuildsARepositoryACrashInterrupted(t *testing.T) {
	f := exporttest.New(t)
	dir := t.TempDir()
	s := openStore(t, dir)
	u := fixtureUpload(t, f)
	st := stage(t, f, s, u)
	inv, err := s.Inventory(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range inv.Needs {
		putNeed(t, st, u, n)
	}

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "manifest")
	if err := os.WriteFile(manifest, f.Signed, 0o644); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), roleEnv+"=receiver", "EXPORT_TEST_STATE="+dir,
		"EXPORT_TEST_MANIFEST="+manifest, "EXPORT_TEST_GIT="+realGit)
	out, err := cmd.CombinedOutput()
	// git fetch runs only after git init succeeded, so a receiver killed
	// at the fetch was interrupted between the two.
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("the receiver was not killed as it fetched: %v: %s", err, out)
	}
	if _, err := os.Lstat(st.Repo("app")); !os.IsNotExist(err) {
		t.Errorf("the crash left a repository at %s", st.Repo("app"))
	}

	s = openStore(t, dir)
	st = stage(t, f, s, u)
	inv, err = s.Inventory(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Needs) != 0 {
		t.Errorf("after the crash the inventory needs %+v; the bodies are still staged", inv.Needs)
	}
	g, err := s.Activate(context.Background(), st)
	if err != nil {
		t.Fatalf("the commit after the crash failed: %v", err)
	}
	repo, _ := g.Repo("app")
	tip := f.Export.Projects[0].SourceTip
	if ok, err := export.Reachable(context.Background(), repo, tip, tip); !ok || err != nil {
		t.Errorf("the rebuilt repository does not hold main at its tip: %v", err)
	}
}

func gitBundle(t *testing.T, repo, path string) {
	t.Helper()
	git(t, repo, nil, "bundle", "create", "--quiet", path, "main")
}

// A newer manifest supersedes a staged one: the older is refused, and its
// staging is discarded.
func TestStoreSupersedesOlderUploads(t *testing.T) {
	f := exporttest.New(t)
	s := openStore(t, t.TempDir())
	older, newer := fixtureUpload(t, f), later(t, f, time.Minute)
	stOlder := stage(t, f, s, older)
	stage(t, f, s, newer)
	if _, err := os.Lstat(stOlder.Dir); !os.IsNotExist(err) {
		t.Error("the older upload's staging was kept")
	}
	_, err := s.Stage(older.m)
	wantCode(t, err, export.CodeSuperseded)
	_, err = s.Activate(context.Background(), stOlder)
	wantCode(t, err, export.CodeSuperseded)
}

// A reader keeps the generation it opened for as long as it holds it, and
// the generation is deleted once it is released.
func TestStoreHoldsAGenerationForItsReader(t *testing.T) {
	f := exporttest.New(t)
	s := openStore(t, t.TempDir())
	u1, u2, u3 := fixtureUpload(t, f), later(t, f, time.Minute), later(t, f, 2*time.Minute)
	if _, err := send(t, f, s, u1); err != nil {
		t.Fatal(err)
	}
	g1, release1, err := s.Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := send(t, f, s, u2); err != nil {
		t.Fatal(err)
	}
	if e, err := g1.Export(); err != nil || e.Collected != f.Export.Collected {
		t.Fatalf("the held generation reads %v, %v", e, err)
	}
	g2, release2, _ := s.Open()
	if g2.Sequence != u2.m.Sequence {
		t.Errorf("a new request opened %d, not the new generation", g2.Sequence)
	}
	release2()
	if got := fmt.Sprint(generations(t, s)); got != fmt.Sprint([]string{seqName(u1), seqName(u2)}) {
		t.Errorf("the generations are %s", got)
	}
	release1()
	release1() // releasing twice is harmless
	if _, err := send(t, f, s, u3); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(generations(t, s)); got != fmt.Sprint([]string{seqName(u3)}) {
		t.Errorf("after release the generations are %s", got)
	}
}

// Readers running beside activations each see one whole generation: the
// export.json they read is the one uploaded under the sequence they hold.
func TestStoreReadersSeeOneGeneration(t *testing.T) {
	f := exporttest.New(t)
	s := openStore(t, t.TempDir())
	var uploads []upload
	collected := map[int64]string{}
	for i := range 6 {
		u := later(t, f, time.Duration(i)*time.Minute)
		uploads = append(uploads, u)
		e, _ := export.Decode(u.bodies[u.m.File.SHA256])
		collected[u.m.Sequence] = e.Collected
	}
	if _, err := send(t, f, s, uploads[0]); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan error, 8)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				g, release, err := s.Open()
				if err != nil {
					errs <- err
					return
				}
				e, err := g.Export()
				if err == nil && e.Collected != collected[g.Sequence] {
					err = fmt.Errorf("generation %d serves the export collected at %s", g.Sequence, e.Collected)
				}
				if _, statErr := os.Stat(filepath.Join(g.Dir, "repos", "app.git", "HEAD")); err == nil {
					err = statErr
				}
				release()
				if err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	for _, u := range uploads[1:] {
		if _, err := send(t, f, s, u); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	s = openStore(t, s.Dir())
	if got := fmt.Sprint(generations(t, s)); got != fmt.Sprint([]string{seqName(uploads[5])}) {
		t.Errorf("once every reader released, the generations are %s", got)
	}
}

// Two commits of one upload at once: the lock serialises them, so one
// activates and the other finds the upload already accepted.
func TestStoreSerialisesActivations(t *testing.T) {
	f := exporttest.New(t)
	s := openStore(t, t.TempDir())
	u := fixtureUpload(t, f)
	st := stage(t, f, s, u)
	inv, _ := s.Inventory(st)
	for _, n := range inv.Needs {
		putNeed(t, st, u, n)
	}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := s.Activate(context.Background(), st)
			results <- err
		}()
	}
	var ok, replayed int
	for range 2 {
		switch err := <-results; {
		case err == nil:
			ok++
		case export.CodeOf(err) == export.CodeReplayed:
			replayed++
		default:
			t.Errorf("a commit failed: %v", err)
		}
	}
	if ok != 1 || replayed != 1 {
		t.Errorf("%d commits activated and %d were refused as replayed; want one of each", ok, replayed)
	}
}

// A crash between writing high-water and swapping active is finished by
// the next OpenStore, which swaps active to the generation high-water
// names.
func TestStoreFinishesAnInterruptedActivation(t *testing.T) {
	f := exporttest.New(t)
	s := openStore(t, t.TempDir())
	u1, u2 := fixtureUpload(t, f), later(t, f, time.Minute)
	if _, err := send(t, f, s, u1); err != nil {
		t.Fatal(err)
	}
	_, release, _ := s.Open() // keeps the first generation from deletion
	if _, err := send(t, f, s, u2); err != nil {
		t.Fatal(err)
	}
	release()
	active := filepath.Join(s.Dir(), "active")
	os.Remove(active)
	if err := os.Symlink("exports/"+seqName(u1), active); err != nil {
		t.Fatal(err)
	}
	s = openStore(t, s.Dir())
	g, release, err := s.Open()
	defer release()
	if err != nil || g.Sequence != u2.m.Sequence {
		t.Errorf("after recovery Open gave %v, %v; want the generation high-water names", g, err)
	}
	if got := fmt.Sprint(generations(t, s)); got != fmt.Sprint([]string{seqName(u2)}) {
		t.Errorf("after recovery the generations are %s", got)
	}
}

// A crash before high-water was written leaves the old generation active;
// the next OpenStore deletes the generation that never became active, and
// stale staging and trash.
func TestStoreDiscardsAnActivationThatDidNotFinish(t *testing.T) {
	f := exporttest.New(t)
	s := openStore(t, t.TempDir())
	u1 := fixtureUpload(t, f)
	if _, err := send(t, f, s, u1); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(s.Dir(), "exports", strconv.FormatInt(u1.m.Sequence+1, 10))
	stale := filepath.Join(s.Dir(), "staging", seqName(u1))
	trash := filepath.Join(s.Dir(), "trash", "leftover")
	for _, d := range []string{filepath.Join(orphan, "repos"), stale, trash} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s = openStore(t, s.Dir())
	g, release, err := s.Open()
	defer release()
	if err != nil || g.Sequence != u1.m.Sequence {
		t.Errorf("Open gave %v, %v; want the first generation", g, err)
	}
	for _, d := range []string{orphan, stale, trash} {
		if _, err := os.Lstat(d); !os.IsNotExist(err) {
			t.Errorf("%s survived", d)
		}
	}
}

// errInjected is the failure a test makes one of the store's calls give.
var errInjected = errors.New("injected failure")

// inject replaces one of the store's file system calls until the test
// ends.
func inject[F any](t *testing.T, call *F, with F) {
	t.Helper()
	was := *call
	*call = with
	t.Cleanup(func() { *call = was })
}

// reports collects what a store reports.
type reports struct {
	mu   sync.Mutex
	errs []error
}

func (r *reports) report(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, err)
}

func (r *reports) all() []error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]error{}, r.errs...)
}

func wantServing(t *testing.T, s *export.Store, u upload) {
	t.Helper()
	g, release, err := s.Open()
	release()
	if err != nil || g.Sequence != u.m.Sequence {
		t.Errorf("Open gave %v, %v; want generation %d", g, err, u.m.Sequence)
	}
}

// A deletion that fails after the swap does not fail the commit. The new
// generation serves, the failure is reported, and the next sweep deletes
// what was left.
func TestStoreReportsASweepThatFails(t *testing.T) {
	f := exporttest.New(t)
	dir := t.TempDir()
	u1, u2 := fixtureUpload(t, f), later(t, f, time.Minute)
	if _, err := send(t, f, openStore(t, dir), u1); err != nil {
		t.Fatal(err)
	}
	var r reports
	s, err := export.OpenStore(dir, r.report)
	if err != nil {
		t.Fatal(err)
	}
	trash := filepath.Join(s.Dir(), "trash")
	inject(t, export.RemoveAll, func(path string) error {
		if strings.HasPrefix(path, trash+string(filepath.Separator)) {
			return errInjected
		}
		return os.RemoveAll(path)
	})
	g, err := send(t, f, s, u2)
	if err != nil || g.Sequence != u2.m.Sequence {
		t.Fatalf("the commit gave %v, %v; a deletion that fails after the swap does not fail it", g, err)
	}
	if errs := r.all(); len(errs) != 1 || !errors.Is(errs[0], errInjected) {
		t.Errorf("the store reported %v, want the failed deletion", errs)
	}
	wantServing(t, s, u2)
	if left := entries(t, trash); len(left) != 1 || !strings.HasPrefix(left[0], seqName(u1)+"-") {
		t.Errorf("the trash holds %v, want the first generation", left)
	}

	*export.RemoveAll = os.RemoveAll
	s = openStore(t, dir)
	if left := entries(t, trash); len(left) != 0 {
		t.Errorf("the next sweep left %v in the trash", left)
	}
	if got := fmt.Sprint(generations(t, s)); got != fmt.Sprint([]string{seqName(u2)}) {
		t.Errorf("after the next sweep the generations are %s", got)
	}
}

// A swap of active that fails fails the commit and leaves the old
// generation serving. high-water was written first, so the sequence is
// accepted, and the commit retried is refused as replayed. The
// publisher's next manifest, signed later, activates.
func TestStoreSwapThatFailsFailsTheCommit(t *testing.T) {
	f := exporttest.New(t)
	s := openStore(t, t.TempDir())
	u1, u2, u3 := fixtureUpload(t, f), later(t, f, time.Minute), later(t, f, 2*time.Minute)
	if _, err := send(t, f, s, u1); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(s.Dir(), "active")
	inject(t, export.Rename, func(old, new string) error {
		if new == active {
			return errInjected
		}
		return os.Rename(old, new)
	})
	st := stage(t, f, s, u2)
	inv, err := s.Inventory(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range inv.Needs {
		putNeed(t, st, u2, n)
	}
	if _, err := s.Activate(context.Background(), st); !errors.Is(err, errInjected) {
		t.Fatalf("the commit gave %v, want the failed swap", err)
	}
	wantServing(t, s, u1)
	if hw, _ := s.HighWater(); hw != u2.m.Sequence {
		t.Errorf("high-water is %d; it was written before the swap", hw)
	}
	_, err = s.Activate(context.Background(), st)
	wantCode(t, err, export.CodeReplayed)

	*export.Rename = os.Rename
	if _, err := send(t, f, s, u3); err != nil {
		t.Fatal(err)
	}
	wantServing(t, s, u3)
	if got := fmt.Sprint(generations(t, s)); got != fmt.Sprint([]string{seqName(u3)}) {
		t.Errorf("the generations are %s", got)
	}
}

// Once active points at the new generation the commit has succeeded. A
// failure to sync the swap to disk is reported, and the commit returns
// the new generation: after a crash, the next OpenStore swaps active to
// the generation high-water names.
func TestStoreReportsASwapNotOnDisk(t *testing.T) {
	f := exporttest.New(t)
	var r reports
	s, err := export.OpenStore(t.TempDir(), r.report)
	if err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(s.Dir(), "active")
	swapped := false
	inject(t, export.Rename, func(old, new string) error {
		err := os.Rename(old, new)
		swapped = swapped || new == active && err == nil
		return err
	})
	inject(t, export.SyncFile, func(file *os.File) error {
		if swapped && file.Name() == s.Dir() {
			return errInjected
		}
		return file.Sync()
	})
	u := fixtureUpload(t, f)
	if g, err := send(t, f, s, u); err != nil || g.Sequence != u.m.Sequence {
		t.Fatalf("the commit gave %v, %v; a swap not yet on disk does not fail it", g, err)
	}
	if errs := r.all(); len(errs) != 1 || !errors.Is(errs[0], errInjected) {
		t.Errorf("the store reported %v, want the failed sync", errs)
	}
	wantServing(t, s, u)
}
