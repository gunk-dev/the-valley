// Package location says where each kind of flow state is. It is the only
// code that spells the directories flow keeps state in, and the tmux
// sessions its agents run in.
//
// Flow's state has two sets of names: the legacy ones (~/.klaus,
// <project>/.klaus, $XDG_STATE_HOME/klaus, <git-common-dir>/klaus) and the
// flow ones (~/.valley/flow, <project>/.valley/flow,
// <git-common-dir>/valley/flow). Flow finds and operates state under
// either, by the rule each kind needs:
//
//   - a declaration (host config, project config, each prompt template,
//     principles) is the file under the flow names when one is there, else
//     the legacy file;
//   - a single-valued mutable file or directory (project registry,
//     self-update state, memory) is the one that exists, and having both is
//     an error;
//   - sessions are enumerated under both roots, and a session ID names one
//     directory;
//   - a lock that moves is held at both paths;
//   - new state goes where the host's naming_generation says.
//
// Nothing is moved, and every generation gives new state the legacy names:
// sessions outlive updates, records hold absolute paths, and an older build
// may still be running.
package location

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Generation is a value of the host-only naming_generation setting: which
// names new state gets.
type Generation string

const (
	// Legacy gives every new object the legacy names.
	Legacy Generation = "legacy"
	// Bridge is to give new home and project state the flow names
	// (~/.valley/flow, <project>/.valley/flow), keeping the legacy names
	// for the plumbing older builds share.
	Bridge Generation = "bridge"
	// Flow is to give every new object the flow names, the plumbing too.
	Flow Generation = "flow"
)

// Generations are the generations in the order a host advances through
// them.
var Generations = []Generation{Legacy, Bridge, Flow}

// ParseGeneration reads a naming_generation value; unset is Legacy.
func ParseGeneration(s string) (Generation, error) {
	switch g := Generation(s); g {
	case "":
		return Legacy, nil
	case Legacy, Bridge, Flow:
		return g, nil
	}
	return "", fmt.Errorf("naming_generation %q is none of legacy, bridge and flow", s)
}

// Next is the generation a host advances to from g, or "" from Flow.
func (g Generation) Next() Generation {
	for i, x := range Generations {
		if x == g && i+1 < len(Generations) {
			return Generations[i+1]
		}
	}
	return ""
}

// Allocates is the generation whose names this build gives new state when
// the host selects g: Legacy for every g. The writers that give new state
// the flow names are not built.
func Allocates(Generation) Generation { return Legacy }

// Operates lists the generations whose stores this build finds, reads and
// writes: all of them, whichever is selected.
func Operates() []Generation { return Generations }

// The names, legacy and flow, of each root.
var (
	legacyHome   = ".klaus"                         // under the home directory and a project
	flowHome     = filepath.Join(".valley", "flow") // under the home directory and a project
	legacyState  = "klaus"                          // under $XDG_STATE_HOME
	legacyGitDir = "klaus"                          // under a clone's git common directory
	flowGitDir   = filepath.Join("valley", "flow")  // under a clone's git common directory
	legacyAgents = "klaus-agents"                   // a detached tmux session of agent panes
)

// Layout is one user's state: where their home directory and state
// directory are, and which generation new state takes.
type Layout struct {
	Home      string // the user's home directory
	StateHome string // $XDG_STATE_HOME when it is absolute, else Home/.local/state
	// Gen is the generation new state takes. Empty reads naming_generation
	// from the host config when an allocation needs it.
	Gen Generation
}

// Host is the running user's layout.
func Host() (Layout, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, fmt.Errorf("resolving home dir: %w", err)
	}
	l := Layout{Home: home, StateHome: os.Getenv("XDG_STATE_HOME")}
	if !filepath.IsAbs(l.StateHome) {
		l.StateHome = filepath.Join(home, ".local", "state")
	}
	return l, nil
}

func (l Layout) legacy(name ...string) string {
	return filepath.Join(append([]string{l.Home, legacyHome}, name...)...)
}

func (l Layout) flow(name ...string) string {
	return filepath.Join(append([]string{l.Home, flowHome}, name...)...)
}

