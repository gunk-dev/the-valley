// Package selfupdate brings the installed agent-run client up to the tip of
// a configured branch. It resolves the branch with git ls-remote, checks
// that the flake there offers the client's package by name, builds exactly
// that commit's packages.<system>.<attribute> with nix, smoke-tests the
// build, and installs it into the user's default nix profile in place of
// the element holding the client, klaus or valley-flow. Every step prints
// one short line, so a journal of timer runs reads as a history.
package selfupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"the-valley/flow/internal/git"
	"the-valley/flow/internal/location"
	"the-valley/flow/internal/nix"
)

// The names the-valley's flake offers the client under. Each is a flake
// attribute, packages.<system>.<name>; the name of that package, after
// which nix names the profile element a store path of it installs as; and
// the executable a build of it is smoke-tested as. Both packages hold the
// same program under both executable names. A flake's default package is
// never selected.
const (
	Klaus      = "klaus"
	ValleyFlow = "valley-flow"
)

// names are the profile elements that can hold the client.
var names = []string{Klaus, ValleyFlow}

const smokeTimeout = 2 * time.Minute

// State is what self-update.json records between runs.
type State struct {
	// Current is the build self-update last put in the profile, and
	// Previous the one it replaced, which --rollback reinstalls. A
	// replacement that lost the client, its build not going in and the one
	// it replaced not going back, records Current as none and Previous as
	// the lost build.
	Current  *Install `json:"current,omitempty"`
	Previous *Install `json:"previous,omitempty"`
	Failed   *Failure `json:"failed,omitempty"` // the last commit that failed to build or test
	Pinned   bool     `json:"pinned,omitempty"` // --pin: leave Current alone until --unpin
	// UpdatedAt is when a run last changed this state.
	UpdatedAt time.Time `json:"updated_at"`
}

// Install is a build of the client in the profile.
type Install struct {
	Commit    string `json:"commit,omitempty"` // full SHA; a short one when read from a store path's name
	StorePath string `json:"store_path"`
	// Element is the profile element holding it, Klaus or ValleyFlow, and
	// Attribute the flake package it was built from. Load reads either as
	// Klaus when the file has none.
	Element   string    `json:"element"`
	Attribute string    `json:"attribute"`
	At        time.Time `json:"at"` // when it went into the profile
}

// Failure is a commit that failed to build, pass the smoke test, or go
// into the profile.
type Failure struct {
	Commit string    `json:"commit"`
	Error  string    `json:"error"`
	At     time.Time `json:"at"`
}

// Updater runs self-update against one repo and branch.
type Updater struct {
	Repo    string    // git URL (or absolute path) of the klaus repository
	Branch  string    // the branch to install, e.g. main
	Dir     string    // one directory holding the lock and self-update.json, unless Locks and StateFile say otherwise
	Version string    // the running klaus's version, e.g. 0.4.1-e949dba
	Out     io.Writer // where each step's line goes
	Verbose bool      // also report runs that find klaus up to date
	// Attribute is the flake package Update builds and smoke-tests, Klaus
	// or ValleyFlow; empty is Klaus. The self-update command leaves it
	// empty.
	Attribute string
	// Locks are the lock files every update holds, in order, and StateFile
	// where self-update.json is, resolved once they are held
	// (location.Layout's SelfUpdateLocks and SelfUpdateState).
	Locks     []string
	StateFile func() (string, error)
	state     string // StateFile's answer, once asked: one per locked operation

	nix nixTool // nil is the nix on PATH (systemNix)
}

// nixTool is what self-update runs nix for.
type nixTool interface {
	// HasPackage reports whether flake offers packages.<system>.<name>.
	HasPackage(ctx context.Context, flake, name string) (bool, error)
	// Build builds installable and returns its output's store path.
	Build(ctx context.Context, installable string) (string, error)
	// ProfileElements lists the default profile's elements, each name
	// with its store paths.
	ProfileElements(ctx context.Context) (map[string][]string, error)
	// ProfileRemove removes the element called name from the profile.
	ProfileRemove(ctx context.Context, name string) error
	// ProfileAdd adds storePath to the profile, as the element nix names
	// after its package.
	ProfileAdd(ctx context.Context, storePath string) error
}

