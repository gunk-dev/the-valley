package export

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// ErrNoExport is Open's error when the receiver has activated no export.
var ErrNoExport = errors.New("no export has been activated")

// Store is a receiver's state directory. Its layout is:
//
//	<state>/lock                          serialises activations
//	<state>/high-water                    the greatest sequence accepted
//	<state>/active                        symlink to exports/<sequence>
//	<state>/exports/<sequence>/           one generation, immutable once active
//	    export.json
//	    repos/<project>.git
//	<state>/staging/<sequence>/           one upload, until it is committed
//	    bodies/<sha256>
//	    export.json
//	    tmp/<project>.git                 a repository being built
//	    repos/<project>.git               a repository once it is accepted
//	<state>/trash/                        generations being deleted
//
// A generation is activated by renaming its staging directory into
// exports/, writing high-water durably, and then swapping active
// atomically. A reader resolves active once per request with Open and
// keeps that generation, locked against deletion, until it releases it.
// An old generation is deleted only once no reader holds it.
//
// The locks are flock(2) locks, so readers and activations in different
// processes on one host are held to the same rule as goroutines are.
type Store struct {
	dir    string
	report func(error)
}

// Generation is one activated export: its sequence and its directory.
type Generation struct {
	Sequence int64
	Dir      string
}

// ExportPath is the generation's export.json.
func (g Generation) ExportPath() string { return filepath.Join(g.Dir, ExportFile) }

// Repo is the bare repository of the generation's project called name. ok
// is false for a name that is not a project name.
func (g Generation) Repo(name string) (path string, ok bool) {
	if !ValidName(name) {
		return "", false
	}
	return filepath.Join(g.Dir, "repos", name+".git"), true
}

// Export reads and validates the generation's export.json.
func (g Generation) Export() (*Export, error) {
	data, err := os.ReadFile(g.ExportPath())
	if err != nil {
		return nil, err
	}
	return Decode(data)
}

// Staging is one upload being received: its manifest and its directory.
type Staging struct {
	Manifest *Manifest
	Dir      string
}

// Bodies is the directory StageBody writes the upload's bodies to.
func (st Staging) Bodies() string { return filepath.Join(st.Dir, "bodies") }

// Body is where the body b is staged once it verified.
func (st Staging) Body(b Body) string { return filepath.Join(st.Bodies(), b.SHA256) }

// Repo is where the staged repository of the project called name goes.
func (st Staging) Repo(name string) string {
	return filepath.Join(st.Dir, "repos", name+".git")
}

// tmp is the directory a staged repository is built in.
func (st Staging) tmp() string { return filepath.Join(st.Dir, "tmp") }

// OpenStore opens the state directory at dir, creating its layout. It
// finishes an activation a crash interrupted after high-water was written,
// and deletes what no activation can use.
//
// report is told of each failure the store works past rather than
// returns: deleting what no activation can use, which the next sweep
// tries again; syncing a swap of active to disk, which the next
// OpenStore repeats after a crash; and a generation high-water names that
// recovery found damaged and did not activate. None undoes an activation
// that succeeded. A receiver logs them. A nil report drops them.
func OpenStore(dir string, report func(error)) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if report == nil {
		report = func(error) {}
	}
	s := &Store{dir: abs, report: report}
	for _, d := range []string{abs, s.path("exports"), s.path("staging"), s.path("trash")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := s.recover(); err != nil {
		return nil, err
	}
	if err := s.sweep(); err != nil {
		s.report(err)
	}
	return s, nil
}

// Dir is the state directory.
func (s *Store) Dir() string { return s.dir }

func (s *Store) path(parts ...string) string {
	return filepath.Join(append([]string{s.dir}, parts...)...)
}

func (s *Store) exportDir(sequence int64) string {
	return s.path("exports", strconv.FormatInt(sequence, 10))
}