// Generation is the generation new state takes: l.Gen, else the host
// config's naming_generation, Legacy when it sets none. A host config
// under the legacy name that can't be read or parsed sets none, so
// allocating never fails on it; config.Load reports one that doesn't parse.
func (l Layout) Generation() (Generation, error) {
	if l.Gen != "" {
		return ParseGeneration(string(l.Gen))
	}
	path, err := l.HostConfig()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Legacy, nil
	}
	if err != nil {
		if !FlowNamed(path) {
			return Legacy, nil
		}
		return "", fmt.Errorf("reading host config: %w", err)
	}
	var c struct {
		NamingGeneration string `json:"naming_generation"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		if !FlowNamed(path) {
			return Legacy, nil
		}
		return "", fmt.Errorf("parsing host config %s: %w", path, err)
	}
	g, err := ParseGeneration(c.NamingGeneration)
	if err != nil {
		return "", fmt.Errorf("host config %s: %w", path, err)
	}
	return g, nil
}

// allocate is where new state of a kind goes, legacy being its legacy
// path: the legacy path under every generation, once the generation is
// known to be one.
func (l Layout) allocate(legacy string) (string, error) {
	if _, err := l.Generation(); err != nil {
		return "", err
	}
	return legacy, nil
}

// declaration is the path of one declaration file: flow when a file is
// there, else legacy. A file there is the one even when it can't be read:
// reading it is the caller's, and never falls back. A symlink there to
// nothing is an error, since its reader would take it for no file.
func declaration(legacy, flow string) (string, error) {
	_, err := os.Lstat(flow)
	switch {
	case err == nil:
		if _, err := os.Stat(flow); errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%s is a symlink to nothing", flow)
		}
		return flow, nil
	case errors.Is(err, fs.ErrNotExist):
		return legacy, nil
	}
	return "", fmt.Errorf("checking %s: %w", flow, err)
}

// FlowNamed reports whether path, as HostConfig or ProjectFile returned
// it, is the file under the flow names, which its reader holds to the
// strict rule: one there that can't be read or parsed is an error. A
// legacy file is read by its reader's own rule.
func FlowNamed(path string) bool {
	dir := filepath.Dir(path)
	return filepath.Base(dir) == filepath.Base(flowHome) && filepath.Base(filepath.Dir(dir)) == filepath.Dir(flowHome)
}

// HostConfig is the host config file.
func (l Layout) HostConfig() (string, error) {
	return declaration(l.legacy("config.json"), l.flow("config.json"))
}

// Project declaration files.
const (
	ProjectConfig = "config.json"
	WorkerPrompt  = "prompt.md"
	FixPrompt     = "pr-fix-prompt.md"
	SessionPrompt = "session-prompt.md"
	Principles    = "principles.md"
)

// ProjectFiles are the declaration files a project can hold.
var ProjectFiles = []string{ProjectConfig, WorkerPrompt, FixPrompt, SessionPrompt, Principles}

// ProjectFile is the declaration file name (ProjectConfig, WorkerPrompt,
// ...) of the project checked out at root.
func ProjectFile(root, name string) (string, error) {
	return declaration(filepath.Join(root, legacyHome, name), filepath.Join(root, flowHome, name))
}

// ScaffoldProjectFile is where scaffolding writes the declaration file
// name of the project checked out at root: the file there is, else where
// a new one goes.
func (l Layout) ScaffoldProjectFile(root, name string) (string, error) {
	return l.scaffold(filepath.Join(root, legacyHome, name), filepath.Join(root, flowHome, name))
}

// ScaffoldHostConfig is where scaffolding writes the host config: the
// file there is, else where a new one goes.
func (l Layout) ScaffoldHostConfig() (string, error) {
	return l.scaffold(l.legacy("config.json"), l.flow("config.json"))
}

func (l Layout) scaffold(legacy, flow string) (string, error) {
	path, err := declaration(legacy, flow)
	if err != nil || path == flow {
		return path, err
	}
	if _, err := os.Lstat(legacy); err == nil {
		return legacy, nil
	}
	return l.allocate(legacy)
}

// ConflictError is state flow keeps in one place found in two.
type ConflictError struct {
	What  string   // the kind of state, e.g. "the project registry"
	Paths []string // where it is
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s is at both %s; flow keeps it in one place, so remove or merge one by hand",
		e.What, strings.Join(e.Paths, " and "))
}

// one is the place of single-valued state: whichever of legacy and flow
// exists, and when neither does, where new state of its kind goes. Both
// existing is a ConflictError, unless symlinks make them one file, which
// is then named by its own path: a save renames a new file over the name
// it is given, which would replace a symlink, and parts a hard link's
// names.
func (l Layout) one(what, legacy, flow string) (string, error) {
	_, lerr := os.Stat(legacy)
	_, ferr := os.Stat(flow)
	for _, e := range []struct {
		path string
		err  error
	}{{legacy, lerr}, {flow, ferr}} {
		if e.err != nil && !errors.Is(e.err, fs.ErrNotExist) {
			return "", fmt.Errorf("checking %s: %w", e.path, e.err)
		}
	}
	switch {
	case lerr == nil && ferr == nil:
		lr, lerr := filepath.EvalSymlinks(legacy)
		fr, ferr := filepath.EvalSymlinks(flow)
		if lerr == nil && ferr == nil && lr == fr {
			return lr, nil
		}
		return "", &ConflictError{What: what, Paths: []string{legacy, flow}}
	case lerr == nil:
		return legacy, nil
	case ferr == nil:
		return flow, nil
	}
	return l.allocate(legacy)
}

// Registry is the project registry file.
func (l Layout) Registry() (string, error) {
	return l.one("the project registry", l.legacy("projects.json"), l.flow("projects.json"))
}

// Memory is the shared coordinator memory directory.
func (l Layout) Memory() (string, error) {
	return l.one("the shared coordinator memory", l.legacy("memory"), l.flow("memory"))
}

// SelfUpdateState is self-update's state file. Read it holding
// SelfUpdateLocks, so no update moves it in between.
func (l Layout) SelfUpdateState() (string, error) {
	return l.one("the self-update state", filepath.Join(l.StateHome, legacyState, "self-update.json"), l.flow("self-update.json"))
}

// SelfUpdateLocks are the lock files every self-update holds, in order.
func (l Layout) SelfUpdateLocks() []string {
	return []string{filepath.Join(l.StateHome, legacyState, "self-update.lock"), l.flow("self-update.lock")}
}

// ReviewLockDirs are the directories of the global review locks, used
// when no session holds a review's lock: each lock is a file of one name
// in both, held in order.
func (l Layout) ReviewLockDirs() []string {
	return []string{l.legacy("locks"), l.flow("locks")}
}

// ContinuationLocks are the files of the continuation lock of the clone
// whose git common directory is gitCommonDir, in order.
func ContinuationLocks(gitCommonDir string) []string {
	return []string{
		filepath.Join(gitCommonDir, "klaus-continue.lock"),
		filepath.Join(gitCommonDir, flowGitDir, "continuation.lock"),
	}
}

// AgentsSession is the detached tmux session the agents of the flow
// session id run in, id being empty outside a session. Every generation
// gives it the legacy name.
func AgentsSession(id string) string {
	if id == "" {
		return legacyAgents
	}
	// tmux treats "." and ":" as target separators.
	return legacyAgents + "-" + strings.NewReplacer(".", "-", ":", "-").Replace(id)
}

// AgentsSessions are the names flow gives the agents session of the flow
// session id under some generation: AgentsSession's alone, since every
// generation allocates the legacy names. A tmux session a run of that
// session records outside them is not one flow made for it.
func AgentsSessions(id string) []string {
	return []string{AgentsSession(id)}
}

// ConsultSnapshots is the directory one-shot consult snapshots go in.
// Each consult removes its own, at the path it was given.
func (l Layout) ConsultSnapshots() (string, error) {
	return l.allocate(l.legacy("consult-snapshots"))
}

// SyncLog is the file background project syncs append to.
func (l Layout) SyncLog() (string, error) {
	return l.allocate(l.legacy("sync.log"))
}

// SyncLogs are the sync log files that exist, legacy first: an older build
// may append to one while this one appends to another.
func (l Layout) SyncLogs() []string {
	return existing(l.legacy("sync.log"), l.flow("sync.log"))
}

// StandaloneStore is the directory a run started outside a session keeps
// its runs/ and logs/ in, in the clone whose git common directory is
// gitCommonDir.
func (l Layout) StandaloneStore(gitCommonDir string) (string, error) {
	return l.allocate(filepath.Join(gitCommonDir, legacyGitDir))
}

func existing(paths ...string) []string {
	var out []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// SessionRoots are the directories sessions are found under, legacy first.
func (l Layout) SessionRoots() []string {
	return []string{l.legacy("sessions"), l.flow("sessions")}
}

// Session is one session's directory.
type Session struct {
	ID  string
	Dir string
}

// ErrUnknownSession is a session ID no directory has.
var ErrUnknownSession = errors.New("unknown session")

// validSessionID rejects what would name a directory other than one entry
// of a sessions root.
func validSessionID(id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("%q is not a session ID", id)
	}
	return nil
}

// Sessions lists the session directories under both roots, by ID. A
// session ID under both roots, unless both are one directory, is left out
// and reported in a ConflictError, which comes back with the rest.
func (l Layout) Sessions() ([]Session, error) {
	byID := map[string][]string{}
	for _, root := range l.SessionRoots() {
		entries, err := os.ReadDir(root)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading sessions dir: %w", err)
		}
		for _, e := range entries {
			dir := filepath.Join(root, e.Name())
			if e.Type()&fs.ModeSymlink != 0 {
				// Session looks a symlinked session up: so does the listing.
				if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
					continue
				}
			} else if !e.IsDir() {
				continue
			}
			byID[e.Name()] = append(byID[e.Name()], dir)
		}
	}
	var out []Session
	var conflicts []error
	for id, dirs := range byID {
		dir, err := oneDir("session "+id, dirs)
		if err != nil {
			conflicts = append(conflicts, err)
			continue
		}
		out = append(out, Session{ID: id, Dir: dir})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Error() < conflicts[j].Error() })
	return out, errors.Join(conflicts...)
}

// oneDir is the one directory dirs name, or a ConflictError.
func oneDir(what string, dirs []string) (string, error) {
	if len(dirs) == 2 {
		a, aerr := os.Stat(dirs[0])
		b, berr := os.Stat(dirs[1])
		if aerr == nil && berr == nil && os.SameFile(a, b) {
			return dirs[0], nil
		}
	}
	if len(dirs) != 1 {
		return "", &ConflictError{What: what, Paths: dirs}
	}
	return dirs[0], nil
}

// Session is the directory of the session id: the one root that has it.
// An ID no root has is ErrUnknownSession; one both have is a
// ConflictError.
func (l Layout) Session(id string) (string, error) {
	if err := validSessionID(id); err != nil {
		return "", err
	}
	var dirs []string
	for _, root := range l.SessionRoots() {
		dir := filepath.Join(root, id)
		fi, err := os.Stat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("checking session dir: %w", err)
		}
		if fi.IsDir() {
			dirs = append(dirs, dir)
		}
	}
	if len(dirs) == 0 {
		return "", fmt.Errorf("%w %s: no %s", ErrUnknownSession, id, strings.Join(l.sessionPaths(id), " or "))
	}
	return oneDir("session "+id, dirs)
}

func (l Layout) sessionPaths(id string) []string {
	var out []string
	for _, root := range l.SessionRoots() {
		out = append(out, filepath.Join(root, id))
	}
	return out
}

// NewSession is the directory a new session id goes in. It does not
// create it; an id a root already has is an error.
func (l Layout) NewSession(id string) (string, error) {
	if err := validSessionID(id); err != nil {
		return "", err
	}
	if dir, err := l.Session(id); err == nil {
		return "", fmt.Errorf("session %s already exists at %s", id, dir)
	} else if !errors.Is(err, ErrUnknownSession) {
		return "", err
	}
	root, err := l.NewSessionsRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, id), nil
}

// NewSessionsRoot is the sessions root new sessions go under.
func (l Layout) NewSessionsRoot() (string, error) {
	return l.allocate(l.SessionRoots()[0])
}