// systemNix is the nix on PATH, acting on the user's default profile. The
// git it fetches a flake with runs with gitEnv.
type systemNix struct{}

func (systemNix) HasPackage(ctx context.Context, flake, name string) (bool, error) {
	return nix.HasPackage(ctx, flake, name, gitEnv...)
}

func (systemNix) Build(ctx context.Context, installable string) (string, error) {
	paths, err := nix.Build(ctx, installable, gitEnv...)
	if err != nil {
		return "", err
	}
	return paths[0], nil
}

func (systemNix) ProfileElements(ctx context.Context) (map[string][]string, error) {
	return nix.ProfileElements(ctx)
}

func (systemNix) ProfileRemove(ctx context.Context, name string) error {
	return nix.ProfileRemove(ctx, name)
}

func (systemNix) ProfileAdd(ctx context.Context, storePath string) error {
	return nix.ProfileAdd(ctx, storePath)
}

func (u *Updater) tool() nixTool {
	if u.nix != nil {
		return u.nix
	}
	return systemNix{}
}

// attribute is the flake package Update builds.
func (u *Updater) attribute() string {
	if u.Attribute == "" {
		return Klaus
	}
	return u.Attribute
}

// StatePath is the state file in dir.
func StatePath(dir string) string { return filepath.Join(dir, "self-update.json") }

var safeBranch = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// gitEnv restricts every git self-update starts, directly or through nix,
// to the transports Validate accepts, and keeps it from prompting.
var gitEnv = []string{"GIT_ALLOW_PROTOCOL=https:ssh:git:file", "GIT_TERMINAL_PROMPT=0"}

