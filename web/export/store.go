package export

import (
	"bytes"
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
	"time"
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
//	<state>/trash/                        what is being deleted: old
//	                                      generations, discarded uploads,
//	                                      and repositories past the cap
//
// staging/ and each upload in it are the receiver's alone, mode 0700, so
// no other user reads a body before it is verified. exports/ and every
// generation in it are readable by every user who can reach them:
// directories 0755 and files 0644, or 0444 for git's read-only objects,
// whatever the receiver's umask. Who can reach them is the state
// directory's own mode, which is the deployment's to set.
//
// A generation is activated by renaming its staging directory into
// exports/, writing high-water durably, and then swapping active
// atomically. A reader resolves active once per request with Open and
// keeps that generation, locked against deletion, until it releases it.
// An old generation is deleted only once no reader holds it.
//
// The locks are flock(2) locks, so readers and activations in different
// processes on one host are held to the same rule as goroutines are. A
// reader needs only to reach the state directory and read it, active and
// exports/: the receiver opens it with OpenStore, and a server with
// OpenReadOnly, which writes nothing.
type Store struct {
	// MaxStorage is the most bytes the state directory may take on disk,
	// as usage counts them, or 0 for no cap. Stage refuses a manifest
	// whose upload, by checkStorage's estimate, would take the directory
	// past it. Activate measures the directory after it builds each
	// repository, and refuses an upload that took it past. Set it before
	// the store is used.
	//
	// The cap is best effort. One repository's build can pass it by what
	// the estimate missed, and a file a process still holds open after it
	// is deleted takes space that no measure counts. The hard bound is the
	// filesystem the state directory is on.
	MaxStorage int64

	dir    string
	report func(error)
	// readOnly is set by OpenReadOnly. Such a store refuses Stage and
	// Activate.
	readOnly bool
	// turn holds a token while one of the store's goroutines holds the
	// lock, so the others wait their turn on it.
	turn chan struct{}
	// writing is the writers Writer admitted.
	writing writing
}

// writing is the writers of staged bodies that Writer admitted. Stage and
// Activate stop them before they delete the bodies they write.
type writing struct {
	mu sync.Mutex
	// open is the sequence of the upload whose bodies take writers: the
	// one Stage staged last, until Activate removes its bodies. 0 is none.
	open    int64
	writers map[*writer]bool
}

type writer struct {
	sequence int64
	stop     context.CancelCauseFunc
	done     chan struct{}
}

// RepoOverhead is what Stage counts for each repository an upload builds,
// beyond the bundle it is fetched from or the repository it is carried
// from: git's own files, the index of each pack, the commit-graph, and
// the blocks small objects take when git keeps them loose. It is an
// estimate. A repository of many objects takes more, and Activate's
// measure after each build catches that.
const RepoOverhead = 1 << 20

// errReadOnly is the error of Stage and Activate on a store OpenReadOnly
// opened.
var errReadOnly = errors.New("the state directory is open read-only")

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
// and deletes what no activation can use. If it cannot finish checking the
// generation that activation would make active, as when git is killed, it
// deletes nothing and returns the error, and the receiver tries again.
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
	s := &Store{dir: abs, report: report, turn: make(chan struct{}, 1)}
	for _, d := range []string{abs, s.path("exports"), s.path("staging"), s.path("trash")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if err := os.Chmod(s.path("exports"), 0o755); err != nil {
		return nil, err
	}
	if err := os.Chmod(s.path("staging"), 0o700); err != nil {
		return nil, err
	}
	unlock, err := s.lock(context.Background())
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := s.recover(); err != nil {
		return nil, err
	}
	if err := s.sweep(context.Background()); err != nil {
		s.report(err)
	}
	return s, nil
}

// OpenReadOnly opens the state directory at dir for reading alone, as a
// server does. It writes nothing: it creates no directory, takes no lock
// but a reader's shared one on the generation it opens, and neither
// recovers an interrupted activation nor sweeps, which the receiver's
// OpenStore does. So a server may run as a user that can only read the
// receiver's state directory. Until the receiver finishes an interrupted
// activation, a reader is served the generation active points at. dir
// must be a directory already.
func OpenReadOnly(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}
	return &Store{dir: abs, report: func(error) {}, readOnly: true, turn: make(chan struct{}, 1)}, nil
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

// Active is the sequence of the generation active points at. It reads
// active without opening the generation, so it holds nothing, and the
// generation it names may be deleted once active moves on. Its error is
// ErrNoExport before the first activation.
func (s *Store) Active() (int64, error) {
	seq, ok, err := s.active()
	if err == nil && !ok {
		err = ErrNoExport
	}
	return seq, err
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
// directories of older ones: it stops their writers (see Writer), waits
// until each has closed its files, and only then deletes the directories.
// A manifest not newer than the high-water mark is refused as replayed,
// and one older than another staged upload as superseded. One whose
// upload would take the state directory past MaxStorage is refused as
// too-large (see checkStorage), but first Stage sweeps the generations no
// reader holds any more, and measures again.
//
// Stage waits for the store's lock, measures the state directory, sweeps,
// and waits for the writers it stops, each only until ctx is done. It
// checks ctx before each directory it deletes or creates, and the sweep
// and each deletion stop part way once ctx is done. So once ctx is done,
// Stage releases the lock and deletes and creates nothing more. A deletion
// it stops part way is left in the trash, for the next sweep.
func (s *Store) Stage(ctx context.Context, m *Manifest) (Staging, error) {
	if s.readOnly {
		return Staging{}, errReadOnly
	}
	unlock, err := s.lock(ctx)
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
	var older []string
	for _, seq := range staged {
		if seq > m.Sequence {
			return Staging{}, refuse(CodeSuperseded, "the upload with sequence %d supersedes this one, %d", seq, m.Sequence)
		}
		if seq < m.Sequence {
			older = append(older, s.path("staging", strconv.FormatInt(seq, 10)))
		}
	}
	st := Staging{Manifest: m, Dir: s.path("staging", strconv.FormatInt(m.Sequence, 10))}
	discard := append(older, st.Dir)
	err = s.checkStorage(ctx, m, discard)
	if e := (*Error)(nil); errors.As(err, &e) && e.Code == CodeTooLarge && ctx.Err() == nil {
		// A generation a reader held at the last sweep may be free now.
		if err := s.sweep(ctx); err != nil {
			s.report(err)
		}
		err = s.checkStorage(ctx, m, discard)
	}
	if err != nil {
		return Staging{}, err
	}
	// Past here Stage deletes and stages, which a client gone away or out
	// of time no longer wants.
	if err := ctx.Err(); err != nil {
		return Staging{}, err
	}
	if err := s.discard(ctx, m.Sequence); err != nil {
		return Staging{}, err
	}
	for _, dir := range older {
		if err := ctx.Err(); err != nil {
			return Staging{}, err
		}
		if err := s.remove(ctx, dir); err != nil {
			return Staging{}, err
		}
	}
	for _, d := range []string{st.Dir, st.Bodies(), filepath.Join(st.Dir, "repos")} {
		if err := ctx.Err(); err != nil {
			return Staging{}, err
		}
		if err := os.MkdirAll(d, 0o700); err != nil {
			return Staging{}, err
		}
		if err := os.Chmod(d, 0o700); err != nil {
			return Staging{}, err
		}
	}
	return st, nil
}

// Writer admits a writer of the staged upload st's bodies, such as a
// request running StageBody, and returns the writer's context and done.
// The context is done once ctx is, and once the store discards st's
// bodies: when a newer manifest's Stage discards the upload, its cause is
// a superseded refusal, and when Activate removes the bodies of the upload
// it commits, a no-manifest refusal. Stage and Activate wait for done
// before they delete anything, so a writer stops once its context is done,
// closes and removes every file it opened, and only then calls done.
// Writer refuses a writer of bodies already discarded with one of those
// refusals.
func (s *Store) Writer(ctx context.Context, st Staging) (context.Context, func(), error) {
	seq := st.Manifest.Sequence
	s.writing.mu.Lock()
	defer s.writing.mu.Unlock()
	if s.writing.open != seq {
		return nil, nil, discarded(seq, s.writing.open)
	}
	wctx, stop := context.WithCancelCause(ctx)
	w := &writer{sequence: seq, stop: stop, done: make(chan struct{})}
	if s.writing.writers == nil {
		s.writing.writers = map[*writer]bool{}
	}
	s.writing.writers[w] = true
	done := sync.OnceFunc(func() {
		stop(nil)
		s.writing.mu.Lock()
		delete(s.writing.writers, w)
		s.writing.mu.Unlock()
		close(w.done)
	})
	return wctx, done, nil
}

// discard opens the bodies of the upload with sequence open to writers, or
// no upload's when open is 0. It stops the writers of every other upload's
// bodies, and waits until each has called done, or until ctx is done.
func (s *Store) discard(ctx context.Context, open int64) error {
	s.writing.mu.Lock()
	s.writing.open = open
	var stopped []*writer
	for w := range s.writing.writers {
		if w.sequence != open {
			w.stop(discarded(w.sequence, open))
			stopped = append(stopped, w)
		}
	}
	s.writing.mu.Unlock()
	for _, w := range stopped {
		select {
		case <-w.done:
		case <-ctx.Done():
			return fmt.Errorf("waiting for the writers of the upload with sequence %d to stop: %w", w.sequence, ctx.Err())
		}
	}
	return ctx.Err()
}

// discarded is the refusal of a writer of the upload with sequence once
// the bodies of the upload with sequence open take writers instead.
func discarded(sequence, open int64) error {
	if sequence < open {
		return refuse(CodeSuperseded, "the upload with sequence %d supersedes this one, %d", open, sequence)
	}
	return refuse(CodeNoManifest, "the upload with sequence %d takes no more bodies", sequence)
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
// lock throughout, so activations are serialised. It waits for the lock
// until ctx is done, and the git it runs is stopped once ctx is done. In
// order, it:
//
//  1. refuses the upload unless its sequence is still greater than the
//     high-water mark and nothing newer is staged, and as incomplete,
//     with the bodies still needed, unless Inventory needs none;
//  2. moves the staged export.json into place, readable by every user,
//     and holds it to the manifest with CheckExport;
//  3. gives every project its repository: one already staged, the staged
//     bundle through AcceptBundle, or the active generation's repository
//     at the same source tip through CarryRepo. A repository is built
//     under tmp/, made readable by every user, every file and directory
//     of it is synced to disk, and only then is it renamed into repos/.
//     Once each is in repos/, the state directory is held to MaxStorage:
//     a repository that took it past the cap is moved to the trash, and
//     the upload is refused as too-large;
//  4. stops the writers of the upload's bodies, and waits until each has
//     closed its files (see Writer); removes the bodies and tmp/; and
//     checks that the staging directory holds export.json and exactly
//     those repositories, each at its source tip;
//  5. stops if ctx is done; makes repos/ and the staging directory
//     readable by every user, syncs export.json, repos/ and the staging
//     directory, renames the staging directory into exports/, and syncs
//     both parents;
//  6. writes high-water durably, once every file and directory of the
//     generation is on disk;
//  7. swaps active to the new generation;
//  8. syncs the swap to disk, and deletes the generations no reader holds
//     and the stale staging directories, until ctx is done.
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
	if s.readOnly {
		return Generation{}, errReadOnly
	}
	unlock, err := s.lock(ctx)
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
	if err := os.Chmod(exportPath, 0o644); err != nil {
		return Generation{}, err
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
		if err := s.checkUsage(ctx); err != nil {
			// Out of repos/ at once, so no part of it is taken for an
			// accepted repository, even when ctx stops its deletion.
			if rerr := s.remove(ctx, st.Repo(p.Name)); rerr != nil {
				return Generation{}, errors.Join(err, rerr)
			}
			return Generation{}, err
		}
	}

	if err := s.discard(ctx, 0); err != nil {
		return Generation{}, err
	}
	for _, d := range []string{st.Bodies(), st.tmp()} {
		if err := removeAll(ctx, d); err != nil {
			return Generation{}, err
		}
	}
	if err := checkStaged(ctx, st); err != nil {
		return Generation{}, err
	}
	// Past here the upload is made the active generation, which a client
	// gone away or out of time no longer wants.
	if err := ctx.Err(); err != nil {
		return Generation{}, err
	}
	for _, d := range []string{filepath.Join(st.Dir, "repos"), st.Dir} {
		if err := os.Chmod(d, 0o755); err != nil {
			return Generation{}, err
		}
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
	if err := s.sweep(ctx); err != nil {
		s.report(err)
	}
	return Generation{Sequence: m.Sequence, Dir: dir}, nil
}

// build gives the project its repository in staging, from the staged
// bundle through AcceptBundle or from the active generation through
// carry. It builds the repository under tmp/, makes it readable by every
// user, syncs it to disk, and only then renames it into repos/, so a
// repository in repos/ is always an accepted one, whole on disk. A build
// a crash interrupted, or one ctx stopped, is left under tmp/, where the
// next build of the project starts again. A bundle AcceptBundle refuses
// is removed, so the inventory names it as needed again; a bundle whose
// fetch ctx stopped is kept.
func (s *Store) build(ctx context.Context, st Staging, p ManifestProject) error {
	tmp := filepath.Join(st.tmp(), p.Name+".git")
	if err := removeAll(ctx, tmp); err != nil {
		return err
	}
	if err := os.MkdirAll(st.tmp(), 0o755); err != nil {
		return err
	}
	if bundle := st.Body(p.Bundle); exists(bundle) {
		if err := AcceptBundle(ctx, bundle, tmp, p.SourceTip); err != nil {
			if e := (*Error)(nil); errors.As(err, &e) && ctx.Err() == nil {
				os.Remove(bundle)
			}
			return err
		}
	} else if err := s.carry(ctx, p, tmp); err != nil {
		return err
	}
	if err := readable(tmp); err != nil {
		return err
	}
	// git syncs what it fetches, but not what git init wrote before, nor
	// the modes readable set.
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
// errNotWhole. Any other error says nothing about the generation: the
// check did not finish, as when git is killed or fails to start.
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
		if _, err := os.Lstat(repo); errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: the repository of %s is gone", errNotWhole, p.Name)
		} else if err != nil {
			return err
		}
		finding, err := fsck(ctx, repo)
		if err != nil {
			return fmt.Errorf("the check of the repository of %s did not finish: %w", p.Name, err)
		}
		if finding != "" {
			return fmt.Errorf("%w: git fsck finds the repository of %s damaged: %s", errNotWhole, p.Name, finding)
		}
		refs, err := git(ctx, repo, "for-each-ref", "--format=%(objectname) %(refname)")
		if err != nil {
			return err
		}
		if got, want := strings.TrimSuffix(string(refs), "\n"), p.SourceTip+" "+MainRef; got != want {
			return fmt.Errorf("%w: the repository of %s holds %q, not %q", errNotWhole, p.Name, got, want)
		}
	}
	return nil
}

// fsck runs git fsck on the repository at repo. finding is the first
// problem fsck names when it finds the repository damaged, and "" when it
// finds it whole. fsck finds damage only when git exits on its own with a
// non-zero status and names a problem. Anything else is an error, which
// says nothing about the repository: git killed by a signal, failing to
// start, outliving ctx, or exiting without naming a problem, as it does
// when it runs out of memory.
func fsck(ctx context.Context, repo string) (finding string, err error) {
	// --git-dir, unlike -C, never lets git check a repository it finds
	// above repo. LC_ALL=C keeps git's messages in the words fsckFinding
	// reads.
	cmd := command(ctx, "", "--git-dir="+repo, "fsck", "--no-progress", "--no-dangling")
	cmd.Env = append(cmd.Env, "LC_ALL=C")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	if err == nil {
		return "", nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.Exited() && ctx.Err() == nil {
		if finding := fsckFinding(out.String()); finding != "" {
			return finding, nil
		}
	}
	last := strings.TrimSpace(out.String())
	if i := strings.LastIndexByte(last, '\n'); i >= 0 {
		last = last[i+1:]
	}
	if last == "" {
		return "", fmt.Errorf("git fsck: %w", err)
	}
	return "", fmt.Errorf("git fsck: %w: %s", err, last)
}

// fsckFinding is the first line of git fsck's output that names a problem
// with the repository, and "" when none does. fsck reports each problem it
// finds on a line of its own: an error, a missing object, or a broken
// link. git refuses a directory that holds no repository, and dies on an
// object it finds corrupt. A failure of git's own, such as malloc failing,
// names no problem.
func fsckFinding(out string) string {
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "error: "),
			strings.HasPrefix(line, "missing "),
			strings.HasPrefix(line, "broken link from "),
			strings.HasPrefix(line, "fatal: not a git repository"),
			strings.HasPrefix(line, "fatal: ") && strings.HasSuffix(line, " is corrupt"):
			return line
		}
	}
	return ""
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
// damaged one, and the publisher's next manifest replaces it. A check
// that did not finish says nothing about the generation, so recover
// returns its error, and OpenStore deletes nothing and fails, to be tried
// again.
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
		return fmt.Errorf("generation %d, which high-water names, is kept unchecked: %w", hw, err)
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
// it again. Once ctx is done, sweep stops, even part way through a
// deletion, and fails with ctx's error; the next sweep finishes the work.
func (s *Store) sweep(ctx context.Context) error {
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
		if ctx.Err() != nil {
			break
		}
		if n, valid := decimal(e.Name()); valid && ok && n == active {
			continue
		}
		errs = append(errs, s.delete(ctx, s.path("exports", e.Name())))
	}
	staging, err := os.ReadDir(s.path("staging"))
	errs = append(errs, err)
	for _, e := range staging {
		if ctx.Err() != nil {
			break
		}
		if n, valid := decimal(e.Name()); !valid || n <= hw {
			errs = append(errs, removeAll(ctx, s.path("staging", e.Name())))
		}
	}
	trash, err := os.ReadDir(s.path("trash"))
	errs = append(errs, err)
	for _, e := range trash {
		if ctx.Err() != nil {
			break
		}
		errs = append(errs, removeAll(ctx, s.path("trash", e.Name())))
	}
	// A deletion ctx stopped has said so already.
	if err := ctx.Err(); err != nil && !errors.Is(errors.Join(errs...), err) {
		errs = append(errs, fmt.Errorf("stopped: %w", err))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("sweeping %s: %w", s.dir, err)
	}
	return nil
}

