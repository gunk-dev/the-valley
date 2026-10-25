// Package selfupdate brings the installed valley CLI up to the tip of a
// configured branch of the-valley. It resolves the branch with git
// ls-remote, builds exactly that commit with nix, smoke-tests the build,
// and installs it into the user's default nix profile in place of the
// profile element named valley. Every step prints one short line, so a
// journal of timer runs reads as a history.
//
// It follows the self-update of an agent runner:
// the same steps, the same state file, the same flags.
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
	"syscall"
	"time"
)

// Element is the nix profile element self-update replaces. The profile
// names an element after its package, so a valley store path installs as
// it.
const Element = "valley"

const (
	lsRemoteTimeout = 2 * time.Minute
	smokeTimeout    = 2 * time.Minute
)

// SmokePolicy is where a valley package keeps the tiny policy the smoke
// test derives checks from, relative to the package's store path.
const SmokePolicy = "share/valley/smoke-policy"

// smokeWant is what `valley checks --names` derives from SmokePolicy for
// the scratch change smokeChecks makes: the floor's check for the edited
// README.md, and the project's check for the added src/main.go.
const smokeWant = "smoke-floor\nsmoke-project"

// State is what self-update.json records between runs.
type State struct {
	Current  *Install `json:"current,omitempty"`  // the valley self-update last put in the profile
	Previous *Install `json:"previous,omitempty"` // the one it replaced: --rollback reinstalls it
	Failed   *Failure `json:"failed,omitempty"`   // the last commit that failed to build or test
	Pinned   bool     `json:"pinned,omitempty"`   // --pin: leave Current alone until --unpin
	// UpdatedAt is when a run last changed this state.
	UpdatedAt time.Time `json:"updated_at"`
}

// Install is a valley build in the profile.
type Install struct {
	Commit    string    `json:"commit,omitempty"` // full SHA; a short one when read from a store path's name
	StorePath string    `json:"store_path"`
	At        time.Time `json:"at"` // when it went into the profile
}

// Failure is a commit that failed to build or pass the smoke test.
type Failure struct {
	Commit string    `json:"commit"`
	Error  string    `json:"error"`
	At     time.Time `json:"at"`
}

// Nix is what self-update asks of nix. Real is the nix on PATH. Tests put
// a builder and a profile of their own in its place, so they build nothing
// and touch no real profile.
type Nix interface {
	// Build builds installable and returns its output's store path.
	Build(ctx context.Context, installable string) (string, error)
	// Elements lists the default profile's elements, each name with its
	// store paths.
	Elements(ctx context.Context) (map[string][]string, error)
	// Remove removes the element called name from the default profile.
	Remove(ctx context.Context, name string) error
	// Add adds storePath to the default profile.
	Add(ctx context.Context, storePath string) error
}

// Updater runs self-update against one repo and branch.
type Updater struct {
	Repo    string    // git URL (or absolute path) of the-valley's repository
	Branch  string    // the branch to install, e.g. main
	Dir     string    // state directory, holding the lock and self-update.json
	Version string    // the running valley's version, e.g. 0.1.0-e949dba; "" if unknown
	Out     io.Writer // where each step's line goes
	Verbose bool      // also report runs that find valley up to date
	Nix     Nix       // nil is Real{}
	System  string    // the nix system to build for; "" is this machine's
}

// DefaultDir returns $XDG_STATE_HOME/valley, or ~/.local/state/valley.
func DefaultDir() (string, error) {
	if d := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "valley"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "valley"), nil
}

// StatePath is the state file in dir.
func StatePath(dir string) string { return filepath.Join(dir, "self-update.json") }

var safeBranch = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// gitEnv restricts every git self-update starts, directly or through nix,
// to the transports Validate accepts, and keeps it from prompting.
var gitEnv = []string{"GIT_ALLOW_PROTOCOL=https:ssh:git:file", "GIT_TERMINAL_PROMPT=0"}

