package selfupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"

	"the-valley/flow/internal/nix"
)

// acceptedRepos covers every form Validate accepts.
var acceptedRepos = []string{
	"https://github.com/maintainer/klaus",
	"https://github.com/maintainer/klaus.git",
	"ssh://git@github.com/maintainer/klaus",
	"ssh://git@[::1]:2222/srv/klaus.git",
	"git://example.com/klaus.git",
	"file:///srv/git/klaus.git",
	"/srv/git/klaus.git",
	"git@github.com:maintainer/klaus.git",
	"me@host.example:~/klaus.git",
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
		"-uhttps://example.com/klaus",
		"--upload-pack=touch /tmp/pwned",
		"/srv/git/klaus.git?rev=0000000",
		"/srv/git/klaus.git#packages.x86_64-linux.evil",
		"https://example.com/klaus?ref=evil",
		"/srv/git/my klaus.git",
		"/srv/git/klaus%20repo.git",
		"https://example.com/my%20klaus",
		"https://example.com/repo+name",
		"https://example.com/repo!",
		"https://example.com/repo$x",
		"https://example.com/repo'x",
		"https://example.com/repo(x",
		"https://example.com/repo,x",
		"https://example.com/repo;x",
		"https://example.com/repo=x",
		"/srv/git/klaus+1.git",
		"/srv/git/[klaus].git",
		"/srv/git/klaus.git\n",
		"http://example.com/klaus",
		"ftp://example.com/klaus",
		"file://relative/klaus",
		"ssh://-oProxyCommand=touch/klaus",
		"relative/klaus",
		"host:klaus",
	} {
		err := Validate(repo, "main")
		if err == nil {
			t.Errorf("Validate(%q) accepted it", repo)
			continue
		}
		if !strings.Contains(err.Error(), "self_update.repo") {
			t.Errorf("Validate(%q) = %v; want the error to name self_update.repo", repo, err)
		}
	}
}

// TestRepoURL: ls-remote and nix read a repo by the same URL, the one the
// flake ref carries.
func TestRepoURL(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for repo, want := range map[string]string{
		"/srv/git/klaus.git":                  "file:///srv/git/klaus.git",
		"file:///srv/git/klaus.git":           "file:///srv/git/klaus.git",
		"https://github.com/maintainer/klaus": "https://github.com/maintainer/klaus",
		"git@github.com:maintainer/klaus.git": "ssh://git@github.com/maintainer/klaus.git",
		"me@host.example:/srv/klaus.git":      "ssh://me@host.example/srv/klaus.git",
		"me@host.example:~/klaus.git":         "ssh://me@host.example/~/klaus.git",
	} {
		if got := RepoURL(repo); got != want {
			t.Errorf("RepoURL(%q) = %q, want %q", repo, got, want)
		}
		if got, ref := FlakeRef(repo, "main", sha, Klaus), "git+"+want+"?ref=refs/heads/main&rev="+sha+"#packages."; !strings.HasPrefix(got, ref) {
			t.Errorf("FlakeRef(%q) = %q, want prefix %q", repo, got, ref)
		}
	}
}