// scpLike is user@host:path, git's shorthand for an ssh URL.
var scpLike = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9.-]*:.`)

// Validate checks the repo and branch before any of them reach git or nix.
// The repo must be an https://, ssh://, git:// or file:// URL, an absolute
// path, or scp-like user@host:path: no other git transport (ext::, fd::)
// and only characters nix's flake-ref parser leaves as they are (urlSafe).
func Validate(repo, branch string) error {
	if err := validRepo(repo); err != nil {
		return fmt.Errorf("self_update.repo %q is not a repo self-update accepts: %v; use an https://, ssh://, git:// or file:// URL, an absolute path, or user@host:path", repo, err)
	}
	if !safeBranch.MatchString(branch) || strings.HasPrefix(branch, "-") || strings.Contains(branch, "..") {
		return fmt.Errorf("self_update.branch %q is not a branch name self-update accepts ([A-Za-z0-9._/-])", branch)
	}
	return nil
}

func validRepo(repo string) error {
	switch {
	case repo == "":
		return errors.New("it is empty")
	case strings.HasPrefix(repo, "-"):
		return errors.New("it starts with -")
	}
	if scheme, _, ok := strings.Cut(repo, "://"); ok {
		u, err := url.Parse(repo)
		if err != nil {
			return err
		}
		// An IPv6 host's brackets pass through nix unchanged.
		if err := urlSafe(strings.Replace(repo, u.Host, strings.NewReplacer("[", "", "]", "").Replace(u.Host), 1)); err != nil {
			return err
		}
		switch scheme {
		case "https", "ssh", "git":
			if u.Hostname() == "" || strings.HasPrefix(u.Hostname(), "-") {
				return errors.New("it names no host")
			}
		case "file":
			if u.Host != "" || !strings.HasPrefix(u.Path, "/") {
				return errors.New("a file:// URL must be file:///<absolute path>")
			}
		default:
			return fmt.Errorf("transport %s:// is not allowed", scheme)
		}
		return nil
	}
	if err := urlSafe(repo); err != nil {
		return err
	}
	switch {
	case filepath.IsAbs(repo):
		return nil
	case strings.Contains(repo, "::"):
		return errors.New("transport-style <transport>::<address> is not allowed")
	case scpLike.MatchString(repo):
		return nil
	}
	return errors.New("it is not a URL, an absolute path or user@host:path")
}

// urlSafe refuses any character outside [A-Za-z0-9._~/:@-]: nix's flake-ref
// parser keeps exactly these as they are (checked against Nix 2.34 with
// builtins.parseFlakeRef and flakeRefToString), and re-encodes or rejects
// the rest (+ becomes %2B), so with them git ls-remote and nix build could
// name different URLs. ? and # it would read as flake-ref syntax.
func urlSafe(s string) error {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("._~/:@-", r):
		default:
			return fmt.Errorf("it has %q; only letters, digits and ._~/:@- are allowed, which nix leaves as they are", r)
		}
	}
	return nil
}

// Update installs the branch's tip unless it is installed already, failed
// before (unless retry), or the install is pinned. A run that finds another
// in progress leaves it to finish and returns nil.
func (u *Updater) Update(ctx context.Context, retry bool) error {
	if err := Validate(u.Repo, u.Branch); err != nil {
		return err
	}
	attr := u.attribute()
	if !slices.Contains(names, attr) {
		return fmt.Errorf("self-update builds packages.<system>.%s or packages.<system>.%s, not %q", Klaus, ValleyFlow, attr)
	}
	unlock, paths, err := u.begin(ctx, false)
	if errors.Is(err, errBusy) {
		u.say("another self-update is in progress; leaving it to finish")
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()
	st, err := u.load()
	if err != nil {
		return err
	}
	if st.Pinned {
		u.say("pinned at %s: leaving it alone until valley flow self-update --unpin", st.Current.label())
		return nil
	}

	ref := "refs/heads/" + u.Branch
	sha, err := git.LsRemote(ctx, RepoURL(u.Repo), ref, gitEnv...)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", u.Branch, err)
	}
	if !isCommit(sha) {
		return fmt.Errorf("%s has no branch %s", u.Repo, u.Branch)
	}
	short := sha[:7]
	if u.upToDate(st, sha) && u.inProfile(st.Current, paths) {
		if u.Verbose {
			u.say("%s is at %s: flow is up to date", u.Branch, short)
		}
		return nil
	}
	// Before anything changes, and whenever the tip is not installed: a
	// repository whose flake offers no package of the attribute by name is
	// not one self-update installs from. It comes before a remembered
	// failure of the same tip, which would otherwise hide the refusal until
	// the tip moves. A failure to read the flake is not recorded, so the
	// next run reads it again.
	offers, err := u.tool().HasPackage(ctx, flakeSource(u.Repo, u.Branch, sha), attr)
	if err != nil {
		return fmt.Errorf("reading the packages %s offers at %s: %w", u.Repo, short, err)
	}
	if !offers {
		return u.noPackage(short, attr)
	}
	if f := st.Failed; f != nil && f.Commit == sha && !retry {
		u.say("skipping %s: it failed at %s: %s (it is retried when %s moves, or with --retry)",
			short, f.At.Local().Format(time.DateTime), f.Error, u.Branch)
		return nil
	}
	u.say("%s is at %s (installed: %s)", u.Branch, short, u.installedLabel(st))

	out, err := u.build(ctx, sha, attr)
	if err == nil {
		err = u.smoke(ctx, out, sha, attr)
	}
	var element, old string
	if err == nil {
		element, old, err = u.replace(ctx, out)
	}
	var lost *restoreError
	switch {
	case errors.Is(err, errBothInstalled):
		return err
	case errors.As(err, &lost):
		// The profile holds no client now. The state says so and holds no
		// failure, so the next run installs the tip, whichever commit it
		// is, rather than hold this one.
		st.Previous = u.replaced(st, lost.element, lost.old)
		st.Current, st.Failed = nil, nil
		if serr := u.save(st); serr != nil {
			return fmt.Errorf("%s: %w (and recording that the profile holds no flow: %v)", short, err, serr)
		}
		return fmt.Errorf("%s: %w\n"+
			"  the nix profile holds no flow now, and the next run installs the tip of %s;\n"+
			"  to put back the build it held by hand: nix profile add %s", short, err, u.Branch, lost.old)
	case err != nil:
		// A build that fails, fails its smoke test or does not go into
		// the profile is held: running it again on every run would
		// rebuild it, or remove and add the profile's element, each time.
		st.Failed = &Failure{Commit: sha, Error: err.Error(), At: u.clock()}
		if serr := u.save(st); serr != nil {
			return fmt.Errorf("%s: %w (and recording the failure: %v)", short, err, serr)
		}
		u.say("%s failed; nothing installed", short)
		return fmt.Errorf("%s: %w", short, err)
	}
	// The build replaced becomes the rollback target. With none in the
	// profile, that is the recorded build, unless this reinstalls it; with
	// none recorded either, before a first install or after a lost
	// replacement, the target stays.
	if old != "" || st.Current != nil && st.Current.Commit != sha {
		st.Previous = u.replaced(st, element, old)
	}
	st.Current = &Install{Commit: sha, StorePath: out, Element: attr, Attribute: attr, At: u.clock()}
	st.Failed = nil
	if err := u.save(st); err != nil {
		return err
	}
	u.say("installed %s into the nix profile, replacing %s", short, st.Previous.label())
	return nil
}

// noPackage refuses a repository whose flake at short offers no package of
// attribute attr. The way out is the updater's own: pointed at a repository
// whose flake offers it, the next run builds, smoke-tests and installs it,
// and puts the working build back if installing fails.
func (u *Updater) noPackage(short, attr string) error {
	pkg := "packages." + nix.System() + "." + attr
	return fmt.Errorf("%s at %s offers no %s; nothing changed\n"+
		"  set self_update.repo in ~/.klaus/config.json to a repository whose flake offers %s,\n"+
		"  as the-valley's does, and run valley flow self-update again",
		u.Repo, short, pkg, pkg)
}

// Rollback swaps the current install for the previous one, so a second
// rollback returns to where the first started. It adds the store path
// recorded as previous, which nix names as the element it was taken from,
// in place of the element now holding the client.
func (u *Updater) Rollback(ctx context.Context) error {
	unlock, _, err := u.begin(ctx, true)
	if err != nil {
		return err
	}
	defer unlock()
	st, err := u.load()
	if err != nil {
		return err
	}
	prev := st.Previous
	if prev == nil || prev.StorePath == "" {
		return errors.New("nothing to roll back to: self-update has not replaced a klaus yet")
	}
	if _, err := os.Stat(prev.StorePath); err != nil {
		return fmt.Errorf("the previous klaus is gone from the store (garbage-collected?): %w", err)
	}
	element, old, err := u.replace(ctx, prev.StorePath)
	if err != nil {
		return err
	}
	st.Previous = u.replaced(st, element, old)
	st.Current = &Install{Commit: prev.Commit, StorePath: prev.StorePath, Element: prev.Element, Attribute: prev.Attribute, At: u.clock()}
	if err := u.save(st); err != nil {
		return err
	}
	if st.Previous != nil {
		u.say("rolled back to %s; --rollback again returns to %s", st.Current.label(), st.Previous.label())
	} else { // from a lost replacement: nothing was in the profile
		u.say("rolled back to %s", st.Current.label())
	}
	if !st.Pinned {
		u.say("not pinned: the next run installs the tip of %s unless it is this one; valley flow self-update --pin keeps this one", u.Branch)
	}
	return nil
}

// Pin sets or clears the pin. While pinned, Update leaves the install alone.
func (u *Updater) Pin(ctx context.Context, pinned bool) error {
	unlock, _, err := u.begin(ctx, true)
	if err != nil {
		return err
	}
	defer unlock()
	st, err := u.load()
	if err != nil {
		return err
	}
	st.Pinned = pinned
	if err := u.save(st); err != nil {
		return err
	}
	if pinned {
		u.say("pinned at %s: self-update leaves it alone until valley flow self-update --unpin", st.Current.label())
	} else {
		u.say("unpinned: the next run installs the tip of %s if it is not installed", u.Branch)
	}
	return nil
}

// upToDate reports whether the installed client was built from sha. Once
// self-update has installed a build, the state file says which, or that a
// replacement lost it; before that, the running program's version stamp
// does.
func (u *Updater) upToDate(st State, sha string) bool {
	if st.recorded() {
		return st.Current != nil && st.Current.Commit == sha
	}
	c := versionCommit(u.Version)
	return c != "" && strings.HasPrefix(sha, c)
}

// recorded reports whether self-update has put a build in the profile, so
// that the state, not the running program, says what the profile holds.
func (st State) recorded() bool { return st.Current != nil || st.Previous != nil }

// inProfile reports whether the profile's client element, holding paths,
// still holds inst, so a build removed or replaced by hand gets
// reinstalled. With no install recorded, the version stamp upToDate read
// stands.
func (u *Updater) inProfile(inst *Install, paths []string) bool {
	if inst == nil || slices.Contains(paths, inst.StorePath) {
		return true
	}
	u.say("the nix profile's %s is no longer %s; reinstalling it", inst.Element, inst.label())
	return false
}

// begin takes the state directory's lock, as lock does, and then lists the
// profile, which every operation does before anything else: a profile
// holding the client under both names, or one nix cannot list, is refused
// with nothing changed, whatever the state would have the operation do. It
// returns the store paths of the element holding the client, none when
// the profile holds neither.
func (u *Updater) begin(ctx context.Context, wait bool) (unlock func(), paths []string, err error) {
	if unlock, err = u.lock(wait); err != nil {
		return nil, nil, err
	}
	if _, paths, err = u.installed(ctx); err != nil {
		unlock()
		return nil, nil, err
	}
	return unlock, paths, nil
}

// errBothInstalled refuses a profile holding the client as both klaus and
// valley-flow: which of them to replace would be a guess.
var errBothInstalled = fmt.Errorf("the nix profile holds both %[1]s and %[2]s, and self-update replaces one; nothing changed\n"+
	"  remove the one not to keep (nix profile remove %[1]s, or %[2]s) and run valley flow self-update again", Klaus, ValleyFlow)

// installed returns the profile element holding the client, klaus or
// valley-flow, and its store paths; "" when the profile holds neither.
func (u *Updater) installed(ctx context.Context) (string, []string, error) {
	elems, err := u.tool().ProfileElements(ctx)
	if err != nil {
		return "", nil, err
	}
	var found string
	for _, name := range names {
		if _, ok := elems[name]; !ok {
			continue
		}
		if found != "" {
			return "", nil, errBothInstalled
		}
		found = name
	}
	return found, elems[found], nil
}

func (u *Updater) installedLabel(st State) string {
	if st.recorded() {
		return st.Current.label()
	}
	return "klaus " + u.Version
}

// build builds the flake's package attr at exactly sha.
func (u *Updater) build(ctx context.Context, sha, attr string) (string, error) {
	ref := FlakeRef(u.Repo, u.Branch, sha, attr)
	u.say("building %s", ref)
	out, err := u.tool().Build(ctx, ref)
	if err != nil {
		return "", err
	}
	u.say("built %s", out)
	return out, nil
}

// smoke runs the build's executable named exe, the attribute it was built
// from: --version must report sha's short form, and status must exit 0,
// which shows the build reads the existing run state.
func (u *Updater) smoke(ctx context.Context, out, sha, exe string) error {
	bin := filepath.Join(out, "bin", exe)
	v, err := runBin(ctx, bin, "--version")
	if err != nil {
		return fmt.Errorf("smoke test: %s --version: %w", exe, err)
	}
	v = firstLine(v)
	if c := versionCommit(lastField(v)); len(c) < 7 || !strings.HasPrefix(sha, c) {
		return fmt.Errorf("smoke test: %s --version reports %q, not commit %s", exe, v, sha[:7])
	}
	if _, err := runBin(ctx, bin, "status"); err != nil {
		return fmt.Errorf("smoke test: %s status: %w", exe, err)
	}
	u.say("smoke test passed: %s; %s status exits 0", v, exe)
	return nil
}

// replace puts storePath in the profile in place of the element holding
// the client, klaus or valley-flow, and returns that element's name and
// the store path it had, both "" if the profile had neither. Other
// elements are left alone, and a profile holding both is refused with
// nothing changed. If adding fails, it adds the old store path back, which
// nix names as the element it was.
func (u *Updater) replace(ctx context.Context, storePath string) (element, old string, err error) {
	element, paths, err := u.installed(ctx)
	if err != nil {
		return "", "", err
	}
	if element != "" {
		if len(paths) > 0 {
			old = paths[0]
		}
		if err := u.tool().ProfileRemove(ctx, element); err != nil {
			return "", "", err
		}
	}
	if err := u.tool().ProfileAdd(ctx, storePath); err != nil {
		if old != "" {
			// Put it back even if ctx is what ended the add.
			rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			if rerr := u.tool().ProfileAdd(rctx, old); rerr != nil {
				return "", "", &restoreError{add: err, element: element, old: old, restore: rerr}
			}
		}
		return "", "", err
	}
	return element, old, nil
}

// restoreError is an add that failed after replace removed element,
// whose store path old then failed to go back too: the profile holds no
// client.
type restoreError struct {
	add, restore error
	element, old string
}

func (e *restoreError) Error() string {
	return fmt.Sprintf("%v (and putting %s back: %v)", e.add, e.old, e.restore)
}

func (e *restoreError) Unwrap() error { return e.add }

// replaced describes the build that replace took out of the profile's
// element.
func (u *Updater) replaced(st State, element, old string) *Install {
	switch {
	case old == "":
		// Removed from the profile by hand: still the one to roll back to.
		return st.Current
	case st.Current != nil && st.Current.StorePath == old:
		prev := *st.Current
		prev.Element = element
		return &prev
	default:
		// Installed by hand: its name carries the version stamp, and the
		// element's name is its package's.
		return &Install{Commit: versionCommit(filepath.Base(old)), StorePath: old, Element: element, Attribute: element}
	}
}

// RepoURL is the one URL self-update reads repo by, for git ls-remote and
// for nix alike, so both read the same repository. A path becomes a
// file:// URL, and user@host:path becomes ssh://user@host/path: a relative
// path counts from the server's root, as forges read it, so a path in your
// home on a plain server is written user@host:~/path. repo must have passed
// Validate, so it needs no escaping: it has no %, ?, # or whitespace.
func RepoURL(repo string) string {
	switch {
	case strings.Contains(repo, "://"):
		return repo
	case filepath.IsAbs(repo):
		return "file://" + repo
	}
	host, path, _ := strings.Cut(repo, ":")
	return "ssh://" + host + "/" + strings.TrimPrefix(path, "/")
}

// FlakeRef names the flake's package attr at exactly sha on branch of
// repo, read by RepoURL.
func FlakeRef(repo, branch, sha, attr string) string {
	return flakeSource(repo, branch, sha) + "#packages." + nix.System() + "." + attr
}

// flakeSource is the flake at exactly sha on branch of repo, read by
// RepoURL. Validate keeps ", \ and $ out of it.
func flakeSource(repo, branch, sha string) string {
	return fmt.Sprintf("git+%s?ref=refs/heads/%s&rev=%s", RepoURL(repo), branch, sha)
}

var errBusy = errors.New("another self-update is in progress")

// lock takes u's locks. Without wait it returns errBusy when another run
// holds one; with wait it says so and waits.
func (u *Updater) lock(wait bool) (func(), error) {
	paths := u.Locks
	if len(paths) == 0 {
		paths = []string{filepath.Join(u.Dir, "self-update.lock")}
	}
	l, err := location.TryLock(paths...)
	if errors.Is(err, location.ErrBusy) {
		if !wait {
			return nil, errBusy
		}
		u.say("waiting for the self-update in progress to finish")
		l, err = location.WaitLock(paths...)
	}
	if err != nil {
		return nil, err
	}
	return l.Release, nil
}

// statePath is where u's self-update.json is, resolved once, under the
// locks: load and save of one operation use the same file.
func (u *Updater) statePath() (string, error) {
	if u.StateFile == nil {
		return StatePath(u.Dir), nil
	}
	if u.state == "" {
		path, err := u.StateFile()
		if err != nil {
			return "", err
		}
		u.state = path
	}
	return u.state, nil
}

// Load reads the state file in dir; a missing file is the zero State.
func Load(dir string) (State, error) { return LoadFile(StatePath(dir)) }

// LoadFile reads the state file at path; a missing file is the zero State.
// An install recorded without an element or attribute, as an updater that
// knows klaus alone records it, is klaus's.
func LoadFile(path string) (State, error) {
	var st State
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("parsing %s: %w", path, err)
	}
	for _, i := range []*Install{st.Current, st.Previous} {
		if i == nil {
			continue
		}
		if i.Element == "" {
			i.Element = Klaus
		}
		if i.Attribute == "" {
			i.Attribute = Klaus
		}
	}
	return st, nil
}

func (u *Updater) load() (State, error) {
	path, err := u.statePath()
	if err != nil {
		return State{}, err
	}
	return LoadFile(path)
}

// save writes the state file back where load read it, through a rename,
// so a reader never sees half.
func (u *Updater) save(st State) error {
	path, err := u.statePath()
	if err != nil {
		return err
	}
	st.UpdatedAt = u.clock()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "self-update-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (u *Updater) say(format string, args ...any) {
	if u.Out != nil {
		fmt.Fprintf(u.Out, format+"\n", args...)
	}
}

func (u *Updater) clock() time.Time { return time.Now().UTC().Truncate(time.Second) }

func (i *Install) label() string {
	switch {
	case i == nil:
		return "nothing (the system klaus)"
	case i.Commit != "":
		return shortSHA(i.Commit)
	}
	return i.StorePath
}

// runBin runs a build's executable and returns its stdout; an error carries
// its last line of output.
func runBin(ctx context.Context, bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, smokeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if line := nix.ErrorLine(stderr.String() + "\n" + stdout.String()); line != "" {
			return "", fmt.Errorf("%w: %s", err, line)
		}
		return "", err
	}
	return stdout.String(), nil
}

var hexRe = regexp.MustCompile(`^[0-9a-f]+$`)

func isCommit(s string) bool { return len(s) >= 40 && hexRe.MatchString(s) }

// versionCommit returns the commit stamped at the end of a klaus version or
// store path name (0.4.1-e949dba gives e949dba), or "" if there is none.
func versionCommit(v string) string {
	i := strings.LastIndex(v, "-")
	if i < 0 {
		return ""
	}
	c := v[i+1:]
	if len(c) < 7 || !hexRe.MatchString(c) {
		return ""
	}
	return c
}

func shortSHA(s string) string { return s[:min(len(s), 7)] }

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(l)
}

func lastField(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}
