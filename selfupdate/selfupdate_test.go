package selfupdate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// acceptedRepos covers every form Validate accepts.
var acceptedRepos = []string{
	"https://github.com/gunk-dev/the-valley",
	"https://github.com/gunk-dev/the-valley.git",
	"ssh://git@github.com/gunk-dev/the-valley",
	"ssh://git@[::1]:2222/srv/the-valley.git",
	"git://example.com/the-valley.git",
	"file:///srv/git/the-valley.git",
	"/srv/git/the-valley.git",
	"git@classic-laddie:the-valley",
	"me@host.example:~/the-valley.git",
	"me@host.example:/srv/git/the-valley.git",
	"https://example.com/a-b_c.d~e/f:g@h",
}

func TestValidateRepo(t *testing.T) {
	for _, repo := range acceptedRepos {
		if err := Validate(repo, "main"); err != nil {
			t.Errorf("Validate(%q) refused: %v", repo, err)
		}
	}
	for _, repo := range []string{
		"",
		"ext::sh -c touch% /tmp/pwned",
		"ext::sh",
		"fd::17",
		"foo::bar",
		"me@host::path",
		"-uhttps://example.com/the-valley",
		"--upload-pack=touch /tmp/pwned",
		"/srv/git/the-valley.git?rev=0000000",
		"/srv/git/the-valley.git#packages.x86_64-linux.evil",
		"https://example.com/the-valley?ref=evil",
		"/srv/git/my valley.git",
		"/srv/git/valley%20repo.git",
		"https://example.com/repo+name",
		"https://example.com/repo;x",
		"/srv/git/[valley].git",
		"/srv/git/the-valley.git\n",
		"http://example.com/the-valley",
		"ftp://example.com/the-valley",
		"file://relative/the-valley",
		"ssh://-oProxyCommand=touch/the-valley",
		"relative/the-valley",
		"host:the-valley",
	} {
		err := Validate(repo, "main")
		if err == nil {
			t.Errorf("Validate(%q) accepted it", repo)
			continue
		}
		if !strings.Contains(err.Error(), "valley.selfUpdate.repo") {
			t.Errorf("Validate(%q) = %v; want the error to name valley.selfUpdate.repo", repo, err)
		}
	}
	for _, branch := range []string{"", "-main", "a..b", "main branch", "main;x"} {
		if err := Validate("/srv/git/the-valley.git", branch); err == nil || !strings.Contains(err.Error(), "valley.selfUpdate.branch") {
			t.Errorf("Validate(branch %q) = %v; want a refusal naming valley.selfUpdate.branch", branch, err)
		}
	}
}

// TestRepoURL: ls-remote and nix read a repo by the same URL, the one the
// flake ref carries, and user@host:path names what git itself would.
func TestRepoURL(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for repo, want := range map[string]string{
		"/srv/git/the-valley.git":                 "file:///srv/git/the-valley.git",
		"file:///srv/git/the-valley.git":          "file:///srv/git/the-valley.git",
		"https://github.com/gunk-dev/the-valley":  "https://github.com/gunk-dev/the-valley",
		"git@classic-laddie:the-valley":           "ssh://git@classic-laddie/~/the-valley",
		"me@host.example:/srv/git/the-valley.git": "ssh://me@host.example/srv/git/the-valley.git",
		"me@host.example:~/the-valley.git":        "ssh://me@host.example/~/the-valley.git",
		"me@host.example:~you/the-valley.git":     "ssh://me@host.example/~you/the-valley.git",
	} {
		if got := RepoURL(repo); got != want {
			t.Errorf("RepoURL(%q) = %q, want %q", repo, got, want)
		}
		ref := "git+" + want + "?ref=refs/heads/main&rev=" + sha + "#packages.x86_64-linux.valley"
		if got := FlakeRef(repo, "main", sha, "x86_64-linux"); got != ref {
			t.Errorf("FlakeRef(%q) = %q, want %q", repo, got, ref)
		}
	}
}

