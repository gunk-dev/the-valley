package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Shared helpers used by both GitDirStore and HomeDirStore.

func saveState(dir string, st *State) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}
	path := filepath.Join(dir, st.ID+".json")
	return os.WriteFile(path, data, 0o644)
}

func loadState(dir string, id string) (*State, error) {
	path := filepath.Join(dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading state file: %w", err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parsing state file: %w", err)
	}
	return &st, nil
}

func listStates(dir string) ([]*State, error) {
	states, _, _, err := scanStates(dir)
	return states, err
}

// scanStates lists the run states in dir, newest first, names the records
// it skipped because they don't parse, and returns the read errors of
// those it could not read. saveState rewrites a record in place, so a
// reader can catch one half written; a record removed since the listing is
// none of these.
func scanStates(dir string) ([]*State, []string, []error, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, fmt.Errorf("reading state dir: %w", err)
	}

	var states []*State
	var unparsed []string
	var unreadable []error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			if !os.IsNotExist(err) {
				unreadable = append(unreadable, err)
			}
			continue
		}
		var st State
		if err := json.Unmarshal(data, &st); err != nil {
			unparsed = append(unparsed, e.Name())
			continue
		}
		states = append(states, &st)
	}

	sort.Slice(states, func(i, j int) bool {
		return states[i].CreatedAt > states[j].CreatedAt
	})

	return states, unparsed, unreadable, nil
}

func deleteState(dir string, id string) error {
	path := filepath.Join(dir, id+".json")
	return os.Remove(path)
}

// StateStore defines the interface for persisting run state.
type StateStore interface {
	Save(s *State) error
	Load(id string) (*State, error)
	List() ([]*State, error)
	Delete(id string) error
	LogDir() string
	StateDir() string
	EnsureDirs() error
}

// PromptDir holds the prompt files workers read on stdin, and the settings
// file a Claude coordinator loads with --settings, beside the store's logs/.
// The prompts are kept after the run as the audit copy of each brief.
func PromptDir(s StateStore) string {
	return filepath.Join(filepath.Dir(s.LogDir()), "prompts")
}

// WritePromptFile writes text owner-only; briefs can quote private context.
func WritePromptFile(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating prompt dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return fmt.Errorf("writing prompt file: %w", err)
	}
	return nil
}

// GitDirStore implements StateStore in a clone's standalone run store
// (location's StandaloneStore), for runs started outside a session.
type GitDirStore struct {
	dir string
}

// NewGitDirStore creates a GitDirStore keeping runs/ and logs/ in dir.
func NewGitDirStore(dir string) *GitDirStore {
	return &GitDirStore{dir: dir}
}

func (s *GitDirStore) StateDir() string {
	return filepath.Join(s.dir, "runs")
}

func (s *GitDirStore) LogDir() string {
	return filepath.Join(s.dir, "logs")
}

func (s *GitDirStore) EnsureDirs() error {
	if err := os.MkdirAll(s.StateDir(), 0o755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	if err := os.MkdirAll(s.LogDir(), 0o755); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}
	return nil
}

func (s *GitDirStore) Save(st *State) error {
	return saveState(s.StateDir(), st)
}

func (s *GitDirStore) Load(id string) (*State, error) {
	return loadState(s.StateDir(), id)
}

func (s *GitDirStore) List() ([]*State, error) {
	return listStates(s.StateDir())
}

func (s *GitDirStore) Delete(id string) error {
	return deleteState(s.StateDir(), id)
}
