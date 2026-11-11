package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// elsewhere has the test run in a directory of its own that is in no
// repository, with a home of its own.
func elsewhere(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)
}

// command runs the valley command line args, elsewhere.
func command(t *testing.T, args ...string) (status int, stdout, stderr string) {
	t.Helper()
	elsewhere(t)
	return commandHere(t, args...)
}

// commandHere runs the valley command line args here.
func commandHere(t *testing.T, args ...string) (status int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	status = cli(args, &out, &errb, func(int) { t.Fatal("a verb ended the process") })
	return status, out.String(), errb.String()
}

// No verb is help. An unknown verb prints the usage on stderr and exits 1.
// Each public verb is its own: given an argument it refuses, it says so
// itself.
func TestDispatch(t *testing.T) {
	if status, out, errs := command(t); status != 0 || out != helpText("") || errs != "" {
		t.Errorf("no verb: %d %q %q", status, out, errs)
	}
	status, out, errs := command(t, "zx81q", "status")
	if want := "valley: unknown verb: zx81q\n\n" + helpPart("usage"); status != 1 || out != "" || errs != want {
		t.Errorf("an unknown verb: %d %q %q", status, out, errs)
	}
	refused := map[string][]string{"knot": {"zx81q"}, "telemetry": {"zx81q"}, "tail": {"a", "b"}, "replay": {"a", "b"}}
	for verb := range helpTopics {
		args, ok := refused[verb]
		if !ok {
			args = []string{"--zx81q"}
		}
		status, out, errs := command(t, append([]string{verb}, args...)...)
		if status == 0 || out != "" || errs == "" || strings.Contains(errs, "unknown verb: "+verb) {
			t.Errorf("valley %s %s: %d %q %q", verb, strings.Join(args, " "), status, out, errs)
		}
	}
}

// -h or --help, alone after a public verb, is its help topic. With
// another argument the verb reads them as its own.
func TestVerbHelp(t *testing.T) {
	for verb := range helpTopics {
		for _, flag := range []string{"-h", "--help"} {
			status, out, errs := command(t, verb, flag)
			if status != 0 || out != helpText(verb) || errs != "" {
				t.Errorf("valley %s %s: %d %q", verb, flag, status, errs)
			}
		}
		if status, out, _ := command(t, "help", verb); status != 0 || out != helpText(verb) {
			t.Errorf("valley help %s: %d", verb, status)
		}
	}
	for _, args := range [][]string{{"tail", "-h", "x"}, {"rebase", "--help", "x"}} {
		status, out, errs := command(t, args...)
		if status != 1 || out != "" || !strings.HasPrefix(errs, "valley: usage: valley "+args[0]+" ") {
			t.Errorf("valley %s: %d %q %q", strings.Join(args, " "), status, out, errs)
		}
	}
	for _, args := range [][]string{{"help", "a", "b"}, {"help", "zx81q"}, {"help", "resolve"}} {
		if status, out, errs := command(t, args...); status != 1 || out != "" || errs == "" {
			t.Errorf("valley %s: %d %q %q", strings.Join(args, " "), status, out, errs)
		}
	}
}

// --version prints the stamp, or says there is none.
func TestVersion(t *testing.T) {
	kept := versionStamp
	t.Cleanup(func() { versionStamp = kept })
	versionStamp = "0.1.0-97db597"
	if _, out, _ := command(t, "--version"); out != "valley 0.1.0-97db597\n" {
		t.Errorf("stamped: %q", out)
	}
	versionStamp = ""
	if _, out, _ := command(t, "--version"); !strings.HasPrefix(out, "valley unstamped (") {
		t.Errorf("unstamped: %q", out)
	}
}

// Every verb the usage lists has a help topic, and every help file is a
// public verb's topic or a part topics share.
func TestHelpTopics(t *testing.T) {
	listed := regexp.MustCompile(`(?m)^  ([a-z-]+) +[a-z]`).FindAllStringSubmatch(helpPart("usage"), -1)
	if len(listed) != len(helpTopics) {
		t.Errorf("the usage lists %d verbs, and %d have a topic", len(listed), len(helpTopics))
	}
	for _, m := range listed {
		if !publicVerb(m[1]) {
			t.Errorf("the usage lists %s, which has no topic", m[1])
		}
	}
	shared := []string{"usage", "naming", "requests", "runtime"}
	files, err := fs.Glob(helpFiles, "help/*")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".txt")
		if !publicVerb(name) && !slices.Contains(shared, name) || !strings.HasSuffix(f, ".txt") {
			t.Errorf("%s is no verb's topic and no shared part", f)
		}
	}
	for verb, parts := range helpTopics {
		if parts[0] != verb || !strings.HasPrefix(helpText(verb), "usage: valley "+verb+" ") {
			t.Errorf("%s's topic is not its own: %q", verb, parts)
		}
	}
}