// TestFlakeRefSurvivesNix: the installed nix parses each accepted repo's
// flake ref and prints it back unchanged, so nix fetches the URL ls-remote
// read. A nix build has no nix, so this runs only outside one.
func TestFlakeRefSurvivesNix(t *testing.T) {
	if _, err := exec.LookPath("nix"); err != nil {
		t.Skip("nix not installed")
	}
	sha := strings.Repeat("a", 40)
	for _, repo := range acceptedRepos {
		ref, _, _ := strings.Cut(FlakeRef(repo, "main", sha, "x86_64-linux"), "#")
		out, err := exec.Command("nix", "--extra-experimental-features", "nix-command flakes", "eval", "--raw", "--expr",
			`builtins.flakeRefToString (builtins.parseFlakeRef "`+ref+`")`).CombinedOutput()
		if err != nil {
			t.Fatalf("nix eval of %q: %v\n%s", ref, err, out)
		}
		// nix prints git+git:// by its alias git://, the same URL.
		if got := strings.Replace(ref, "git+git://", "git://", 1); string(out) != got {
			t.Errorf("repo %q: nix turned %q into %q", repo, got, out)
		}
	}
}

// upstream is a bare repository standing for the-valley's, and a clone
// that pushes commits to its main.
type upstream struct {
	t    *testing.T
	bare string
	work string
}

func newUpstream(t *testing.T) *upstream {
	dir := t.TempDir()
	u := &upstream{t: t, bare: filepath.Join(dir, "the-valley.git"), work: filepath.Join(dir, "work")}
	u.git(dir, "init", "--quiet", "--bare", u.bare)
	u.git(dir, "init", "--quiet", u.work)
	return u
}

