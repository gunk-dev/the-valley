package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"the-valley/flow/internal/location"
)

// HomeDirStore implements StateStore using a session's directory, under
// either sessions root (location's SessionRoots). Everything of a session
// stays in its directory.
type HomeDirStore struct {
	baseDir string // the session's directory
}

// OpenSession returns the store of the existing session id, under either
// sessions root. An ID no root has is an error: looking a session up never
// creates one.
func OpenSession(id string) (*HomeDirStore, error) {
	l, err := location.Host()
	if err != nil {
		return nil, err
	}
	dir, err := l.Session(id)
	if err != nil {
		return nil, err
	}
	return &HomeDirStore{baseDir: dir}, nil
}

// CreateSession returns the store of the new session id, in the directory
// location allocates for it, which it creates.
func CreateSession(id string) (*HomeDirStore, error) {
	l, err := location.Host()
	if err != nil {
		return nil, err
	}
	return CreateSessionIn(l, id)
}

// CreateSessionIn is CreateSession in the layout l, whose generation, when
// set, decides the directory.
func CreateSessionIn(l location.Layout, id string) (*HomeDirStore, error) {
	dir, err := l.NewSession(id)
	if err != nil {
		return nil, err
	}
	s := &HomeDirStore{baseDir: dir}
	if err := s.EnsureDirs(); err != nil {
		return nil, err
	}
	return s, nil
}

// Sessions lists every session directory under either root, by ID, with a
// location.ConflictError for each ID both roots have.
func Sessions() ([]location.Session, error) {
	l, err := location.Host()
	if err != nil {
		return nil, err
	}
	return l.Sessions()
}

// NewHomeDirStoreFromPath creates a HomeDirStore with an explicit base directory.
// Useful for testing.
func NewHomeDirStoreFromPath(baseDir string) *HomeDirStore {
	return &HomeDirStore{baseDir: baseDir}
}

func (s *HomeDirStore) StateDir() string {
	return filepath.Join(s.baseDir, "runs")
}

func (s *HomeDirStore) LogDir() string {
	return filepath.Join(s.baseDir, "logs")
}

func (s *HomeDirStore) BaseDir() string {
	return s.baseDir
}

func (s *HomeDirStore) EnsureDirs() error {
	if err := os.MkdirAll(s.StateDir(), 0o755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	if err := os.MkdirAll(s.LogDir(), 0o755); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}
	return nil
}

func (s *HomeDirStore) Save(st *State) error {
	return saveState(s.StateDir(), st)
}

func (s *HomeDirStore) Load(id string) (*State, error) {
	return loadState(s.StateDir(), id)
}

func (s *HomeDirStore) List() ([]*State, error) {
	return listStates(s.StateDir())
}

// Scan lists the run states as List does, and names the records List
// skips because they don't parse, as one caught while it is rewritten.
func (s *HomeDirStore) Scan() ([]*State, []string, error) {
	states, unparsed, _, err := scanStates(s.StateDir())
	return states, unparsed, err
}

// Audit is Scan for a reader that must account for every run: it also
// returns the read errors of the records Scan skips because they can't be
// read.
func (s *HomeDirStore) Audit() (states []*State, unparsed []string, unreadable []error, err error) {
	return scanStates(s.StateDir())
}

func (s *HomeDirStore) Delete(id string) error {
	return deleteState(s.StateDir(), id)
}

// ListAllSessions returns the run states of every session, under either
// root. A session ID both roots have is left out, and its error returned
// with the rest.
func ListAllSessions() ([]*State, error) {
	sessions, err := Sessions()
	var all []*State
	for _, sess := range sessions {
		states, listErr := NewHomeDirStoreFromPath(sess.Dir).List()
		if listErr != nil {
			continue
		}
		all = append(all, states...)
	}
	return all, err
}

// ErrNoSessions is the absence of any session, under either root.
var ErrNoSessions = errors.New("no sessions found")

// MostRecentSession returns the ID of the most recent session, under
// either root. Session IDs embed timestamps (session-YYYYMMDD-HHMM-XXXX),
// so lexicographic order is chronological. With none it returns
// ErrNoSessions; any other error, such as a session ID both roots have, is
// one it could not look past.
func MostRecentSession() (string, error) {
	sessions, err := Sessions()
	if err != nil {
		return "", err
	}
	var latest string
	for _, sess := range sessions {
		if strings.HasPrefix(sess.ID, "session-") && len(sess.ID) > len("session-") && sess.ID > latest {
			latest = sess.ID
		}
	}
	if latest == "" {
		return "", ErrNoSessions
	}
	return latest, nil
}