// delete removes the generation at dir unless a reader holds it. Holding
// the generation's lock exclusively, it renames the generation into the
// trash, so a reader that locks it afterwards finds it gone from its path.
// It then deletes it there until ctx is done.
func (s *Store) delete(ctx context.Context, dir string) error {
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
	return removeAll(ctx, trash)
}

// remove deletes the tree at path. It renames the tree into the trash, so
// the tree is gone from path at once and whole until then, and deletes it
// there until ctx is done. What ctx stops is left in the trash for the
// next sweep. A path that does not exist is no error.
func (s *Store) remove(ctx context.Context, path string) error {
	trash := s.path("trash", filepath.Base(path)+"-"+random())
	if err := rename(path, trash); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return removeAll(ctx, trash)
}

// removeTree deletes the tree at path, as os.RemoveAll does, but checks
// ctx before it deletes each entry, and stops with ctx's error once ctx is
// done. A symlink is deleted, not followed. A path that does not exist is
// no error.
func removeTree(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("deleting %s: %w", path, err)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		for _, e := range entries {
			if err := removeTree(ctx, filepath.Join(path, e.Name())); err != nil {
				return err
			}
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// lock takes the store's activation lock, waiting for it until ctx is
// done, and fails once ctx is done, even when the lock is free. The
// store's own goroutines wait their turn on s.turn. The flock(2) lock then
// holds off other processes, and lock polls for it, since a process
// cannot stop waiting in flock(2) when ctx is done.
func (s *Store) lock(ctx context.Context) (unlock func(), err error) {
	waiting := func() error { return fmt.Errorf("waiting for the lock on %s: %w", s.dir, ctx.Err()) }
	if ctx.Err() != nil {
		return nil, waiting()
	}
	select {
	case s.turn <- struct{}{}:
	case <-ctx.Done():
		return nil, waiting()
	}
	f, err := os.OpenFile(s.path("lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		<-s.turn
		return nil, err
	}
	for pause := time.Millisecond; ; pause = min(2*pause, 100*time.Millisecond) {
		err := flock(f, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			<-s.turn
			return nil, err
		}
		select {
		case <-time.After(pause):
		case <-ctx.Done():
			f.Close()
			<-s.turn
			return nil, waiting()
		}
	}
	unlock = func() {
		f.Close()
		<-s.turn
	}
	// ctx may have been done while flock(2) succeeded, and the caller
	// does no work once it is.
	if ctx.Err() != nil {
		unlock()
		return nil, waiting()
	}
	return unlock, nil
}

// checkStorage refuses m as too-large when its upload would take the
// state directory past MaxStorage, and stops measuring once ctx is done.
// It counts what the directory holds now,
// less the staging directories in discard, which are older uploads Stage
// discards and m's own. To that it adds everything the upload may write:
// every body m declares, once for each SHA-256, since the publisher may
// send any of them; and a repository for every project, each counted
// apart, even when two projects name one bundle. A project's repository
// counts as large as its bundle, or as the active generation's repository
// of it at the same source tip, which Activate carries, when that is
// larger; and RepoOverhead more.
func (s *Store) checkStorage(ctx context.Context, m *Manifest, discard []string) error {
	if s.MaxStorage <= 0 {
		return nil
	}
	skip := map[string]bool{}
	for _, dir := range discard {
		skip[dir] = true
	}
	held, err := usage(ctx, s.dir, skip)
	if err != nil {
		return err
	}
	carried := map[string]string{}
	g, release, err := s.Open()
	defer release()
	switch {
	case errors.Is(err, ErrNoExport):
	case err != nil:
		return err
	default:
		e, err := g.Export()
		if err != nil {
			return err
		}
		for _, p := range e.Projects {
			carried[p.Name+" "+p.SourceTip], _ = g.Repo(p.Name)
		}
	}
	total, fits := held, true
	add := func(n int64) {
		// Compared before it is added, so the sum never overflows.
		if !fits || n > s.MaxStorage-total {
			fits = false
			return
		}
		total += n
	}
	counted := map[string]bool{}
	for _, b := range m.Bodies() {
		if !counted[b.SHA256] {
			counted[b.SHA256] = true
			add(b.Size)
		}
	}
	for _, p := range m.Projects {
		repo := p.Bundle.Size
		if from, ok := carried[p.Name+" "+p.SourceTip]; ok {
			n, err := usage(ctx, from, nil)
			if err != nil {
				return err
			}
			repo = max(repo, n)
		}
		add(repo)
		add(RepoOverhead)
	}
	if !fits {
		return refuse(CodeTooLarge, "the state directory takes %d bytes on disk, and the upload with sequence %d would take it past this receiver's cap of %d", held, m.Sequence, s.MaxStorage)
	}
	return nil
}

// checkUsage refuses as too-large once the state directory takes more
// than MaxStorage on disk. It stops measuring once ctx is done.
func (s *Store) checkUsage(ctx context.Context) error {
	if s.MaxStorage <= 0 {
		return nil
	}
	n, err := usage(ctx, s.dir, nil)
	if err != nil {
		return err
	}
	if n > s.MaxStorage {
		return refuse(CodeTooLarge, "the state directory takes %d bytes on disk, past this receiver's cap of %d", n, s.MaxStorage)
	}
	return nil
}

// usage is what the tree at root takes on disk. Each file and directory
// in it counts its size or the blocks it holds, whichever is more, and a
// symlink is not followed. An entry deleted while usage walks is not
// counted, and neither is a path in skip, nor anything under it. usage
// stops with ctx's error once ctx is done.
func usage(ctx context.Context, root string, skip map[string]bool) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("measuring %s: %w", root, err)
		}
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if skip[path] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		n := info.Size()
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			n = max(n, st.Blocks*512)
		}
		total += n
		return nil
	})
	return total, err
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

// readable makes the tree at root readable by every user who can reach
// it, whatever the umask it was written under: each directory 0755, and
// each file 0644, or 0444 when its owner may not write it, as git's
// objects are. A symlink is left as it is.
func readable(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			return os.Chmod(path, 0o755)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.Chmod(path, info.Mode().Perm()&0o200|0o444)
		}
		return nil
	})
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
	removeAll = removeTree
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