// git runs git with no configuration but an identity of its own.
func (u *upstream) git(dir string, args ...string) string {
	u.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@localhost",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@localhost")
	out, err := cmd.CombinedOutput()
	if err != nil {
		u.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// push commits to main and pushes it, returning the commit.
func (u *upstream) push(msg string) string {
	u.git(u.work, "commit", "--quiet", "--allow-empty", "-m", msg)
	u.git(u.work, "push", "--quiet", u.bare, "HEAD:refs/heads/main")
	return u.git(u.work, "rev-parse", "HEAD")
}

// fakeNix builds a valley for any commit as a small script under store, and
// keeps the profile as a map. A commit in broken builds a valley that fails
// one step of the smoke test, or fails to build at all.
type fakeNix struct {
	store   string
	profile map[string][]string
	broken  map[string]string // commit → "build", "version", "help" or "checks"
	builds  []string          // the commits built, in order
}

func newFakeNix(t *testing.T) *fakeNix {
	return &fakeNix{store: t.TempDir(), profile: map[string][]string{}, broken: map[string]string{}}
}

func (f *fakeNix) Build(_ context.Context, ref string) (string, error) {
	_, rest, _ := strings.Cut(ref, "&rev=")
	sha, _, _ := strings.Cut(rest, "#")
	f.builds = append(f.builds, sha)
	if f.broken[sha] == "build" {
		return "", errors.New("nix build: exit status 1: error: builder failed")
	}
	version, help, checks := "0.1.0-"+sha[:7], "exit 0", `printf 'smoke-floor\nsmoke-project\n'`
	switch f.broken[sha] {
	case "version":
		version = "0.1.0-0000000"
	case "help":
		help = "echo 'valley: broken' >&2; exit 1"
	case "checks":
		checks = "echo smoke-floor"
	}
	out := filepath.Join(f.store, fmt.Sprintf("%d-valley-%s", len(f.builds), version))
	// A VALLEY_ variable from the running valley would stand in for the
	// build's own, so the smoke test must not pass one on.
	script := fmt.Sprintf(`#!/bin/sh
if env | grep -q '^VALLEY_'; then echo "valley: inherited $(env | grep '^VALLEY_')" >&2; exit 1; fi
case "$1" in
--version) echo "valley %s" ;;
help) %s ;;
checks) [ -d "$4" ] && [ -d "$6" ] || exit 1; %s ;;
*) exit 2 ;;
esac
`, version, help, checks)
	if err := os.MkdirAll(filepath.Join(out, "bin"), 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(out, SmokePolicy, "instance"), 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(out, SmokePolicy, "project"), 0o755); err != nil {
		return "", err
	}
	return out, os.WriteFile(filepath.Join(out, "bin", "valley"), []byte(script), 0o755)
}

func (f *fakeNix) Elements(context.Context) (map[string][]string, error) {
	return maps.Clone(f.profile), nil
}

func (f *fakeNix) Remove(_ context.Context, name string) error {
	delete(f.profile, name)
	return nil
}

func (f *fakeNix) Add(_ context.Context, storePath string) error {
	if _, ok := f.profile[Element]; ok {
		return errors.New("nix profile add: a valley is installed already")
	}
	f.profile[Element] = []string{storePath}
	return nil
}

// installed is the store path the profile's valley element holds.
func (f *fakeNix) installed() string {
	if p := f.profile[Element]; len(p) > 0 {
		return p[0]
	}
	return ""
}

// fixture is an updater over a fresh upstream, fake nix and state
// directory, with output collected in out.
type fixture struct {
	t   *testing.T
	up  *upstream
	nix *fakeNix
	u   *Updater
	out *bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	up, nix, out := newUpstream(t), newFakeNix(t), &bytes.Buffer{}
	return &fixture{t: t, up: up, nix: nix, out: out, u: &Updater{
		Repo: up.bare, Branch: "main", Dir: t.TempDir(), Out: out, Nix: nix, System: "x86_64-linux",
	}}
}

// update runs Update and returns its error and what it printed.
func (f *fixture) update(retry bool) (string, error) {
	f.out.Reset()
	err := f.u.Update(context.Background(), retry)
	return f.out.String(), err
}

func (f *fixture) mustUpdate(retry bool) string {
	f.t.Helper()
	out, err := f.update(retry)
	if err != nil {
		f.t.Fatalf("Update: %v\n%s", err, out)
	}
	return out
}

func (f *fixture) state() State {
	f.t.Helper()
	st, err := Load(f.u.Dir)
	if err != nil {
		f.t.Fatal(err)
	}
	return st
}

// handInstalled puts a valley in the profile the way a person would, with
// the version stamp in its store path's name.
func (f *fixture) handInstalled(version string) string {
	f.t.Helper()
	path := filepath.Join(f.nix.store, "hand-valley-"+version)
	if err := os.MkdirAll(path, 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.nix.profile[Element] = []string{path}
	return path
}

func TestUpdateInstallsNewCommit(t *testing.T) {
	// The running valley's wrapper sets these; the smoke test must not
	// hand them to the build.
	t.Setenv("VALLEY_VERIFICATION_SCHEMA", "/nix/store/old-schema.cue")
	t.Setenv("VALLEY_VERSION", "0.1.0-1111111")
	f := newFixture(t)
	f.u.Version = "0.1.0-1111111"
	hand := f.handInstalled("0.1.0-1111111")
	f.nix.profile["klaus"] = []string{"/nix/store/klaus"}
	c1 := f.up.push("one")

	out := f.mustUpdate(false)
	for _, want := range []string{"main is at " + c1[:7] + " (installed: valley 0.1.0-1111111)", "smoke test passed: valley 0.1.0-" + c1[:7], "installed " + c1[:7]} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	st := f.state()
	if st.Current == nil || st.Current.Commit != c1 || st.Current.StorePath != f.nix.installed() {
		t.Errorf("current = %+v; want %s at %s", st.Current, c1, f.nix.installed())
	}
	if st.Previous == nil || st.Previous.StorePath != hand || st.Previous.Commit != "1111111" {
		t.Errorf("previous = %+v; want the hand-installed %s", st.Previous, hand)
	}
	if got := f.nix.profile["klaus"]; len(got) != 1 {
		t.Errorf("the klaus element was touched: %v", got)
	}
}

func TestUpdateUpToDate(t *testing.T) {
	f := newFixture(t)
	c1 := f.up.push("one")
	f.mustUpdate(false)

	if out := f.mustUpdate(false); out != "" {
		t.Errorf("an up-to-date run printed:\n%s", out)
	}
	f.u.Verbose = true
	if out := f.mustUpdate(false); !strings.Contains(out, "main is at "+c1[:7]+": valley is up to date") {
		t.Errorf("-v printed:\n%s", out)
	}
	if len(f.nix.builds) != 1 {
		t.Errorf("built %d times; want once", len(f.nix.builds))
	}

	// Before self-update has installed anything, the running valley's
	// version stamp says what is installed.
	g := newFixture(t)
	c := g.up.push("one")
	g.u.Version = "0.1.0-" + c[:7]
	if out := g.mustUpdate(false); out != "" || len(g.nix.builds) != 0 {
		t.Errorf("the running valley is %s, and Update built %v and printed:\n%s", c[:7], g.nix.builds, out)
	}
	// A dirty build's stamp names no commit, so it is never up to date.
	g.u.Version = "0.1.0-" + c[:7] + "-dirty"
	g.mustUpdate(false)
	if len(g.nix.builds) != 1 {
		t.Errorf("the running valley is dirty, and Update built %v", g.nix.builds)
	}
}

func TestFailedSmokeTestSkipped(t *testing.T) {
	for _, kind := range []string{"build", "version", "help", "checks"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			f.up.push("one")
			f.mustUpdate(false)
			good := f.nix.installed()

			c2 := f.up.push("two")
			f.nix.broken[c2] = kind
			out, err := f.update(false)
			if err == nil {
				t.Fatalf("a %s failure installed:\n%s", kind, out)
			}
			if !strings.Contains(out, c2[:7]+" failed; nothing installed") {
				t.Errorf("output:\n%s", out)
			}
			if kind != "build" && !strings.Contains(err.Error(), "smoke test") {
				t.Errorf("error = %v; want a smoke test failure", err)
			}
			if f.nix.installed() != good {
				t.Errorf("the profile holds %s; want %s still", f.nix.installed(), good)
			}
			st := f.state()
			if st.Failed == nil || st.Failed.Commit != c2 || st.Failed.Error == "" {
				t.Errorf("failed = %+v; want %s recorded", st.Failed, c2)
			}

			out, err = f.update(false)
			if err != nil || !strings.Contains(out, "skipping "+c2[:7]) {
				t.Errorf("the second run: %v\n%s", err, out)
			}
			if len(f.nix.builds) != 2 {
				t.Errorf("builds = %v; want the failed commit built once", f.nix.builds)
			}
		})
	}
}