// HighWater is the greatest sequence the store has accepted, 0 for none.
func (s *Store) HighWater() (int64, error) {
	data, err := os.ReadFile(s.path("high-water"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, ok := decimal(strings.TrimSuffix(string(data), "\n"))
	if !ok {
		return 0, fmt.Errorf("%s holds %q, which is not a sequence", s.path("high-water"), data)
	}
	return n, nil
}

// active is the sequence active points at, and false when there is none.
func (s *Store) active() (int64, bool, error) {
	target, err := os.Readlink(s.path("active"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	name, ok := strings.CutPrefix(target, "exports/")
	n, valid := decimal(name)
	if !ok || !valid {
		return 0, false, fmt.Errorf("%s points at %q, which is no generation", s.path("active"), target)
	}
	return n, true, nil
}

// Open resolves active once and returns that generation, held against
// deletion until release is called. A request serves everything from the
// one generation it opened, so an activation during the request never
// mixes two exports in one response. release may be called more than
// once. Open's error is ErrNoExport before the first activation.
func (s *Store) Open() (Generation, func(), error) {
	for range 16 {
		seq, ok, err := s.active()
		if err != nil {
			return Generation{}, func() {}, err
		}
		if !ok {
			return Generation{}, func() {}, ErrNoExport
		}
		dir := s.exportDir(seq)
		f, err := os.Open(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue // active moved on and the generation was deleted
		}
		if err != nil {
			return Generation{}, func() {}, err
		}
		if err := flock(f, syscall.LOCK_SH); err != nil {
			f.Close()
			return Generation{}, func() {}, err
		}
		// A deletion renames the generation away while it holds the lock
		// exclusively, so a generation still at its path once this holds
		// the lock is one no deletion will touch.
		held, errHeld := f.Stat()
		there, errThere := os.Stat(dir)
		if errHeld != nil || errThere != nil || !os.SameFile(held, there) {
			f.Close()
			continue
		}
		var once sync.Once
		release := func() { once.Do(func() { f.Close() }) }
		return Generation{Sequence: seq, Dir: dir}, release, nil
	}
	return Generation{}, func() {}, errors.New("active moved on every time it was resolved")
}

// Stage is the staging directory for an upload whose manifest
// VerifyManifest admitted. A manifest posted again finds its directory and
// the bodies already staged in it. Staging a manifest discards the staging
// directories of older ones. A manifest not newer than the high-water mark
// is refused as replayed, and one older than another staged upload as
// superseded.
func (s *Store) Stage(m *Manifest) (Staging, error) {
	unlock, err := s.lock()
	if err != nil {
		return Staging{}, err
	}
	defer unlock()
	if err := s.checkNewer(m); err != nil {
		return Staging{}, err
	}
	staged, err := s.staged()
	if err != nil {
		return Staging{}, err
	}
	for _, seq := range staged {
		if seq > m.Sequence {
			return Staging{}, refuse(CodeSuperseded, "the upload with sequence %d supersedes this one, %d", seq, m.Sequence)
		}
		if seq < m.Sequence {
			if err := removeAll(s.path("staging", strconv.FormatInt(seq, 10))); err != nil {
				return Staging{}, err
			}
		}
	}
	st := Staging{Manifest: m, Dir: s.path("staging", strconv.FormatInt(m.Sequence, 10))}
	for _, d := range []string{st.Bodies(), filepath.Join(st.Dir, "repos")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return Staging{}, err
		}
	}
	return st, nil
}

// Inventory is the reply to the staged upload's manifest: the export the
// store serves, the projects it holds, and the bodies still to be sent.
// export.json is needed until it is staged. A bundle is needed until it is
// staged or its repository is, unless the active generation holds its
// project at the declared source tip, from which Activate carries it.
func (s *Store) Inventory(st Staging) (Inventory, error) {
	inv := Inventory{Schema: InventorySchema, Sequence: st.Manifest.Sequence, Holds: []Held{}, Needs: []Need{}}
	held := map[string]string{}
	g, release, err := s.Open()
	defer release()
	switch {
	case errors.Is(err, ErrNoExport):
	case err != nil:
		return Inventory{}, err
	default:
		e, err := g.Export()
		if err != nil {
			return Inventory{}, err
		}
		digest, err := e.Digest()
		if err != nil {
			return Inventory{}, err
		}
		inv.Active = &Active{Sequence: g.Sequence, Digest: digest}
		for _, p := range e.Projects {
			inv.Holds = append(inv.Holds, Held{Project: p.Name, SourceTip: p.SourceTip})
			held[p.Name] = p.SourceTip
		}
	}
	needed := map[string]bool{}
	need := func(b Body) {
		if !needed[b.SHA256] && !exists(st.Body(b)) {
			needed[b.SHA256] = true
			inv.Needs = append(inv.Needs, Need{Name: b.Name, SHA256: b.SHA256, Size: b.Size})
		}
	}
	if !exists(filepath.Join(st.Dir, ExportFile)) {
		need(st.Manifest.File)
	}
	for _, p := range st.Manifest.Projects {
		if !exists(st.Repo(p.Name)) && held[p.Name] != p.SourceTip {
			need(p.Bundle)
		}
	}
	return inv, nil
}

// Activate commits a staged upload: it makes it the active generation, or
// refuses it and leaves the active one as it was. It holds the store's
// lock throughout, so activations are serialised. In order, it:
//
//  1. refuses the upload unless its sequence is still greater than the
//     high-water mark and nothing newer is staged, and as incomplete,
//     with the bodies still needed, unless Inventory needs none;
//  2. moves the staged export.json into place and holds it to the
//     manifest with CheckExport;
//  3. gives every project its repository: one already staged, the staged
//     bundle through AcceptBundle, or the active generation's repository
//     at the same source tip through CarryRepo. A repository is built
//     under tmp/, every file and directory of it is synced to disk, and
//     only then is it renamed into repos/;
//  4. removes the bodies and tmp/, and checks that the staging directory
//     holds export.json and exactly those repositories, each at its
//     source tip;
//  5. syncs export.json, repos/ and the staging directory, renames the
//     staging directory into exports/, and syncs both parents;
//  6. writes high-water durably, once every file and directory of the
//     generation is on disk;
//  7. swaps active to the new generation;
//  8. syncs the swap to disk, and deletes the generations no reader holds
//     and the stale staging directories.
//
// The activation has succeeded once step 7 is done: the new generation
// serves, and high-water names it, so the next OpenStore restores it
// after a crash. What fails in step 8 goes to the store's report, and
// Activate still returns the new generation.
//
// A failure before step 7 is returned, and leaves the old generation
// active. A crash before step 6 leaves the old generation active, and the
// next OpenStore deletes the new one. After step 6 the sequence is
// accepted: a commit retried after a failure between 6 and 7 is refused
// as replayed. A crash between 6 and 7 is finished by the next OpenStore,
// which swaps active to the generation high-water names.
func (s *Store) Activate(ctx context.Context, st Staging) (Generation, error) {
	unlock, err := s.lock()
	if err != nil {
		return Generation{}, err
	}
	defer unlock()
	m := st.Manifest
	if want := s.path("staging", strconv.FormatInt(m.Sequence, 10)); st.Dir != want {
		return Generation{}, fmt.Errorf("the staging directory of sequence %d is %s, not %s", m.Sequence, want, st.Dir)
	}
	if err := s.checkNewer(m); err != nil {
		return Generation{}, err
	}
	staged, err := s.staged()
	if err != nil {
		return Generation{}, err
	}
	if len(staged) == 0 || staged[len(staged)-1] != m.Sequence {
		return Generation{}, refuse(CodeSuperseded, "the upload with sequence %d is not the newest staged", m.Sequence)
	}

	inv, err := s.Inventory(st)
	if err != nil {
		return Generation{}, err
	}
	if len(inv.Needs) > 0 {
		e := refuse(CodeIncomplete, "the upload with sequence %d still needs %d bodies", m.Sequence, len(inv.Needs))
		e.Needs = inv.Needs
		return Generation{}, e
	}

	// A staged body that fails here is removed, so the inventory names it
	// as needed again rather than keep a body that cannot be activated.
	exportPath := filepath.Join(st.Dir, ExportFile)
	if !exists(exportPath) {
		if err := rename(st.Body(m.File), exportPath); err != nil {
			return Generation{}, err
		}
	}
	data, err := os.ReadFile(exportPath)
	if err != nil {
		return Generation{}, err
	}
	if _, err := m.CheckExport(data); err != nil {
		os.Remove(exportPath)
		return Generation{}, err
	}

	for _, p := range m.Projects {
		if exists(st.Repo(p.Name)) {
			continue
		}
		if err := s.build(ctx, st, p); err != nil {
			return Generation{}, err
		}
	}

	for _, d := range []string{st.Bodies(), st.tmp()} {
		if err := removeAll(d); err != nil {
			return Generation{}, err
		}
	}
	if err := checkStaged(ctx, st); err != nil {
		return Generation{}, err
	}
	for _, p := range []string{exportPath, filepath.Join(st.Dir, "repos"), st.Dir} {
		if err := syncPath(p); err != nil {
			return Generation{}, err
		}
	}
	dir := s.exportDir(m.Sequence)
	if err := rename(st.Dir, dir); err != nil {
		return Generation{}, err
	}
	for _, d := range []string{s.path("exports"), s.path("staging")} {
		if err := syncPath(d); err != nil {
			return Generation{}, err
		}
	}
	if err := writeDurably(s.path("high-water"), []byte(strconv.FormatInt(m.Sequence, 10)+"\n")); err != nil {
		return Generation{}, err
	}
	if err := s.swap(m.Sequence); err != nil {
		return Generation{}, err
	}
	if err := syncPath(s.dir); err != nil {
		s.report(fmt.Errorf("the swap of active to generation %d is not on disk: %w", m.Sequence, err))
	}
	if err := s.sweep(); err != nil {
		s.report(err)
	}
	return Generation{Sequence: m.Sequence, Dir: dir}, nil
}

// build gives the project its repository in staging, from the staged
// bundle through AcceptBundle or from the active generation through
// carry. It builds the repository under tmp/, syncs it to disk, and only
// then renames it into repos/, so a repository in repos/ is always an
// accepted one, whole on disk. A build a crash interrupted is left under
// tmp/, where the next build of the project starts again.
func (s *Store) build(ctx context.Context, st Staging, p ManifestProject) error {
	tmp := filepath.Join(st.tmp(), p.Name+".git")
	if err := removeAll(tmp); err != nil {
		return err
	}
	if err := os.MkdirAll(st.tmp(), 0o755); err != nil {
		return err
	}
	if bundle := st.Body(p.Bundle); exists(bundle) {
		if err := AcceptBundle(ctx, bundle, tmp, p.SourceTip); err != nil {
			os.Remove(bundle)
			return err
		}
	} else if err := s.carry(ctx, p, tmp); err != nil {
		return err
	}
	// git syncs what it fetches, but not what git init wrote before.
	if err := syncTree(tmp); err != nil {
		return err
	}
	return rename(tmp, st.Repo(p.Name))
}

// carry gives a project the active generation's repository, when that
// generation holds the project at the manifest's source tip.
func (s *Store) carry(ctx context.Context, p ManifestProject, repo string) error {
	g, release, err := s.Open()
	defer release()
	if err != nil && !errors.Is(err, ErrNoExport) {
		return err
	}
	if err == nil {
		e, err := g.Export()
		if err != nil {
			return err
		}
		for _, held := range e.Projects {
			if held.Name == p.Name && held.SourceTip == p.SourceTip {
				from, _ := g.Repo(p.Name)
				return CarryRepo(ctx, from, repo, p.SourceTip)
			}
		}
	}
	return refuse(CodeIncomplete, "project %s has no staged bundle, and the receiver holds no repository of it at %s", p.Name, p.SourceTip)
}

// checkStaged holds the staging directory to step 4 of Activate.
func checkStaged(ctx context.Context, st Staging) error {
	entries, err := os.ReadDir(st.Dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != ExportFile+" repos" {
		return fmt.Errorf("the staging directory %s holds %q, not export.json and repos", st.Dir, names)
	}
	repos, err := os.ReadDir(filepath.Join(st.Dir, "repos"))
	if err != nil {
		return err
	}
	if len(repos) != len(st.Manifest.Projects) {
		return fmt.Errorf("the staging directory holds %d repositories for %d projects", len(repos), len(st.Manifest.Projects))
	}
	for _, p := range st.Manifest.Projects {
		refs, err := git(ctx, st.Repo(p.Name), "for-each-ref", "--format=%(objectname) %(refname)")
		if err != nil {
			return err
		}
		if got, want := strings.TrimSuffix(string(refs), "\n"), p.SourceTip+" "+MainRef; got != want {
			return refuse(CodeBadBundle, "the staged repository of %s holds %q, not %q", p.Name, got, want)
		}
	}
	return nil
}

// errNotWhole is checkGeneration's finding that a generation is damaged.
var errNotWhole = errors.New("the generation is not whole")

// checkGeneration holds the activated generation at dir to what Activate
// checked before high-water named it, and to git fsck. export.json is a
// valid export, and each of its projects has a repository that git fsck
// finds whole and that holds refs/heads/main at the project's source tip
// and no other ref. A generation that fails gives an error that wraps
// errNotWhole. Any other error, such as git failing to start, says
// nothing about the generation.
func checkGeneration(ctx context.Context, dir string) error {
	g := Generation{Dir: dir}
	data, err := os.ReadFile(g.ExportPath())
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: it holds no export.json", errNotWhole)
	}
	if err != nil {
		return err
	}
	e, err := Decode(data)
	if err != nil {
		return fmt.Errorf("%w: %v", errNotWhole, err)
	}
	for _, p := range e.Projects {
		repo, _ := g.Repo(p.Name)
		var refs []byte
		_, err := git(ctx, repo, "fsck", "--no-progress", "--no-dangling")
		if err == nil {
			refs, err = git(ctx, repo, "for-each-ref", "--format=%(objectname) %(refname)")
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Errorf("%w: the repository of %s: %v", errNotWhole, p.Name, err)
		}
		if err != nil {
			return err
		}
		if got, want := strings.TrimSuffix(string(refs), "\n"), p.SourceTip+" "+MainRef; got != want {
			return fmt.Errorf("%w: the repository of %s holds %q, not %q", errNotWhole, p.Name, got, want)
		}
	}
	return nil
}

// checkNewer refuses a manifest whose sequence is not greater than the
// high-water mark.
func (s *Store) checkNewer(m *Manifest) error {
	hw, err := s.HighWater()
	if err != nil {
		return err
	}
	return checkSequence(m, hw)
}

// staged are the sequences with a staging directory, in ascending order.
func (s *Store) staged() ([]int64, error) {
	entries, err := os.ReadDir(s.path("staging"))
	if err != nil {
		return nil, err
	}
	var seqs []int64
	for _, e := range entries {
		if n, ok := decimal(e.Name()); ok {
			seqs = append(seqs, n)
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return seqs, nil
}

// swap points active at the generation with sequence, atomically. The
// caller syncs the state directory.
func (s *Store) swap(sequence int64) error {
	tmp := s.path("active." + random())
	if err := os.Symlink("exports/"+strconv.FormatInt(sequence, 10), tmp); err != nil {
		return err
	}
	if err := rename(tmp, s.path("active")); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// recover finishes an activation interrupted between writing high-water
// and swapping active. The generation high-water names was on disk before
// high-water was written, so it is whole unless the disk lost what it was
// told to keep. recover holds it to checkGeneration before it swaps
// active to it. A generation that fails is reported and not activated:
// the old generation keeps serving, the sweep after recovery deletes the
// damaged one, and the publisher's next manifest replaces it.
func (s *Store) recover() error {
	hw, err := s.HighWater()
	if err != nil || hw == 0 {
		return err
	}
	seq, ok, err := s.active()
	if err != nil {
		return err
	}
	if ok && seq == hw {
		return nil
	}
	dir := s.exportDir(hw)
	if !exists(dir) {
		return nil
	}
	if err := checkGeneration(context.Background(), dir); errors.Is(err, errNotWhole) {
		s.report(fmt.Errorf("generation %d, which high-water names, is not activated: %w", hw, err))
		return nil
	} else if err != nil {
		return err
	}
	if err := s.swap(hw); err != nil {
		return err
	}
	return syncPath(s.dir)
}

// sweep deletes every generation but the active one that no reader holds,
// every staging directory not newer than the high-water mark, and the
// trash. A generation a reader holds is left for a later sweep. A
// deletion that fails does not stop the others, and the next sweep tries
// it again.
func (s *Store) sweep() error {
	active, ok, err := s.active()
	if err != nil {
		return fmt.Errorf("sweeping %s: %w", s.dir, err)
	}
	hw, err := s.HighWater()
	if err != nil {
		return fmt.Errorf("sweeping %s: %w", s.dir, err)
	}
	var errs []error
	exports, err := os.ReadDir(s.path("exports"))
	errs = append(errs, err)
	for _, e := range exports {
		if n, valid := decimal(e.Name()); valid && ok && n == active {
			continue
		}
		errs = append(errs, s.delete(s.path("exports", e.Name())))
	}
	staging, err := os.ReadDir(s.path("staging"))
	errs = append(errs, err)
	for _, e := range staging {
		if n, valid := decimal(e.Name()); !valid || n <= hw {
			errs = append(errs, removeAll(s.path("staging", e.Name())))
		}
	}
	trash, err := os.ReadDir(s.path("trash"))
	errs = append(errs, err)
	for _, e := range trash {
		errs = append(errs, removeAll(s.path("trash", e.Name())))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("sweeping %s: %w", s.dir, err)
	}
	return nil
}

// delete removes the generation at dir unless a reader holds it. Holding
// the generation's lock exclusively, it renames the generation into the
// trash, so a reader that locks it afterwards finds it gone from its path.
func (s *Store) delete(dir string) error {
	f, err := os.Open(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if err := flock(f, syscall.LOCK_EX|syscall.LOCK_NB); errors.Is(err, syscall.EWOULDBLOCK) {
		return nil
	} else if err != nil {
		return err
	}
	trash := s.path("trash", filepath.Base(dir)+"-"+random())
	if err := rename(dir, trash); err != nil {
		return err
	}
	f.Close()
	return removeAll(trash)
}

// lock takes the store's activation lock, waiting for it.
func (s *Store) lock() (unlock func(), err error) {
	f, err := os.OpenFile(s.path("lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := flock(f, syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}

// flock takes a lock on f's open file description, which closing f
// releases.
func flock(f *os.File, how int) error {
	for {
		err := syscall.Flock(int(f.Fd()), how)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// writeDurably replaces the file at path with data: a temporary file
// beside it, synced, renamed over it, and the directory synced.
func writeDurably(path string, data []byte) error {
	tmp := path + "." + random()
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = syncFile(f)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return syncPath(filepath.Dir(path))
}

// syncTree syncs every file and directory in the tree at root. A symlink
// is not followed: syncing its directory makes it durable.
func syncTree(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return nil
		}
		return syncPath(path)
	})
}

// syncPath syncs the file or directory at path. Syncing a directory makes
// its entries durable.
func syncPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return syncFile(f)
}

// The calls the store syncs, renames and deletes with. A test replaces
// them to record their order or to make one fail.
var (
	syncFile  = (*os.File).Sync
	rename    = os.Rename
	removeAll = os.RemoveAll
)

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func random() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
