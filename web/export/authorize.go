package export

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Holdings are the active export, by its sequence and its digest, or nil
// for none, and the source tip of each of its projects whose repository
// the active generation still holds.
func (s *Store) Holdings() (*Active, map[string]string, error) {
	held := map[string]string{}
	g, release, err := s.Open()
	defer release()
	switch {
	case errors.Is(err, ErrNoExport):
		return nil, held, nil
	case err != nil:
		return nil, nil, err
	}
	e, err := g.Export()
	if err != nil {
		return nil, nil, err
	}
	digest, err := e.Digest()
	if err != nil {
		return nil, nil, err
	}
	for _, p := range e.Projects {
		if repo, _ := g.Repo(p.Name); exists(repo) {
			held[p.Name] = p.SourceTip
		}
	}
	return &Active{Sequence: g.Sequence, Digest: digest}, held, nil
}

// named reports, by the stored authorization, whether a project and
// identity are named, with any reader. With no stored authorization it
// names nothing.
func (s *Store) named() (func(name, identity string) bool, error) {
	a, _, err := s.Authorization()
	if errors.Is(err, ErrNoAuthorization) {
		return func(string, string) bool { return false }, nil
	}
	if err != nil {
		return nil, err
	}
	return a.Names, nil
}

// ReadAuthorization is the stored authorization, as the signed note the
// receiver admitted, read whole and at most MaxAuthorization bytes. Its
// error is ErrNoAuthorization before the first is stored.
func (s *Store) ReadAuthorization() ([]byte, error) {
	f, err := os.Open(s.path("authorization"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoAuthorization
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxAuthorization+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxAuthorization {
		return nil, fmt.Errorf("%s is larger than %d bytes", s.path("authorization"), MaxAuthorization)
	}
	return data, nil
}

// Authorization is the stored authorization, read and parsed, with the
// signed note it was read from.
func (s *Store) Authorization() (*Authorization, []byte, error) {
	signed, err := s.ReadAuthorization()
	if err != nil {
		return nil, nil, err
	}
	a, err := ReadStoredAuthorization(signed)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", s.path("authorization"), err)
	}
	return a, signed, nil
}

// AuthorizationHighWater is the stored authorization's sequence, 0 for
// none. A stored authorization that cannot be read is an error, never 0,
// so no replay is admitted over it.
func (s *Store) AuthorizationHighWater() (int64, error) {
	a, _, err := s.Authorization()
	if errors.Is(err, ErrNoAuthorization) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return a.Sequence, nil
}

// Authorize stores an authorization VerifyAuthorization admitted, whose
// signed note is signed. It takes the authorization lock, waiting for it
// until ctx is done, and never the activation lock, so a stalled upload,
// commit or repository check does not hold it up. Under the lock it
// refuses a sequence not greater than the stored one's as replayed, and a
// basis other than the stored one's sequence as stale-basis, and then
// replaces the stored authorization durably. Once Authorize returns
// nil, every response a server starts writing is held to it: the rename
// happens under the serving lock, exclusively.
//
// Authorize deletes nothing. Prune deletes the repositories it withdrew.
func (s *Store) Authorize(ctx context.Context, a *Authorization, signed []byte) error {
	if s.readOnly {
		return errReadOnly
	}
	unlock, err := s.lockAuthorization(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	hw, err := s.AuthorizationHighWater()
	if err != nil {
		return err
	}
	if err := a.admissible(hw); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeDurablyHeld(s.path("authorization"), signed, func() (func(), error) {
		return s.holdServing(ctx, syscall.LOCK_EX)
	})
}

// servingLock is the file a server and Authorize coordinate through. A
// server holds it shared from its last read of the authorization until it
// has written a response's headers. Authorize holds it exclusively while
// it renames the new authorization into place, and returns only after it
// releases it. So a response whose headers are written after Authorize
// returns was checked against the new authorization.
const servingLock = "serving.lock"

// HoldServing takes the serving lock shared, for a server's last read of
// the authorization and the response it then starts. Authorize holds it
// exclusively for a rename alone, so the wait is short. A store opened
// read-only takes it too: the lock file is opened read-only.
func (s *Store) HoldServing() (release func(), err error) {
	return s.holdServing(context.Background(), syscall.LOCK_SH)
}

// holdServing takes the serving lock as how says, waiting for it until
// ctx is done.
func (s *Store) holdServing(ctx context.Context, how int) (func(), error) {
	flags := os.O_RDONLY
	if how == syscall.LOCK_EX {
		flags = os.O_RDWR
	}
	f, err := os.OpenFile(s.path(servingLock), flags, 0)
	if err != nil {
		return nil, err
	}
	for pause := time.Millisecond; ; pause = min(2*pause, 50*time.Millisecond) {
		err := flock(f, how|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, err
		}
		select {
		case <-time.After(pause):
		case <-ctx.Done():
			f.Close()
			return nil, fmt.Errorf("waiting for %s: %w", s.path(servingLock), ctx.Err())
		}
	}
	var once sync.Once
	return func() { once.Do(func() { f.Close() }) }, nil
}

// touch makes the file at path if it does not exist, and gives it mode
// 0644 whatever the umask.
func touch(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Chmod(0o644)
}

// Missing are the projects of the active export that the stored
// authorization names and whose repositories the active generation no
// longer holds, in the export's order.
func (s *Store) Missing() ([]string, error) {
	named, err := s.named()
	if err != nil {
		return nil, err
	}
	missing := []string{}
	g, release, err := s.Open()
	defer release()
	if errors.Is(err, ErrNoExport) {
		return missing, nil
	}
	if err != nil {
		return nil, err
	}
	e, err := g.Export()
	if err != nil {
		return nil, err
	}
	for _, p := range e.Projects {
		if repo, _ := g.Repo(p.Name); named(p.Name, p.Identity) && !exists(repo) {
			missing = append(missing, p.Name)
		}
	}
	return missing, nil
}

// Prune deletes from the active generation the repository of each project
// the stored authorization does not name, once no reader holds the
// generation. It takes the activation lock, waiting for it until ctx is
// done. While a reader holds the generation it deletes nothing and returns
// ErrHeld, and the caller tries again later. With no stored authorization
// it deletes nothing: a server serves nothing without one, and a later
// authorization may name what the generation holds. export.json stays as
// it was signed, so it keeps the withdrawn projects' plan text until a
// later activation replaces the generation.
func (s *Store) Prune(ctx context.Context) error {
	if s.readOnly {
		return errReadOnly
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return s.prune(ctx)
}

// prune is Prune, with the activation lock held.
func (s *Store) prune(ctx context.Context) error {
	a, _, err := s.Authorization()
	if errors.Is(err, ErrNoAuthorization) {
		return nil
	}
	if err != nil {
		return err
	}
	seq, ok, err := s.active()
	if err != nil || !ok {
		return err
	}
	g := Generation{Sequence: seq, Dir: s.generationDir(seq)}
	e, err := g.Export()
	if err != nil {
		return err
	}
	var withdrawn []string
	for _, p := range e.Projects {
		if repo, _ := g.Repo(p.Name); !a.Names(p.Name, p.Identity) && exists(repo) {
			withdrawn = append(withdrawn, repo)
		}
	}
	if len(withdrawn) == 0 {
		return nil
	}
	f, err := os.Open(g.Dir)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := flock(f, syscall.LOCK_EX|syscall.LOCK_NB); errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrHeld
	} else if err != nil {
		return err
	}
	// Holding the generation exclusively, no reader is in it, and one
	// that opens it next finds the repositories gone.
	var trash []string
	for _, repo := range withdrawn {
		to := s.path("trash", "withdrawn-"+filepath.Base(repo)+"-"+random())
		if err := rename(repo, to); err != nil {
			return err
		}
		trash = append(trash, to)
	}
	err = syncPath(filepath.Join(g.Dir, "repos"))
	f.Close()
	for _, t := range trash {
		err = errors.Join(err, removeAll(ctx, t))
	}
	return err
}