// TestFlakeRefSurvivesNix: the installed nix parses each accepted repo's
// flake ref and prints it back unchanged, so nix fetches the URL ls-remote
// read.
func TestFlakeRefSurvivesNix(t *testing.T) {
	if _, err := exec.LookPath("nix"); err != nil {
		if os.Getenv("KLAUS_TEST_TOOLS") == "required" {
			t.Fatal("nix not installed")
		}
		t.Skip("nix not installed")
	}
	sha := strings.Repeat("a", 40)
	for _, repo := range acceptedRepos {
		ref, _, _ := strings.Cut(FlakeRef(repo, "main", sha, Klaus), "#")
		// Only stdout is the reference: nix warns on stderr, as it does
		// with no network.
		var stderr strings.Builder
		cmd := exec.Command("nix", "--extra-experimental-features", "nix-command flakes", "eval", "--raw", "--expr",
			`builtins.flakeRefToString (builtins.parseFlakeRef "`+ref+`")`)
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("nix eval of %q: %v\n%s", ref, err, stderr.String())
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

// fakeNix offers both packages of the client and builds either for any
// commit as a store path under store, holding bin/klaus and
// bin/valley-flow. Each executable answers --version as the program does
// under its name, and logs what it was run as to ran. The profile is a map
// from element to store paths: an added store path becomes the element
// nix would name after its package, and adding fails when the element is
// there already, or when the other client element is, whose files would
// collide. failAdds makes that many adds fail first.
type fakeNix struct {
	t        *testing.T
	store    string
	profile  map[string][]string
	asked    []string // the packages HasPackage was asked for
	builds   []string // the installables built
	ops      []string // "remove <element>" and "add <store path>", in order
	failAdds int
}

func newFakeNix(t *testing.T) *fakeNix {
	return &fakeNix{t: t, store: t.TempDir(), profile: map[string][]string{}}
}

func (f *fakeNix) HasPackage(_ context.Context, flake, name string) (bool, error) {
	f.asked = append(f.asked, name)
	return slices.Contains(names, name), nil
}

func (f *fakeNix) Build(_ context.Context, ref string) (string, error) {
	f.builds = append(f.builds, ref)
	_, rest, _ := strings.Cut(ref, "&rev=")
	sha, installable, _ := strings.Cut(rest, "#")
	attr := strings.TrimPrefix(installable, "packages."+nix.System()+".")
	out := filepath.Join(f.store, fmt.Sprintf("%d-%s-0.1.0-%s", len(f.builds), attr, sha[:7]))
	for exe, program := range map[string]string{Klaus: "klaus", ValleyFlow: "valley flow"} {
		script := fmt.Sprintf(`#!/bin/sh
echo "%s $1" >> %q
case "$1" in
--version) echo "%s version 0.1.0-%s" ;;
status) echo "No runs found." ;;
*) exit 2 ;;
esac
`, exe, f.ranLog(), program, sha[:7])
		if err := os.MkdirAll(filepath.Join(out, "bin"), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(out, "bin", exe), []byte(script), 0o755); err != nil {
			return "", err
		}
	}
	return out, nil
}

func (f *fakeNix) ranLog() string { return filepath.Join(f.store, "ran") }

// ran is what the builds' executables were run as, in order.
func (f *fakeNix) ran() []string {
	data, _ := os.ReadFile(f.ranLog())
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func (f *fakeNix) ProfileElements(context.Context) (map[string][]string, error) {
	return maps.Clone(f.profile), nil
}

func (f *fakeNix) ProfileRemove(_ context.Context, name string) error {
	f.ops = append(f.ops, "remove "+name)
	delete(f.profile, name)
	return nil
}

func (f *fakeNix) ProfileAdd(_ context.Context, storePath string) error {
	f.ops = append(f.ops, "add "+storePath)
	if f.failAdds > 0 {
		f.failAdds--
		return errors.New("nix profile add: exit status 1: error: cannot write the profile")
	}
	name := packageName(storePath)
	for _, held := range names {
		if _, ok := f.profile[held]; ok && (held == name || slices.Contains(names, name)) {
			return fmt.Errorf("nix profile add: exit status 1: error: %s is already provided by element %s", name, held)
		}
	}
	f.profile[name] = []string{storePath}
	return nil
}

// packageName is the name nix gives the element a store path installs as:
// the path's name less the hash before it and the version after it, which
// starts at the first dash followed by something other than a letter.
func packageName(storePath string) string {
	_, name, _ := strings.Cut(filepath.Base(storePath), "-")
	parts := strings.Split(name, "-")
	for i, p := range parts {
		if i > 0 && p != "" && !unicode.IsLetter(rune(p[0])) {
			return strings.Join(parts[:i], "-")
		}
	}
	return name
}

// handInstalled puts a build of package name at commit short into the
// profile the way a person would, as the element of that name.
func (f *fakeNix) handInstalled(name, short string) string {
	f.t.Helper()
	path := filepath.Join(f.store, "hand-"+name+"-0.1.0-"+short)
	if err := os.MkdirAll(path, 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.profile[name] = []string{path}
	return path
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
	up, fake, out := newUpstream(t), newFakeNix(t), &bytes.Buffer{}
	return &fixture{t: t, up: up, nix: fake, out: out, u: &Updater{
		Repo: up.bare, Branch: "main", Dir: t.TempDir(), Out: out, nix: fake,
	}}
}

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

func (f *fixture) mustRollback() string {
	f.t.Helper()
	f.out.Reset()
	if err := f.u.Rollback(context.Background()); err != nil {
		f.t.Fatalf("Rollback: %v\n%s", err, f.out.String())
	}
	return f.out.String()
}

func (f *fixture) state() State {
	f.t.Helper()
	st, err := Load(f.u.Dir)
	if err != nil {
		f.t.Fatal(err)
	}
	return st
}

func (f *fixture) stateFile() []byte {
	data, _ := os.ReadFile(StatePath(f.u.Dir))
	return data
}

// wantProfile fails unless the profile holds exactly the element name,
// holding exactly storePath, besides elements other than the client's.
func (f *fixture) wantProfile(name, storePath string) {
	f.t.Helper()
	want := map[string][]string{name: {storePath}}
	got := map[string][]string{}
	for _, n := range names {
		if p, ok := f.nix.profile[n]; ok {
			got[n] = p
		}
	}
	if !maps.EqualFunc(got, want, slices.Equal) {
		f.t.Errorf("profile's client elements = %v, want %v", got, want)
	}
}

// wantInstall fails unless inst records commit, storePath and the element
// and attribute name.
func wantInstall(t *testing.T, what string, inst *Install, commit, storePath, element, attribute string) {
	t.Helper()
	if inst == nil {
		t.Errorf("%s = nil, want %s in %s", what, shortSHA(commit), element)
		return
	}
	if inst.Commit != commit || inst.StorePath != storePath || inst.Element != element || inst.Attribute != attribute {
		t.Errorf("%s = %+v, want commit %s, store path %s, element %s, attribute %s",
			what, *inst, commit, storePath, element, attribute)
	}
}

// TestUpdateReplacesTheClientUnderEitherName: Update builds the attribute
// it is given, klaus unless set, smoke-tests the executable of that name,
// and puts the build in place of whichever client element the profile
// holds, recording both. Rollback puts back exactly the store path it
// replaced, as the element it was, and a second rollback returns.
func TestUpdateReplacesTheClientUnderEitherName(t *testing.T) {
	for _, c := range []struct {
		installed, attribute string
	}{
		{Klaus, ""},
		{ValleyFlow, ""},
		{Klaus, ValleyFlow},
		{ValleyFlow, ValleyFlow},
	} {
		t.Run(c.installed+" built as "+cmpOr(c.attribute, "default"), func(t *testing.T) {
			f := newFixture(t)
			f.u.Attribute = c.attribute
			attr := cmpOr(c.attribute, Klaus)
			old := f.nix.handInstalled(c.installed, "1111111")
			sha := f.up.push("first")

			out := f.mustUpdate(false)
			if !slices.Equal(f.nix.asked, []string{attr}) {
				t.Errorf("asked the flake for %v, want %s", f.nix.asked, attr)
			}
			if len(f.nix.builds) != 1 || !strings.HasSuffix(f.nix.builds[0], "&rev="+sha+"#packages."+nix.System()+"."+attr) {
				t.Errorf("built %v, want packages.%s.%s at %s", f.nix.builds, nix.System(), attr, sha[:7])
			}
			if ran := f.nix.ran(); !slices.Equal(ran, []string{attr + " --version", attr + " status"}) {
				t.Errorf("the smoke test ran %v, want bin/%s", ran, attr)
			}
			if !strings.Contains(out, "; "+attr+" status exits 0") {
				t.Errorf("Update printed:\n%s", out)
			}
			built := f.nix.store + fmt.Sprintf("/1-%s-0.1.0-%s", attr, sha[:7])
			f.wantProfile(attr, built)
			if want := []string{"remove " + c.installed, "add " + built}; !slices.Equal(f.nix.ops, want) {
				t.Errorf("profile operations %v, want %v", f.nix.ops, want)
			}
			st := f.state()
			wantInstall(t, "current", st.Current, sha, built, attr, attr)
			wantInstall(t, "previous", st.Previous, "1111111", old, c.installed, c.installed)

			f.mustRollback()
			f.wantProfile(c.installed, old)
			st = f.state()
			wantInstall(t, "current after rollback", st.Current, "1111111", old, c.installed, c.installed)
			wantInstall(t, "previous after rollback", st.Previous, sha, built, attr, attr)

			f.mustRollback()
			f.wantProfile(attr, built)
			st = f.state()
			wantInstall(t, "current after a second rollback", st.Current, sha, built, attr, attr)
			wantInstall(t, "previous after a second rollback", st.Previous, "1111111", old, c.installed, c.installed)
		})
	}
}

func cmpOr(s, or string) string {
	if s == "" {
		return or
	}
	return s
}

// TestEveryOperationRefusesAProfileHoldingBothNames: with klaus and
// valley-flow both in the profile, every operation refuses before anything
// else, whatever the state would have it do: Update with the tip
// installed, moved, or running with nothing recorded, pinned, or held as
// failed with and without --retry; --pin; --unpin; and --rollback with a
// build to roll back to and without. None reads the flake, builds, or
// changes the profile or the state.
func TestEveryOperationRefusesAProfileHoldingBothNames(t *testing.T) {
	update := func(retry bool) func(*fixture) error {
		return func(f *fixture) error { _, err := f.update(retry); return err }
	}
	pin := func(pinned bool) func(*fixture) error {
		return func(f *fixture) error { return f.u.Pin(context.Background(), pinned) }
	}
	rollback := func(f *fixture) error { return f.u.Rollback(context.Background()) }
	// tip is the branch's tip, held by the profile's klaus element, and
	// other an older build, still in the store.
	for _, c := range []struct {
		name  string
		state func(tip, other *Install) State
		op    func(*fixture) error
	}{
		{"update with the tip installed", func(tip, _ *Install) State { return State{Current: tip} }, update(false)},
		{"update with the tip moved", func(_, other *Install) State { return State{Current: other} }, update(false)},
		{"update running the tip with nothing recorded", func(_, _ *Install) State { return State{} }, update(false)},
		{"update while pinned", func(_, other *Install) State { return State{Current: other, Pinned: true} }, update(false)},
		{"update with the tip held as failed", func(tip, other *Install) State {
			return State{Current: other, Failed: &Failure{Commit: tip.Commit, Error: "smoke test"}}
		}, update(false)},
		{"update --retry with the tip held as failed", func(tip, other *Install) State {
			return State{Current: other, Failed: &Failure{Commit: tip.Commit, Error: "smoke test"}}
		}, update(true)},
		{"pin", func(tip, _ *Install) State { return State{Current: tip} }, pin(true)},
		{"unpin", func(tip, _ *Install) State { return State{Current: tip, Pinned: true} }, pin(false)},
		{"rollback", func(tip, other *Install) State { return State{Current: tip, Previous: other} }, rollback},
		{"rollback with nothing to roll back to", func(tip, _ *Install) State { return State{Current: tip} }, rollback},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			sha := f.up.push("tip")
			f.u.Version = "0.1.0-" + sha[:7] // the running program is the tip's build
			tip := &Install{Commit: sha, StorePath: f.nix.handInstalled(Klaus, sha[:7]), Element: Klaus, Attribute: Klaus}
			f.nix.handInstalled(ValleyFlow, "2222222")
			other := &Install{Commit: "1111111", StorePath: filepath.Join(f.nix.store, "old-klaus-0.1.0-1111111"), Element: Klaus, Attribute: Klaus}
			if err := os.MkdirAll(other.StorePath, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := f.u.save(c.state(tip, other)); err != nil {
				t.Fatal(err)
			}
			profile, state := maps.Clone(f.nix.profile), f.stateFile()

			f.out.Reset()
			if err := c.op(f); !errors.Is(err, errBothInstalled) {
				t.Errorf("%s = %v, want the refusal of both elements\n%s", c.name, err, f.out.String())
			}
			if len(f.nix.asked) != 0 || len(f.nix.builds) != 0 || len(f.nix.ops) != 0 || !maps.EqualFunc(f.nix.profile, profile, slices.Equal) {
				t.Errorf("the refusal read the flake for %v, built %v or changed the profile: %v", f.nix.asked, f.nix.builds, f.nix.ops)
			}
			if !bytes.Equal(f.stateFile(), state) {
				t.Errorf("the refusal changed the state:\nbefore %s\nafter  %s", state, f.stateFile())
			}
		})
	}
}

// TestUpdateRestoresTheElementWhenAddingFails: when the new build does not
// go into the profile, the element that held the client gets its store
// path back, and the state records the failure and keeps what is
// installed. A later run holds that commit, as for a failed smoke test,
// and --retry installs it, keeping the restored build as the rollback
// target.
func TestUpdateRestoresTheElementWhenAddingFails(t *testing.T) {
	f := newFixture(t)
	first := f.up.push("first")
	f.mustUpdate(false)
	installed := f.state().Current.StorePath

	second := f.up.push("second")
	f.nix.failAdds = 1
	out, err := f.update(false)
	if err == nil || !strings.Contains(err.Error(), "cannot write the profile") {
		t.Fatalf("Update = %v, want the add's failure\n%s", err, out)
	}
	if !strings.Contains(out, second[:7]+" failed; nothing installed") {
		t.Errorf("Update printed:\n%s", out)
	}
	built := f.nix.store + "/2-klaus-0.1.0-" + second[:7]
	if want := []string{"add " + installed, "remove klaus", "add " + built, "add " + installed}; !slices.Equal(f.nix.ops, want) {
		t.Errorf("profile operations %v, want %v", f.nix.ops, want)
	}
	f.wantProfile(Klaus, installed)
	st := f.state()
	wantInstall(t, "current", st.Current, first, installed, Klaus, Klaus)
	if st.Previous != nil {
		t.Errorf("previous = %+v, want none", *st.Previous)
	}
	if st.Failed == nil || st.Failed.Commit != second || !strings.Contains(st.Failed.Error, "cannot write the profile") {
		t.Errorf("failed = %+v, want %s and the add's error", st.Failed, second[:7])
	}

	ops := len(f.nix.ops)
	if out := f.mustUpdate(false); !strings.Contains(out, "skipping "+second[:7]+": it failed at") {
		t.Errorf("the run after the failure printed:\n%s", out)
	}
	if len(f.nix.builds) != 2 || len(f.nix.ops) != ops {
		t.Errorf("the run after the failure built %v or changed the profile: %v", f.nix.builds, f.nix.ops[ops:])
	}

	f.mustUpdate(true)
	built = f.nix.store + "/3-klaus-0.1.0-" + second[:7]
	f.wantProfile(Klaus, built)
	st = f.state()
	wantInstall(t, "current after --retry", st.Current, second, built, Klaus, Klaus)
	wantInstall(t, "previous after --retry", st.Previous, first, installed, Klaus, Klaus)
	if st.Failed != nil {
		t.Errorf("failed after --retry = %+v, want none", *st.Failed)
	}
}

// TestARetryThatLosesTheClientRecordsNone: a commit held after its add
// failed is retried, and adding it and putting the element's store path
// back both fail. The state records no install and no failure, keeping the
// lost build as the rollback target, and the error says the profile holds
// no client and how to add that build back. The next ordinary run then
// installs the tip, though the running program reports that commit.
func TestARetryThatLosesTheClientRecordsNone(t *testing.T) {
	f := newFixture(t)
	first := f.up.push("first")
	f.mustUpdate(false)
	installed := f.state().Current.StorePath

	second := f.up.push("second")
	f.nix.failAdds = 1
	if out, err := f.update(false); err == nil {
		t.Fatalf("Update with a failing add succeeded\n%s", out)
	}
	if st := f.state(); st.Failed == nil || st.Failed.Commit != second {
		t.Fatalf("failed = %+v, want %s held", st.Failed, second[:7])
	}

	f.nix.failAdds = 2
	out, err := f.update(true)
	if err == nil || !strings.Contains(err.Error(), "holds no flow") || !strings.Contains(err.Error(), "nix profile add "+installed) {
		t.Fatalf("Update --retry = %v, want what is left and the command that adds %s back\n%s", err, installed, out)
	}
	if len(f.nix.profile) != 0 {
		t.Errorf("profile = %v, want no client: both adds failed", f.nix.profile)
	}
	st := f.state()
	if st.Current != nil || st.Failed != nil {
		t.Errorf("current = %+v, failed = %+v, want neither: the profile holds no client", st.Current, st.Failed)
	}
	wantInstall(t, "previous", st.Previous, first, installed, Klaus, Klaus)

	f.u.Version = "0.1.0-" + second[:7]
	f.mustUpdate(false)
	built := f.nix.store + "/4-klaus-0.1.0-" + second[:7]
	f.wantProfile(Klaus, built)
	st = f.state()
	wantInstall(t, "current", st.Current, second, built, Klaus, Klaus)
	wantInstall(t, "previous", st.Previous, first, installed, Klaus, Klaus)
}

// TestStateAcrossUpdaters: a state file an updater that knows klaus alone
// wrote reads as klaus's, and one this updater writes keeps the keys that
// updater reads.
func TestStateAcrossUpdaters(t *testing.T) {
	dir := t.TempDir()
	older := `{
  "current": {"commit": "` + strings.Repeat("a", 40) + `", "store_path": "/nix/store/a-klaus-0.1.0-aaaaaaa", "at": "2026-01-02T03:04:05Z"},
  "previous": {"commit": "bbbbbbb", "store_path": "/nix/store/b-klaus-0.1.0-bbbbbbb", "at": "2026-01-01T03:04:05Z"},
  "updated_at": "2026-01-02T03:04:05Z"
}
`
	if err := os.WriteFile(StatePath(dir), []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	wantInstall(t, "current", st.Current, strings.Repeat("a", 40), "/nix/store/a-klaus-0.1.0-aaaaaaa", Klaus, Klaus)
	wantInstall(t, "previous", st.Previous, "bbbbbbb", "/nix/store/b-klaus-0.1.0-bbbbbbb", Klaus, Klaus)

	st.Current.Element, st.Current.Attribute = ValleyFlow, ValleyFlow
	u := &Updater{Dir: dir}
	if err := u.save(st); err != nil {
		t.Fatal(err)
	}
	var read struct {
		Current, Previous struct {
			Commit    string `json:"commit"`
			StorePath string `json:"store_path"`
		}
	}
	data, _ := os.ReadFile(StatePath(dir))
	if err := json.Unmarshal(data, &read); err != nil {
		t.Fatal(err)
	}
	if read.Current.Commit != st.Current.Commit || read.Current.StorePath != st.Current.StorePath ||
		read.Previous.Commit != st.Previous.Commit || read.Previous.StorePath != st.Previous.StorePath {
		t.Errorf("an older updater reads %+v from:\n%s", read, data)
	}
}