func TestRetry(t *testing.T) {
	f := newFixture(t)
	c1 := f.up.push("one")
	f.nix.broken[c1] = "help"
	if _, err := f.update(false); err == nil {
		t.Fatal("a broken build installed")
	}

	// --retry builds a failed commit again, and it can fail again.
	if _, err := f.update(true); err == nil {
		t.Fatal("a broken build installed on retry")
	}
	delete(f.nix.broken, c1)
	f.mustUpdate(true)
	st := f.state()
	if st.Current == nil || st.Current.Commit != c1 || st.Failed != nil {
		t.Errorf("after a retry that passed: current %+v, failed %+v", st.Current, st.Failed)
	}

	// A failed commit is tried again, without --retry, once the branch
	// moves past it.
	c2 := f.up.push("two")
	f.nix.broken[c2] = "help"
	f.update(false)
	c3 := f.up.push("three")
	f.mustUpdate(false)
	if got := f.nix.builds; len(got) != 5 || got[4] != c3 {
		t.Errorf("builds = %v; want %s built last", got, c3)
	}
}

func TestRollback(t *testing.T) {
	f := newFixture(t)
	if err := f.u.Rollback(context.Background()); err == nil || !strings.Contains(err.Error(), "nothing to roll back to") {
		t.Errorf("Rollback with nothing installed = %v", err)
	}
	c1 := f.up.push("one")
	f.mustUpdate(false)
	first := f.nix.installed()
	c2 := f.up.push("two")
	f.mustUpdate(false)
	second := f.nix.installed()

	f.out.Reset()
	if err := f.u.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := f.state()
	if f.nix.installed() != first || st.Current.Commit != c1 || st.Previous.Commit != c2 {
		t.Errorf("after a rollback: profile %s, current %+v, previous %+v", f.nix.installed(), st.Current, st.Previous)
	}
	if !strings.Contains(f.out.String(), "rolled back to "+c1[:7]) || !strings.Contains(f.out.String(), "not pinned") {
		t.Errorf("output:\n%s", f.out)
	}

	// A second rollback returns to where the first started.
	if err := f.u.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := f.state(); f.nix.installed() != second || st.Current.Commit != c2 || st.Previous.Commit != c1 {
		t.Errorf("after a second rollback: profile %s, current %+v, previous %+v", f.nix.installed(), st.Current, st.Previous)
	}
}

