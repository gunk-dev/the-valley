// Package readerstate is valley-web's browser state: the read links
// readers have redeemed at this edge, and the session each one holds.
//
// # The state
//
// valley-web keeps the state in a directory of its own, browser/ beside
// the receiver's store descriptor. The deployment makes that directory,
// owned by valley-web's user and mode 0700. Neither valley-web nor the
// receiver makes it or changes its owner. Its files are:
//
//	owner.lock   held by the one valley-web that owns the directory
//	lock         held for each transaction
//	state        the state document
//	state.new    the next state document, while a transaction writes it
//
// Every file is mode 0600. A symbolic link, or any file that is not a
// regular file, is refused where a file is expected.
//
// The state document records every link that has been redeemed, once,
// for good. Each record keeps the binding the link had when it was
// redeemed: its credential ID, its principal and its verifier. It also
// keeps the SHA-256 of the session token the redemption gave out, until
// the reader signs out. Signing out removes the hash and keeps the
// record, so a link is never redeemed twice. Its text is a type line,
// then one line per record, in ID order, with "-" for no session:
//
//	the-valley/web-reader-state/v1
//	link lk-0123456789abcdef alice <verifier> <SHA-256 of the session token>
//
// The document is at most MaxState bytes. A redemption that would pass
// that is refused, and nothing is consumed. Signing out never makes the
// document larger. No record is ever removed to make room.
//
// # One owner, one snapshot
//
// One process owns the directory: Open takes owner.lock and keeps it
// until the process ends, and a second Open of the same directory fails.
// The owner holds the decoded state in memory. Reads take a read lock on
// it and touch no file but the state's directory entry. A mutation takes
// the write lock, then the transaction lock, then the receiver's serving
// lock shared, and reads the stored authorization again under it. It
// writes the whole next document to state.new, syncs it, renames it over
// state, and syncs the directory. Only then does it replace the snapshot
// in memory.
//
// Every wait for a lock ends when the request's context is done. A
// mutation checks its context again once it holds each lock, and once
// more after it encodes the next document, just before it creates
// state.new. One whose context is done at any of these checks stops
// there, before it changes anything. So a request whose deadline passed
// while it waited consumes no link and ends no session. A mutation that
// has started writing finishes, whatever its context.
//
// A transaction that fails while it writes, syncs or renames leaves the
// state unavailable until the process restarts. So does a state file
// that is missing, replaced or changed under the process. The process
// never goes back to a snapshot it held before: an unavailable state
// serves no session and takes no redemption, and anonymous readers are
// served as ever.
//
// # Starting empty
//
// An edge whose state file does not exist yet starts with no state. The
// first state is created only under a stored authorization that was read
// and parsed, and that carries no binding (Initialize). A missing or
// unreadable authorization is not one with no bindings. So a link the
// host issued before the edge's state was created is never redeemed: the
// host revokes every link, delivers an authorization with no binding, and
// issues new links once the state is created.
package readerstate

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"the-valley/web/export"
)

// Type is the first line of every state document.
const Type = "the-valley/web-reader-state/v1"

// MaxState is the most bytes a state document takes.
const MaxState = 32 << 20

// The files of a browser-state directory.
const (
	stateFile = "state"
	nextFile  = "state.new"
	lockFile  = "lock"
	ownerFile = "owner.lock"
)

// The errors a transaction or a read fails with.
var (
	// ErrUnavailable is the error of every read and every transaction
	// while the state is not usable: before it is created, and after a
	// failure made it unavailable until the process restarts.
	ErrUnavailable = errors.New("the browser state is unavailable")
	// ErrRefused is Redeem's error for every credential it does not take:
	// one that is malformed, unknown, revoked, redeemed already, or whose
	// secret does not match its verifier. Its cause is never told apart.
	ErrRefused = errors.New("this link cannot be used")
	// ErrSignedIn is Redeem's error when the request carries a valid
	// session already.
	ErrSignedIn = errors.New("a reader is signed in already")
	// ErrFull is Redeem's error when the state has no room for one more
	// record.
	ErrFull = errors.New("the browser state is full")
)

// Op names a kind of transaction.
type Op int

// The kinds of transaction.
const (
	Initializing Op = iota
	Redeeming
	SigningOut
)

func (o Op) String() string {
	return [...]string{"initialize", "redeem", "logout"}[o]
}