// rebase takes one change and the signer flags, which it passes on in
// front of the change, and it refuses anything else before it fetches.
func TestRebaseArguments(t *testing.T) {
	got, err := rebaseCommandArgs([]string{"--known-signers", "f", "topic", "--integrator-name=i"})
	if want := []string{"--known-signers", "f", "--integrator-name=i", "topic"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("got %q %v, want %q", got, err, want)
	}
	for _, args := range [][]string{{}, {"a", "b"}, {"--head", "0123456", "a"}, {"a", "--known-signers"}, {"-", "a"}, {"--", "a"}, {""}} {
		if _, err := rebaseCommandArgs(args); err == nil || err.Error() != rebaseUsage {
			t.Errorf("rebase %q: %v", args, err)
		}
		// Outside a repository a fetch would fail with git's 128.
		status, out, errs := command(t, append([]string{"rebase"}, args...)...)
		if status != 1 || out != "" || errs != "valley: "+rebaseUsage+"\n" {
			t.Errorf("valley rebase %q: %d %q %q", args, status, out, errs)
		}
	}
}

// pending and rebase fetch origin first, and a fetch that fails ends them
// with git's own status.
func TestFailedFetchExitsWithGitsStatus(t *testing.T) {
	elsewhere(t)
	gitIn := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Env = gitEnvironment(os.Environ())
		return cmd.Run()
	}
	if err := gitIn("init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	if err := gitIn("remote", "add", "origin", filepath.Join(t.TempDir(), "gone.git")); err != nil {
		t.Fatal(err)
	}
	want := exitCode(gitIn("fetch", "--quiet", "origin"))
	if want == 0 {
		t.Fatal("a fetch of a missing origin succeeded")
	}
	for _, args := range [][]string{{"pending"}, {"rebase", "topic"}} {
		if status, out, errs := commandHere(t, args...); status != want || out != "" {
			t.Errorf("valley %s: %d, want git's %d: %s", strings.Join(args, " "), status, want, errs)
		}
	}
}

// replay publishes the payload the host's publisher hook does: a " in a
// refname escaped as the hook escapes it, and < > & as they are.
func TestReplayPayload(t *testing.T) {
	oid := strings.Repeat("a1", 20)
	got := refUpdated("app", `refs/heads/a"b<&>`, oid)
	want := `{"event":"ref-updated","repo":"app","ref":"refs/heads/a\"b<&>","old":"` + strings.Repeat("0", 40) + `","new":"` + oid + `"}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	var read struct{ Ref string }
	if err := json.Unmarshal([]byte(got), &read); err != nil || read.Ref != `refs/heads/a"b<&>` {
		t.Fatalf("the payload reads as %q: %v", read.Ref, err)
	}
}

// The stamp is taken out of the environment, so a program valley starts
// does not inherit it, and self-update and the command record still read
// it. git there ignores replacement refs.
func TestStampStaysOutOfStartedPrograms(t *testing.T) {
	kept := versionStamp
	t.Cleanup(func() { versionStamp = kept })
	t.Setenv("VALLEY_VERSION", "0.1.0-97db597")
	t.Setenv("GIT_NO_REPLACE_OBJECTS", "")
	t.Setenv("GIT_GRAFT_FILE", "/elsewhere")
	takeEnvironment()
	out, err := exec.Command("sh", "-c", `printf '%s %s %s' "${VALLEY_VERSION-unset}" "$GIT_NO_REPLACE_OBJECTS" "${GIT_GRAFT_FILE-unset}"`).Output()
	if err != nil || string(out) != "unset 1 unset" {
		t.Fatalf("a started program's environment: %q %v", out, err)
	}
	if inv := commandOf([]string{"status", "topic"}); inv == nil || inv.version != "0.1.0-97db597" {
		t.Errorf("the command record's version: %+v", inv)
	}
	u, err := newUpdater(selfUpdateArgs{repo: "/elsewhere.git", branch: "main"}, &bytes.Buffer{})
	if err != nil || u.Version != "0.1.0-97db597" {
		t.Errorf("self-update's version: %+v %v", u, err)
	}
}

// A public verb run as the command records it, from the process's start;
// help, a verb's help, --version, an unknown or internal verb and the
// background flusher record nothing.
func TestCommandOf(t *testing.T) {
	if inv := commandOf([]string{"pending", "--json"}); inv == nil || !inv.since.Equal(phases.began) {
		t.Errorf("pending: %+v", inv)
	}
	for _, args := range [][]string{{}, {"help", "pending"}, {"pending", "-h"}, {"review", "--help"}, {"--version"},
		{"zx81q"}, {"resolve", "topic"}, {"checkline", "x"}, {"style"}, {"work"}, {"telemetry", "flush", "--watch", "1"}} {
		if inv := commandOf(args); inv != nil {
			t.Errorf("%q records %+v", args, inv)
		}
	}
}
