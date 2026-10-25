package export

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
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
	dir string
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

// OpenStore opens the state directory at dir, creating its layout. It
// finishes an activation a crash interrupted after high-water was written,
// and deletes what no activation can use.
func OpenStore(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{dir: abs}
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
	return s, s.sweep()
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
			if err := os.RemoveAll(s.path("staging", strconv.FormatInt(seq, 10))); err != nil {
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
//     at the same source tip through CarryRepo;
//  4. checks that the staging directory holds export.json and exactly
//     those repositories, each at its source tip, and removes the bodies;
//  5. renames the staging directory into exports/;
//  6. writes high-water durably;
//  7. swaps active to the new generation;
//  8. deletes the generations no reader holds and the stale staging
//     directories.
//
// A crash before step 6 leaves the old generation active, and the next
// OpenStore deletes the new one. A crash between 6 and 7 is finished by
// the next OpenStore, which swaps active to the generation high-water
// names.
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
		if err := os.Rename(st.Body(m.File), exportPath); err != nil {
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
		repo := st.Repo(p.Name)
		if exists(repo) {
			continue
		}
		if bundle := st.Body(p.Bundle); exists(bundle) {
			if err := AcceptBundle(ctx, bundle, repo, p.SourceTip); err != nil {
				os.Remove(bundle)
				return Generation{}, err
			}
			continue
		}
		if err := s.carry(ctx, p, repo); err != nil {
			return Generation{}, err
		}
	}

	if err := os.RemoveAll(st.Bodies()); err != nil {
		return Generation{}, err
	}
	if err := checkStaged(ctx, st); err != nil {
		return Generation{}, err
	}
	for _, d := range []string{filepath.Join(st.Dir, "repos"), st.Dir} {
		if err := syncDir(d); err != nil {
			return Generation{}, err
		}
	}
	dir := s.exportDir(m.Sequence)
	if err := os.Rename(st.Dir, dir); err != nil {
		return Generation{}, err
	}
	for _, d := range []string{s.path("exports"), s.path("staging")} {
		if err := syncDir(d); err != nil {
			return Generation{}, err
		}
	}
	if err := writeDurably(s.path("high-water"), []byte(strconv.FormatInt(m.Sequence, 10)+"\n")); err != nil {
		return Generation{}, err
	}
	if err := s.swap(m.Sequence); err != nil {
		return Generation{}, err
	}
	return Generation{Sequence: m.Sequence, Dir: dir}, s.sweep()
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

// swap points active at the generation with sequence, atomically.
func (s *Store) swap(sequence int64) error {
	tmp := s.path("active." + random())
	if err := os.Symlink("exports/"+strconv.FormatInt(sequence, 10), tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path("active")); err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(s.dir)
}

// recover finishes an activation interrupted between writing high-water
// and swapping active: the generation high-water names is complete, since
// it was renamed into exports/ before high-water was written.
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
	if !exists(s.exportDir(hw)) {
		return nil
	}
	return s.swap(hw)
}

// sweep deletes every generation but the active one that no reader holds,
// every staging directory not newer than the high-water mark, and the
// trash. A generation a reader holds is left for a later sweep.
func (s *Store) sweep() error {
	active, ok, err := s.active()
	if err != nil {
		return err
	}
	hw, err := s.HighWater()
	if err != nil {
		return err
	}
	exports, err := os.ReadDir(s.path("exports"))
	if err != nil {
		return err
	}
	for _, e := range exports {
		if n, valid := decimal(e.Name()); valid && ok && n == active {
			continue
		}
		if err := s.delete(s.path("exports", e.Name())); err != nil {
			return err
		}
	}
	staging, err := os.ReadDir(s.path("staging"))
	if err != nil {
		return err
	}
	for _, e := range staging {
		if n, valid := decimal(e.Name()); !valid || n <= hw {
			if err := os.RemoveAll(s.path("staging", e.Name())); err != nil {
				return err
			}
		}
	}
	trash, err := os.ReadDir(s.path("trash"))
	if err != nil {
		return err
	}
	for _, e := range trash {
		if err := os.RemoveAll(s.path("trash", e.Name())); err != nil {
			return err
		}
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
	if err := os.Rename(dir, trash); err != nil {
		return err
	}
	f.Close()
	return os.RemoveAll(trash)
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
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func random() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