// Step names a point in a transaction, from the encoding of the next
// document through its durable write, each reached once the work it names
// is done.
type Step int

// The steps of a transaction, in order.
const (
	// Encoded: the next document is encoded, and state.new is not made.
	// The transaction checks its context after it.
	Encoded Step = iota
	// Created: state.new is made, and nothing is written to it.
	Created
	// Written: the next document is written to state.new.
	Written
	// Synced: state.new is synced.
	Synced
	// Renamed: state.new is renamed over state.
	Renamed
	// Committed: the directory is synced, so the transaction is durable.
	Committed
	// Published: the new snapshot is the one reads see.
	Published
)

// Steps names each step as Step.String does.
var Steps = []Step{Encoded, Created, Written, Synced, Renamed, Committed, Published}

func (s Step) String() string {
	return [...]string{"encoded", "created", "written", "synced", "renamed", "committed", "published"}[s]
}

// Options are what Open opens a state with. The zero value is what
// valley-web uses.
type Options struct {
	// Max is the most bytes the state document takes, or 0 for MaxState.
	Max int
	// Fault, when set, is called at each step of each transaction. An
	// error it returns fails the transaction at that step, as a failed
	// write or sync would, and leaves the state unavailable. Tests set it.
	Fault func(Op, Step) error
}

// State is one browser-state directory, owned by this process.
type State struct {
	dir   string
	max   int
	fault func(Op, Step) error
	owner *os.File

	// mu is the read and write lock on the snapshot. snap is nil while
	// the state does not exist. file is the state file as this process
	// last wrote or read it, and nil while it does not exist.
	mu   rwLock
	snap *snapshot
	file fs.FileInfo

	// broken is why the state is unavailable until the process restarts,
	// or nil. It is set under either lock on mu, so it has a lock of its
	// own.
	brokenMu sync.Mutex
	broken   error
}

// Open opens the browser-state directory dir and takes its owner lock for
// the life of the process. It refuses a directory that is a symbolic
// link, that is not owned by the process's user, or that other users may
// reach, and one another process owns. It then reads the state file, if
// there is one, and syncs it and the directory before it serves anything
// from it. A state file that cannot be read or does not parse leaves the
// state unavailable: Open still succeeds, and Err says why.
func Open(dir string, o Options) (*State, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	switch {
	case !info.IsDir():
		return nil, fmt.Errorf("%s is not a directory", abs)
	case owner(info) != os.Geteuid():
		return nil, fmt.Errorf("%s is not owned by this process's user", abs)
	case info.Mode().Perm()&0o077 != 0:
		return nil, fmt.Errorf("%s is mode %04o; the browser state's directory is its owner's alone, mode 0700", abs, info.Mode().Perm())
	}
	f, err := openPrivate(filepath.Join(abs, ownerFile), os.O_RDWR|os.O_CREATE)
	if err != nil {
		return nil, err
	}
	if err := flock(f, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another process owns the browser state in %s", abs)
		}
		return nil, err
	}
	s := &State{dir: abs, max: o.Max, fault: o.Fault, owner: f}
	if s.max <= 0 {
		s.max = MaxState
	}
	if err := s.load(); err != nil {
		s.fail(err)
	}
	return s, nil
}

// Close gives back the owner lock. The state is not used after.
func (s *State) Close() error { return s.owner.Close() }

// Err is why the state is unavailable until the process restarts, or nil.
func (s *State) Err() error {
	s.brokenMu.Lock()
	defer s.brokenMu.Unlock()
	return s.broken
}

// fail makes the state unavailable until the process restarts, for err,
// unless it is so already.
func (s *State) fail(err error) {
	s.brokenMu.Lock()
	defer s.brokenMu.Unlock()
	if s.broken == nil {
		s.broken = err
	}
}

// Pending reports whether the state does not exist yet and may still be
// created: Initialize may create it. It takes the read lock, waiting for
// it until ctx is done, and fails then with ctx's error.
func (s *State) Pending(ctx context.Context) (bool, error) {
	if err := s.mu.rlock(ctx); err != nil {
		return false, fmt.Errorf("waiting to read the browser state: %w", err)
	}
	defer s.mu.runlock()
	return s.snap == nil && s.Err() == nil, nil
}