// scpLike is user@host:path, git's shorthand for an ssh URL.
var scpLike = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9.-]*:.`)

// Validate checks the repo and branch before either reaches git or nix.
// The repo must be an https://, ssh://, git:// or file:// URL, an absolute
// path, or scp-like user@host:path: no other git transport (ext::, fd::)
// and only characters nix's flake-ref parser leaves as they are (urlSafe).
func Validate(repo, branch string) error {
	if err := validRepo(repo); err != nil {
		return fmt.Errorf("valley.selfUpdate.repo %q is not a repo self-update accepts: %v; use an https://, ssh://, git:// or file:// URL, an absolute path, or user@host:path", repo, err)
	}
	if !safeBranch.MatchString(branch) || strings.HasPrefix(branch, "-") || strings.Contains(branch, "..") {
		return fmt.Errorf("valley.selfUpdate.branch %q is not a branch name self-update accepts ([A-Za-z0-9._/-])", branch)
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
// parser keeps exactly these as they are, and re-encodes or rejects the
// rest (+ becomes %2B), so with them git ls-remote and nix build could
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
	unlock, err := u.lock(false)
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
		u.say("pinned at %s: leaving it alone until valley self-update --unpin", st.Current.label())
		return nil
	}

	ref := "refs/heads/" + u.Branch
	sha, err := lsRemote(ctx, RepoURL(u.Repo), ref)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", u.Branch, err)
	}
	if !isCommit(sha) {
		return fmt.Errorf("%s has no branch %s", u.Repo, u.Branch)
	}
	short := sha[:7]
	if u.upToDate(st, sha) && u.inProfile(ctx, st.Current) {
		if u.Verbose {
			u.say("%s is at %s: valley is up to date", u.Branch, short)
		}
		return nil
	}
	if f := st.Failed; f != nil && f.Commit == sha && !retry {
		u.say("skipping %s: it failed at %s: %s (it is retried when %s moves, or with --retry)",
			short, f.At.Local().Format(time.DateTime), f.Error, u.Branch)
		return nil
	}
	u.say("%s is at %s (installed: %s)", u.Branch, short, u.installedLabel(st))

	out, err := u.build(ctx, sha)
	if err == nil {
		err = u.smoke(ctx, out, sha)
	}
	if err != nil {
		st.Failed = &Failure{Commit: sha, Error: err.Error(), At: u.clock()}
		if serr := u.save(st); serr != nil {
			return fmt.Errorf("%s: %w (and recording the failure: %v)", short, err, serr)
		}
		u.say("%s failed; nothing installed", short)
		return fmt.Errorf("%s: %w", short, err)
	}

	old, err := u.replace(ctx, out)
	if err != nil {
		return err
	}
	if old != "" || st.Current == nil || st.Current.Commit != sha {
		st.Previous = u.replaced(st, old)
	} // else reinstalling a valley removed by hand keeps the rollback target
	st.Current = &Install{Commit: sha, StorePath: out, At: u.clock()}
	st.Failed = nil
	if err := u.save(st); err != nil {
		return err
	}
	u.say("installed %s into the nix profile, replacing %s", short, st.Previous.label())
	return nil
}

// Rollback swaps the current install for the previous one, so a second
// rollback returns to where the first started.
func (u *Updater) Rollback(ctx context.Context) error {
	unlock, err := u.lock(true)
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
		return errors.New("nothing to roll back to: self-update has not replaced a valley yet")
	}
	if _, err := os.Stat(prev.StorePath); err != nil {
		return fmt.Errorf("the previous valley is gone from the store (garbage-collected?): %w", err)
	}
	old, err := u.replace(ctx, prev.StorePath)
	if err != nil {
		return err
	}
	st.Previous = u.replaced(st, old)
	st.Current = &Install{Commit: prev.Commit, StorePath: prev.StorePath, At: u.clock()}
	if err := u.save(st); err != nil {
		return err
	}
	u.say("rolled back to %s; --rollback again returns to %s", st.Current.label(), st.Previous.label())
	if !st.Pinned {
		u.say("not pinned: the next run installs the tip of %s unless it is this one; valley self-update --pin keeps this one", u.Branch)
	}
	return nil
}

// Pin sets or clears the pin. While pinned, Update leaves the install alone.
func (u *Updater) Pin(pinned bool) error {
	unlock, err := u.lock(true)
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
		u.say("pinned at %s: self-update leaves it alone until valley self-update --unpin", st.Current.label())
	} else {
		u.say("unpinned: the next run installs the tip of %s if it is not installed", u.Branch)
	}
	return nil
}

func (u *Updater) nix() Nix {
	if u.Nix == nil {
		return Real{}
	}
	return u.Nix
}

// upToDate reports whether the installed valley was built from sha. Once
// self-update has installed a valley, the state file says which; before
// that, the running valley's version stamp does.
func (u *Updater) upToDate(st State, sha string) bool {
	if st.Current != nil {
		return st.Current.Commit == sha
	}
	c := versionCommit(u.Version)
	return c != "" && strings.HasPrefix(sha, c)
}

// inProfile reports whether the profile's valley element still holds inst,
// so a valley removed or replaced by hand gets reinstalled. With no install
// recorded, or a profile nix can't list, it trusts the state.
func (u *Updater) inProfile(ctx context.Context, inst *Install) bool {
	if inst == nil {
		return true
	}
	elems, err := u.nix().Elements(ctx)
	if err != nil {
		return true
	}
	if slices.Contains(elems[Element], inst.StorePath) {
		return true
	}
	u.say("the nix profile's valley is no longer %s; reinstalling it", inst.label())
	return false
}

func (u *Updater) installedLabel(st State) string {
	switch {
	case st.Current != nil:
		return st.Current.label()
	case u.Version != "":
		return "valley " + u.Version
	}
	return "a valley of unknown version"
}

// build builds the flake's valley package at exactly sha.
func (u *Updater) build(ctx context.Context, sha string) (string, error) {
	system := u.System
	if system == "" {
		system = System()
	}
	ref := FlakeRef(u.Repo, u.Branch, sha, system)
	u.say("building %s", ref)
	out, err := u.nix().Build(ctx, ref)
	if err != nil {
		return "", err
	}
	u.say("built %s", out)
	return out, nil
}

// smoke runs the build's valley three times, each in smokeEnv, so nothing
// the running valley set reaches it. valley --version must report sha's
// short form. valley help must exit 0. And valley checks --names must
// derive smokeWant from the tiny policy the package ships, over a scratch
// change: that composes a policy under the schema the build carries.
func (u *Updater) smoke(ctx context.Context, out, sha string) error {
	tmp, err := os.MkdirTemp("", "valley-smoke-")
	if err != nil {
		return fmt.Errorf("smoke test: %w", err)
	}
	defer os.RemoveAll(tmp)
	env := smokeEnv(tmp)
	bin := filepath.Join(out, "bin", "valley")

	v, err := runBin(ctx, env, "", bin, "--version")
	if err != nil {
		return fmt.Errorf("smoke test: valley --version: %w", err)
	}
	v = firstLine(v)
	if c := versionCommit(lastField(v)); len(c) < 7 || !strings.HasPrefix(sha, c) {
		return fmt.Errorf("smoke test: valley --version reports %q, not commit %s", v, sha[:7])
	}
	if _, err := runBin(ctx, env, "", bin, "help"); err != nil {
		return fmt.Errorf("smoke test: valley help: %w", err)
	}
	if err := smokeChecks(ctx, env, tmp, out); err != nil {
		return fmt.Errorf("smoke test: valley checks: %w", err)
	}
	u.say("smoke test passed: %s; valley help exits 0; valley checks composes the policy it ships", v)
	return nil
}

// smokeEnv is the environment a smoke test runs in. Only PATH comes from
// this process, so the build's wrapper finds what it does not put first
// itself. HOME is a scratch directory, so neither git nor cue reads the
// user's configuration, and the commits made there have an author of
// their own. Nothing else reaches the build: not the VALLEY_ variables the
// running valley's wrapper set, which would stand in for the build's own,
// and not GIT_ variables, which could point git anywhere.
func smokeEnv(home string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"TMPDIR=" + home,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=valley self-update",
		"GIT_AUTHOR_EMAIL=self-update@localhost",
		"GIT_COMMITTER_NAME=valley self-update",
		"GIT_COMMITTER_EMAIL=self-update@localhost",
	}
}

// smokeChecks makes a scratch repository with two commits and runs the
// build's valley checks over the change between them, with both policy
// layers taken from the package's SmokePolicy. The change edits README.md,
// which the floor's class covers, and adds src/main.go, which the
// project's class covers.
func smokeChecks(ctx context.Context, env []string, tmp, out string) error {
	repo := filepath.Join(tmp, "repo")
	gitIn := func(args ...string) (string, error) {
		return runBin(ctx, env, repo, "git", args...)
	}
	write := func(name, text string) error {
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(text), 0o644)
	}
	commit := func(msg string) (string, error) {
		if _, err := gitIn("add", "-A"); err != nil {
			return "", err
		}
		if _, err := gitIn("commit", "--quiet", "--no-verify", "-m", msg); err != nil {
			return "", err
		}
		sha, err := gitIn("rev-parse", "HEAD")
		return strings.TrimSpace(sha), err
	}
	if err := os.Mkdir(repo, 0o755); err != nil {
		return err
	}
	if _, err := gitIn("init", "--quiet"); err != nil {
		return err
	}
	if err := write("README.md", "smoke\n"); err != nil {
		return err
	}
	base, err := commit("base")
	if err != nil {
		return err
	}
	if err := write("README.md", "smoke test\n"); err != nil {
		return err
	}
	if err := write("src/main.go", "package main\n"); err != nil {
		return err
	}
	head, err := commit("head")
	if err != nil {
		return err
	}
	policy := filepath.Join(out, SmokePolicy)
	got, err := runBin(ctx, env, repo, filepath.Join(out, "bin", "valley"), "checks", "--names",
		"--instance", filepath.Join(policy, "instance"), "--project", filepath.Join(policy, "project"),
		base, head)
	if err != nil {
		return err
	}
	if got = strings.TrimSpace(got); got != smokeWant {
		return fmt.Errorf("derived %q from the policy the package ships, not %q", got, smokeWant)
	}
	return nil
}

// replace puts storePath in the profile in place of the valley element and
// returns the store path that element had, "" if the profile had none.
// Other elements are left alone. If adding fails, it puts the old one back.
func (u *Updater) replace(ctx context.Context, storePath string) (string, error) {
	n := u.nix()
	elems, err := n.Elements(ctx)
	if err != nil {
		return "", err
	}
	var old string
	if paths, ok := elems[Element]; ok {
		if len(paths) > 0 {
			old = paths[0]
		}
		if err := n.Remove(ctx, Element); err != nil {
			return "", err
		}
	}
	if err := n.Add(ctx, storePath); err != nil {
		if old != "" {
			// Put it back even if ctx is what ended the add.
			rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			if rerr := n.Add(rctx, old); rerr != nil {
				return "", fmt.Errorf("%w (and putting %s back: %v)", err, old, rerr)
			}
		}
		return "", err
	}
	return old, nil
}

// replaced describes the valley that replace took out of the profile.
func (u *Updater) replaced(st State, old string) *Install {
	switch {
	case old == "":
		// Removed from the profile by hand: still the one to roll back to.
		return st.Current
	case st.Current != nil && st.Current.StorePath == old:
		return st.Current
	default:
		// Installed by hand: its name carries the version stamp.
		return &Install{Commit: versionCommit(filepath.Base(old)), StorePath: old}
	}
}

// RepoURL is the one URL self-update reads repo by, for git ls-remote and
// for nix alike, so both read the same repository. A path becomes a
// file:// URL. user@host:path becomes an ssh:// URL naming the same
// repository git itself would: a path that starts with / or ~ as it is,
// and any other path under the login's home, as ssh://user@host/~/path.
// That is how a valley host serves its projects, from the git user's
// home, so git@host:the-valley reads as ssh://git@host/~/the-valley. A
// forge reads paths from its own root instead; name one by its https://
// or ssh:// URL. repo must have passed Validate, so it needs no escaping:
// it has no %, ?, # or whitespace.
func RepoURL(repo string) string {
	switch {
	case strings.Contains(repo, "://"):
		return repo
	case filepath.IsAbs(repo):
		return "file://" + repo
	}
	host, path, _ := strings.Cut(repo, ":")
	switch {
	case strings.HasPrefix(path, "/"):
		return "ssh://" + host + path
	case strings.HasPrefix(path, "~"):
		return "ssh://" + host + "/" + path
	}
	return "ssh://" + host + "/~/" + path
}

// FlakeRef names the flake's valley package for system at exactly sha on
// branch of repo, read by RepoURL.
func FlakeRef(repo, branch, sha, system string) string {
	return fmt.Sprintf("git+%s?ref=refs/heads/%s&rev=%s#packages.%s.valley", RepoURL(repo), branch, sha, system)
}

// lsRemote returns the commit ref names in the repository at url, or ""
// when it names none.
func lsRemote(ctx context.Context, url, ref string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, lsRemoteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--", url, ref)
	cmd.Env = append(os.Environ(), gitEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("git ls-remote timed out after %s", lsRemoteTimeout)
		}
		return "", fmt.Errorf("git ls-remote: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		// ls-remote matches patterns by their tail; keep the exact ref.
		if sha, name, ok := strings.Cut(line, "\t"); ok && name == ref {
			return sha, nil
		}
	}
	return "", nil
}

var errBusy = errors.New("another self-update is in progress")

// lock takes the state directory's lock. Without wait it returns errBusy
// when another run holds it; with wait it says so and waits.
func (u *Updater) lock(wait bool) (func(), error) {
	if err := os.MkdirAll(u.Dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(u.Dir, "self-update.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		if !wait {
			f.Close()
			return nil, errBusy
		}
		u.say("waiting for the self-update in progress to finish")
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}
	return func() { f.Close() }, nil // close drops the flock
}

// Load reads the state file in dir; a missing file is the zero State.
func Load(dir string) (State, error) {
	var st State
	data, err := os.ReadFile(StatePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("parsing %s: %w", StatePath(dir), err)
	}
	return st, nil
}

func (u *Updater) load() (State, error) { return Load(u.Dir) }

// save writes the state file through a rename, so a reader never sees half.
func (u *Updater) save(st State) error {
	st.UpdatedAt = u.clock()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(u.Dir, "self-update-*.json")
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
	return os.Rename(tmp.Name(), StatePath(u.Dir))
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
		return "nothing (the system valley)"
	case i.Commit != "":
		return shortSHA(i.Commit)
	}
	return i.StorePath
}

// runBin runs a program in env and dir and returns its stdout; an error
// carries its last line of output.
func runBin(ctx context.Context, env []string, dir, bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, smokeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if line := ErrorLine(stderr.String() + "\n" + stdout.String()); line != "" {
			return "", fmt.Errorf("%w: %s", err, line)
		}
		return "", err
	}
	return stdout.String(), nil
}

var hexRe = regexp.MustCompile(`^[0-9a-f]+$`)

func isCommit(s string) bool { return len(s) >= 40 && hexRe.MatchString(s) }

// versionCommit returns the commit stamped at the end of a valley version
// or store path name (0.1.0-e949dba gives e949dba), or "" if there is none:
// a build from a dirty tree ends in -dirty, and one from an unknown tree in
// -unknown.
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