func TestPin(t *testing.T) {
	f := newFixture(t)
	c1 := f.up.push("one")
	f.mustUpdate(false)
	if err := f.u.Pin(true); err != nil {
		t.Fatal(err)
	}
	f.up.push("two")
	out := f.mustUpdate(false)
	if !strings.Contains(out, "pinned at "+c1[:7]) || len(f.nix.builds) != 1 {
		t.Errorf("a pinned run built %v and printed:\n%s", f.nix.builds, out)
	}

	if err := f.u.Pin(false); err != nil {
		t.Fatal(err)
	}
	f.mustUpdate(false)
	if len(f.nix.builds) != 2 || f.state().Pinned {
		t.Errorf("an unpinned run built %v; state %+v", f.nix.builds, f.state())
	}
}

func TestReinstallsHandRemoved(t *testing.T) {
	f := newFixture(t)
	hand := f.handInstalled("0.1.0-1111111")
	c1 := f.up.push("one")
	f.mustUpdate(false)
	installed := f.nix.installed()

	delete(f.nix.profile, Element)
	out := f.mustUpdate(false)
	if !strings.Contains(out, "no longer "+c1[:7]+"; reinstalling it") {
		t.Errorf("output:\n%s", out)
	}
	st := f.state()
	if f.nix.installed() == "" || st.Current.Commit != c1 {
		t.Errorf("after the reinstall: profile %q, current %+v", f.nix.installed(), st.Current)
	}
	// The rollback target is still the valley the first install replaced.
	if st.Previous == nil || st.Previous.StorePath != hand {
		t.Errorf("previous = %+v; want %s, not %s", st.Previous, hand, installed)
	}
}

func TestVersionCommit(t *testing.T) {
	for v, want := range map[string]string{
		"0.1.0-e949dba":                          "e949dba",
		"abcdefghijklmnopq-valley-0.1.0-e949dba": "e949dba",
		"0.1.0-e949dba-dirty":                    "",
		"0.1.0-unknown":                          "",
		"":                                       "",
		"abcdefghijklmnopq-valley":               "",
	} {
		if got := versionCommit(v); got != want {
			t.Errorf("versionCommit(%q) = %q, want %q", v, got, want)
		}
	}
}

// TestErrorLine: a failure is reported by the line that says what went
// wrong, not by the last line of a usage text printed after it.
func TestErrorLine(t *testing.T) {
	for out, want := range map[string]string{
		"valley: unknown verb: --version\n\nvalley — S1 integrator verbs\n  help  show this help\n": "valley: unknown verb: --version",
		"warning: Git tree is dirty\nerror: builder failed\nmore\n":                                 "error: builder failed",
		"one\ntwo\n\n": "two",
		"":             "",
	} {
		if got := ErrorLine(out); got != want {
			t.Errorf("ErrorLine(%q) = %q, want %q", out, got, want)
		}
	}
}