// load reads the state file into the snapshot, and makes it durable
// before the snapshot is used: a document a crash left in the page cache
// alone is synced before anything is served from it. It deletes a
// state.new a crash left behind, which it never reads. A state file that
// does not exist leaves no snapshot.
func (s *State) load() error {
	if err := os.Remove(s.path(nextFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := openPrivate(s.path(stateFile), os.O_RDONLY)
	if errors.Is(err, fs.ErrNotExist) {
		return syncDir(s.dir)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(s.max)+1))
	if err != nil {
		return err
	}
	if len(data) > s.max {
		return fmt.Errorf("%s is larger than %d bytes", s.path(stateFile), s.max)
	}
	snap, err := parse(data)
	if err != nil {
		return fmt.Errorf("%s: %w", s.path(stateFile), err)
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := syncDir(s.dir); err != nil {
		return err
	}
	info, err := os.Lstat(s.path(stateFile))
	if err != nil {
		return err
	}
	s.snap, s.file = snap, info
	return nil
}

// usable is nil when reads may use the snapshot, and ErrUnavailable,
// wrapped with why, when they may not. It is called with either lock on
// mu held. A state file that is no longer the one this process last wrote
// or read, or that appeared while it had none, makes the state
// unavailable.
func (s *State) usable() error {
	if err := s.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if err := s.unchanged(); err != nil {
		s.fail(err)
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if s.snap == nil {
		return fmt.Errorf("%w: it has not been created", ErrUnavailable)
	}
	return nil
}

// unchanged is nil when the state file is the one this process last
// wrote or read, or still does not exist when it had none.
func (s *State) unchanged() error {
	info, err := os.Lstat(s.path(stateFile))
	switch {
	case errors.Is(err, fs.ErrNotExist) && s.file == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%s was removed", s.path(stateFile))
	case err != nil:
		return err
	case s.file == nil:
		return fmt.Errorf("%s was made by another process", s.path(stateFile))
	case !os.SameFile(info, s.file) || info.Size() != s.file.Size() || !info.ModTime().Equal(s.file.ModTime()) || info.Mode() != s.file.Mode():
		return fmt.Errorf("%s was replaced or changed by another process", s.path(stateFile))
	}
	return nil
}

// Reading is the state held under its read lock, from Read until Done.
type Reading struct{ s *State }

// Read takes the state's read lock, waiting for it until ctx is done.
// Done gives it back.
func (s *State) Read(ctx context.Context) (Reading, error) {
	if err := s.mu.rlock(ctx); err != nil {
		return Reading{}, fmt.Errorf("waiting to read the browser state: %w", err)
	}
	return Reading{s}, nil
}

// Done gives back the read lock Read took.
func (r Reading) Done() { r.s.mu.runlock() }

// Session is the principal whose valid session token is, under the
// authorization a: a token whose hash the state holds for its link, while
// a binds that link to the principal and the verifier it had when it was
// redeemed. It is "" for a token that names no valid session: malformed,
// unknown, signed out, or of a link a no longer binds as it was. Its
// error is ErrUnavailable, wrapped, when the state is not usable.
func (r Reading) Session(token string, a *export.Authorization) (string, error) {
	if err := r.s.usable(); err != nil {
		return "", err
	}
	return r.s.snap.session(token, a), nil
}

// session is the principal of token's valid session under a, or "".
func (snap *snapshot) session(token string, a *export.Authorization) string {
	id, ok := tokenID(token)
	if !ok {
		return ""
	}
	rec, ok := snap.records[id]
	if !ok || rec.session == "" || !hashMatches(token, rec.session) {
		return ""
	}
	b, ok := binding(a, id)
	if !ok || b.Principal != rec.principal || b.Verifier != rec.verifier {
		return ""
	}
	return rec.principal
}

// Hold takes the receiver's serving lock shared, waiting for it until ctx
// is done, and reads the stored authorization under it. a is nil when no
// valid authorization is stored. release gives the lock back.
type Hold func(ctx context.Context) (a *export.Authorization, release func(), err error)

// Initialize creates the empty state, when there is none yet, under an
// authorization hold reads that carries no binding. It reports whether it
// created it. It takes the locks a mutation takes and holds the serving
// lock until the state is durable, so no authorization that carries a
// binding is stored between its check and its commit. Under an
// authorization that carries a binding, or none, it creates nothing.
func (s *State) Initialize(ctx context.Context, hold Hold) (bool, error) {
	created := false
	err := s.transact(ctx, hold, Initializing, func(a *export.Authorization, snap *snapshot) (*snapshot, error) {
		if snap != nil || a == nil || len(a.Bindings) > 0 {
			return nil, nil
		}
		created = true
		return &snapshot{records: map[string]record{}}, nil
	})
	return created && err == nil, err
}

// Redeem consumes the read link credential, "<credential-id>.<secret>",
// and starts a session for its principal, under the authorization hold
// reads. It returns the session token, which the state keeps only as a
// hash. current is the session token the request carries, or "". A
// current token that names a valid session fails with ErrSignedIn, before
// anything is consumed. A link the authorization does not bind, whose
// secret's SHA-256 is not its verifier, or that was redeemed already,
// fails with ErrRefused. The link is consumed and its session starts in
// one transaction, so of two redemptions of one link one alone succeeds.
func (s *State) Redeem(ctx context.Context, hold Hold, credential, current string) (string, error) {
	var token string
	err := s.transact(ctx, hold, Redeeming, func(a *export.Authorization, snap *snapshot) (*snapshot, error) {
		if snap == nil {
			return nil, fmt.Errorf("%w: it has not been created", ErrUnavailable)
		}
		if current != "" && snap.session(current, a) != "" {
			return nil, ErrSignedIn
		}
		id, secret, ok := parseCredential(credential)
		if !ok {
			return nil, ErrRefused
		}
		b, bound := binding(a, id)
		sum := sha256.Sum256(secret)
		matches := subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(b.Verifier)) == 1
		if _, used := snap.records[id]; !bound || !matches || used {
			return nil, ErrRefused
		}
		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, err
		}
		t := id + "." + base64.RawURLEncoding.EncodeToString(random[:])
		next := snap.with(record{id: id, principal: b.Principal, verifier: b.Verifier, session: hashOf(t)})
		if next.size > s.max {
			return nil, ErrFull
		}
		token = t
		return next, nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// Logout ends the session token names: it removes the token's hash from
// its link's record, and keeps the record, so the link stays consumed. A
// token that names no session the state holds changes nothing, and
// Logout succeeds. It takes the locks every mutation takes.
func (s *State) Logout(ctx context.Context, hold Hold, token string) error {
	return s.transact(ctx, hold, SigningOut, func(_ *export.Authorization, snap *snapshot) (*snapshot, error) {
		if snap == nil {
			return nil, fmt.Errorf("%w: it has not been created", ErrUnavailable)
		}
		id, ok := tokenID(token)
		if !ok {
			return nil, nil
		}
		rec, ok := snap.records[id]
		if !ok || rec.session == "" || !hashMatches(token, rec.session) {
			return nil, nil
		}
		rec.session = ""
		return snap.with(rec), nil
	})
}

// transact runs one transaction: it takes the write lock, the transaction
// lock and the serving lock, in that order, reads the authorization under
// the last, and gives change the authorization and the snapshot. change
// returns the next snapshot, or nil for no change. A next snapshot is
// encoded, written durably and then published. The locks are given back
// in the reverse order, the serving lock once the next state is
// published. Each wait ends when ctx is done. Once it holds each lock, and
// once it has encoded the next document, it stops, with nothing changed,
// when ctx is done.
func (s *State) transact(ctx context.Context, hold Hold, op Op, change func(*export.Authorization, *snapshot) (*snapshot, error)) error {
	if err := s.mu.lock(ctx); err != nil {
		return fmt.Errorf("waiting to change the browser state: %w", err)
	}
	defer s.mu.unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	a, release, err := hold(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.unchanged(); err != nil {
		s.fail(err)
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	next, err := change(a, s.snap)
	if err != nil || next == nil {
		return err
	}
	data := next.encode()
	err = s.step(op, Encoded)
	if err == nil {
		// The last check, before state.new is made. From here on the
		// transaction finishes, whatever its context.
		if err := ctx.Err(); err != nil {
			return err
		}
		err = s.commit(op, next, data)
	}
	if err != nil {
		s.fail(fmt.Errorf("a transaction to %s failed, so its outcome is uncertain: %w", op, err))
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}

// step calls the fault hook, when there is one, at step st of a
// transaction to op.
func (s *State) step(op Op, st Step) error {
	if s.fault != nil {
		return s.fault(op, st)
	}
	return nil
}

// commit writes data, next's document, durably and publishes next,
// calling the fault hook at each step from Created on. Its caller holds
// the write lock and the transaction lock.
func (s *State) commit(op Op, next *snapshot, data []byte) error {
	step := func(st Step) error { return s.step(op, st) }
	tmp := s.path(nextFile)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	err = f.Chmod(0o600)
	if err == nil {
		err = step(Created)
	}
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = step(Written)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = step(Synced)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, s.path(stateFile))
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err := step(Renamed); err != nil {
		return err
	}
	if err := syncDir(s.dir); err != nil {
		return err
	}
	if err := step(Committed); err != nil {
		return err
	}
	info, err := os.Lstat(s.path(stateFile))
	if err != nil {
		return err
	}
	s.snap, s.file = next, info
	return step(Published)
}

// lock takes the transaction lock, waiting for it until ctx is done.
func (s *State) lock(ctx context.Context) (func(), error) {
	f, err := openPrivate(s.path(lockFile), os.O_RDWR|os.O_CREATE)
	if err != nil {
		return nil, err
	}
	for pause := time.Millisecond; ; pause = min(2*pause, 50*time.Millisecond) {
		err := flock(f, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, err
		}
		select {
		case <-time.After(pause):
		case <-ctx.Done():
			f.Close()
			return nil, fmt.Errorf("waiting for %s: %w", s.path(lockFile), ctx.Err())
		}
	}
}

func (s *State) path(name string) string { return filepath.Join(s.dir, name) }

// rwLock is the snapshot's read and write lock. Unlike sync.RWMutex, a
// wait for it ends when its context is done. A writer that waits for it
// holds back the readers that come after, so readers never keep a
// transaction waiting. Its zero value is unlocked.
type rwLock struct {
	mu      sync.Mutex
	readers int  // the readers that hold it
	writer  bool // whether a writer holds it
	waiting int  // the writers that wait for it
	// changed is closed, and then made again, each time the lock is given
	// back or a writer stops waiting, so every waiter looks again.
	changed chan struct{}
}

// lock takes l to write, waiting until ctx is done. It may take l when
// ctx is done already: its caller checks ctx once it holds l.
func (l *rwLock) lock(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.waiting++
	defer func() { l.waiting-- }()
	for l.writer || l.readers > 0 {
		if err := l.wait(ctx); err != nil {
			l.signal()
			return err
		}
	}
	l.writer = true
	return nil
}

func (l *rwLock) unlock() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writer = false
	l.signal()
}

// rlock takes l to read, waiting until ctx is done, while a writer holds
// it or waits for it.
func (l *rwLock) rlock(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for l.writer || l.waiting > 0 {
		if err := l.wait(ctx); err != nil {
			return err
		}
	}
	l.readers++
	return nil
}

func (l *rwLock) runlock() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.readers--; l.readers == 0 {
		l.signal()
	}
}

// wait gives back l.mu until the lock changes or ctx is done, and then
// takes it again.
func (l *rwLock) wait(ctx context.Context) error {
	if l.changed == nil {
		l.changed = make(chan struct{})
	}
	changed := l.changed
	l.mu.Unlock()
	defer l.mu.Lock()
	select {
	case <-changed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// signal wakes every waiter. It is called with l.mu held.
func (l *rwLock) signal() {
	if l.changed != nil {
		close(l.changed)
		l.changed = nil
	}
}

// record is one redeemed link: its binding as it was redeemed, and the
// hex SHA-256 of its session's token, or "" once its reader signed out.
type record struct {
	id, principal, verifier, session string
}

// snapshot is one state document, decoded, and its encoded size. A
// snapshot never changes once it is published: a transaction makes a new
// one.
type snapshot struct {
	records map[string]record
	size    int
}

// with is a copy of snap with rec in place of the record of its ID.
func (snap *snapshot) with(rec record) *snapshot {
	next := &snapshot{records: make(map[string]record, len(snap.records)+1)}
	for id, r := range snap.records {
		next.records[id] = r
	}
	next.records[rec.id] = rec
	next.size = len(next.encode())
	return next
}

// encode is snap's document: the type line, then each record in ID order.
func (snap *snapshot) encode() []byte {
	ids := make([]string, 0, len(snap.records))
	for id := range snap.records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b bytes.Buffer
	b.WriteString(Type + "\n")
	for _, id := range ids {
		r := snap.records[id]
		session := r.session
		if session == "" {
			session = "-"
		}
		fmt.Fprintf(&b, "link %s %s %s %s\n", r.id, r.principal, r.verifier, session)
	}
	return b.Bytes()
}

// parse reads a state document: the type line, then records in strictly
// increasing ID order, each exactly a link record's five fields, and the
// document ends with a newline.
func parse(data []byte) (*snapshot, error) {
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return nil, errors.New("a browser-state document ends with a newline")
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if lines[0] != Type {
		return nil, fmt.Errorf("the document opens with %q, not %q", lines[0], Type)
	}
	snap := &snapshot{records: map[string]record{}, size: len(data)}
	last := ""
	for i, line := range lines[1:] {
		f := strings.Split(line, " ")
		if len(f) != 5 || f[0] != "link" {
			return nil, fmt.Errorf("line %d is not a link record", i+2)
		}
		r := record{id: f[1], principal: f[2], verifier: f[3], session: f[4]}
		if r.session == "-" {
			r.session = ""
		}
		switch {
		case !export.ValidLinkID(r.id) || r.id <= last:
			return nil, fmt.Errorf("line %d names no credential ID after the one before it", i+2)
		case !export.ValidName(r.principal) || r.principal == export.Anyone:
			return nil, fmt.Errorf("line %d names no principal", i+2)
		case !export.ValidVerifier(r.verifier):
			return nil, fmt.Errorf("line %d holds no verifier", i+2)
		case r.session != "" && !export.ValidVerifier(r.session):
			return nil, fmt.Errorf("line %d holds no session hash", i+2)
		}
		last = r.id
		snap.records[r.id] = r
	}
	return snap, nil
}

// binding is the binding a gives the credential id.
func binding(a *export.Authorization, id string) (export.Binding, bool) {
	if a == nil {
		return export.Binding{}, false
	}
	i := sort.Search(len(a.Bindings), func(i int) bool { return a.Bindings[i].ID >= id })
	if i < len(a.Bindings) && a.Bindings[i].ID == id {
		return a.Bindings[i], true
	}
	return export.Binding{}, false
}

// parseCredential splits a link's credential, "<credential-id>.<secret>",
// and decodes its secret: canonical unpadded base64url of 32 bytes.
func parseCredential(credential string) (id string, secret []byte, ok bool) {
	id, encoded, found := strings.Cut(credential, ".")
	if !found || !export.ValidLinkID(id) {
		return "", nil, false
	}
	secret, ok = random32(encoded)
	return id, secret, ok
}

// tokenID is the credential ID of a session token, "<credential-id>.<random>",
// whose random part is canonical unpadded base64url of 32 bytes.
func tokenID(token string) (string, bool) {
	id, _, ok := parseCredential(token)
	return id, ok
}

// random32 decodes s, canonical unpadded base64url of 32 bytes.
func random32(s string) ([]byte, bool) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil || len(b) != 32 || base64.RawURLEncoding.EncodeToString(b) != s {
		return nil, false
	}
	return b, true
}

// hashOf is the lowercase hex SHA-256 of a session token.
func hashOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// hashMatches compares a token's hash with a stored one in constant time.
func hashMatches(token, stored string) bool {
	return subtle.ConstantTimeCompare([]byte(hashOf(token)), []byte(stored)) == 1
}

// openPrivate opens the file at path, refusing a symbolic link and
// anything that is not a regular file. A file opened for writing is made
// mode 0600, and one opened for reading alone is refused when other users
// may read it. A file opened for reading is opened without blocking, so a
// FIFO put in its place cannot stall it.
func openPrivate(path string, flag int) (*os.File, error) {
	flag |= syscall.O_NOFOLLOW | syscall.O_CLOEXEC
	if flag&(os.O_WRONLY|os.O_RDWR) == 0 {
		flag |= syscall.O_NONBLOCK
	}
	f, err := os.OpenFile(path, flag, 0o600)
	if errors.Is(err, syscall.ELOOP) {
		return nil, fmt.Errorf("%s is a symbolic link", path)
	}
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	switch {
	case err != nil:
	case !info.Mode().IsRegular():
		err = fmt.Errorf("%s is not a regular file", path)
	case flag&os.O_RDWR != 0:
		err = f.Chmod(0o600)
	case info.Mode().Perm()&0o077 != 0:
		err = fmt.Errorf("%s is mode %04o; the browser state's files are their owner's alone, mode 0600", path, info.Mode().Perm())
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// owner is the user ID that owns the file info describes.
func owner(info fs.FileInfo) int {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}

// syncDir syncs the directory at path, so its entries are durable.
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
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
