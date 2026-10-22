package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// These tests run attest against the machine's real nix: they evaluate and
// build flake checks, so they need a nix daemon to talk to, and skip where
// there is none, as inside a nix build. attest itself is this test binary,
// run again with ATTEST_AS_MAIN set, so what is run is the program's own
// main with real standard output and error.

func TestMain(m *testing.M) {
	if os.Getenv("ATTEST_AS_MAIN") != "" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// nixFixture is a scratch repository whose flake defines the checks it is
// given, and a bare repository to publish to. Each check is a nix
// expression in which `check NAME SCRIPT` is a derivation that runs SCRIPT
// with /bin/sh, from a file in the store, so the derivation has an input. The flake has no inputs, so nothing is fetched, and every
// derivation carries a nonce, so nothing a test builds is in the store
// before it runs.
type nixFixture struct {
	repo   string
	origin string
	state  string
	key    string
	name   string
	env    []string
}

func newNixFixture(t *testing.T, checks map[string]string) nixFixture {
	t.Helper()
	for _, tool := range []string{"nix", "nix-store", "git", "cue", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not available: %v", tool, err)
		}
	}
	// A client may send max-jobs to the daemon, so the side-by-side test
	// does not depend on how this machine is configured.
	nixConfig := os.Getenv("NIX_CONFIG") + "\nextra-experimental-features = nix-command flakes\nmax-jobs = 4\n"
	if out, err := exec.Command("env", "NIX_CONFIG="+nixConfig, "nix", "store", "info").CombinedOutput(); err != nil {
		t.Skipf("no nix store to build in: %v: %s", err, out)
	}
	t.Setenv("NIX_CONFIG", nixConfig)

	dir := t.TempDir()
	f := nixFixture{
		repo:   filepath.Join(dir, "repo"),
		origin: filepath.Join(dir, "origin.git"),
		state:  filepath.Join(dir, "state"),
		key:    filepath.Join(dir, "key"),
		name:   "test.example/attestations",
	}
	f.env = append(os.Environ(),
		"ATTEST_AS_MAIN=1",
		"XDG_STATE_HOME="+f.state,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@l",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@l")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "t", "-f", f.key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}

	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(checks))
	for name := range checks {
		names = append(names, name)
	}
	sort.Strings(names)
	var defs strings.Builder
	for _, name := range names {
		fmt.Fprintf(&defs, "        %s = %s;\n", name, checks[name])
	}
	flake := fmt.Sprintf(`{
  outputs = { self }:
    let
      system = "%s";
      check = name: script: derivation {
        inherit name system;
        builder = "/bin/sh";
        args = [ (builtins.toFile "${name}.sh" script) ];
        nonce = "%s";
      };
    in {
      checks.${system} = {
%s      };
    };
}
`, nixSystem(), hex.EncodeToString(nonce), defs.String())
	if err := os.MkdirAll(f.repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "flake.nix"), []byte(flake), 0o644); err != nil {
		t.Fatal(err)
	}
	f.git(t, "", "init", "--quiet", "-b", "main", f.repo)
	f.git(t, f.repo, "add", "flake.nix")
	f.git(t, f.repo, "commit", "--quiet", "-m", "checks")
	f.git(t, "", "init", "--quiet", "--bare", f.origin)
	return f
}

func (f nixFixture) git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// attest runs `attest run` over the fixture's HEAD, publishing to its
// bare repository, with the flags given.
func (f nixFixture) attest(t *testing.T, flags ...string) (stdout, stderr string, err error) {
	t.Helper()
	schema, err := filepath.Abs(filepath.Join("..", "schema", "attestation.cue"))
	if err != nil {
		t.Fatal(err)
	}
	args := append([]string{"run", "--repo", f.repo, "--key", f.key, "--name", f.name,
		"--schema", schema, "--push", f.origin, "--branch", "main"}, flags...)
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = f.env
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	return out.String(), errb.String(), err
}

// published is every ref under refs/the-valley/ in the fixture's bare
// repository and in the repository itself.
func (f nixFixture) published(t *testing.T) string {
	t.Helper()
	return f.git(t, f.origin, "for-each-ref", "refs/the-valley/") + f.git(t, f.repo, "for-each-ref", "refs/the-valley/")
}

// sequentialOutcome is what the sequential run made of one pure check that
// passes: an evaluation of the check alone, then a nix build of its
// derivation alone, whose printed output path is digested. A batch must
// decide each check as this did.
func sequentialOutcome(t *testing.T, tree string, spec checkSpec) checkOutcome {
	t.Helper()
	c, err := evalClosure(tree, nixSystem(), spec)
	if err != nil {
		t.Fatal(err)
	}
	built, errb, err := nix(tree, "build", "--no-link", "--print-out-paths", c.drv+"^*")
	if err != nil {
		t.Fatalf("building %s alone: %v: %s", spec.name, err, errb)
	}
	paths := strings.Fields(built)
	if len(paths) != 1 {
		t.Fatalf("%s built %d outputs", spec.name, len(paths))
	}
	entries, err := fsEntries(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	return checkOutcome{spec: spec, passed: true, derivation: c.derivation, inputs: c.inputs, output: treeDigest(entries)}
}

// checkLine is the line run prints for a pure check with a result.
func checkLine(name, result string) string {
	return fmt.Sprintf("check   %-24s %-8s %s\n", name, "nix", result)
}

func statementText(t *testing.T, subj subject, out checkOutcome) string {
	t.Helper()
	doc, err := json.Marshal(composeStatement(subj, out, "local", nil))
	if err != nil {
		t.Fatal(err)
	}
	text, err := renderJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// Three checks built together, one of which fails. The two that pass are
// recorded exactly as the sequential run recorded them, the one that fails
// keeps its own log, and nothing is published. The same two checks without
// the failing one publish statements a sequential run would have signed.
func TestABatchDecidesEachCheckAlone(t *testing.T) {
	f := newNixFixture(t, map[string]string{
		"one":    `check "one" "echo one > $out"`,
		"two":    `check "two" "echo two > $out"`,
		"broken": `check "broken" "echo the broken check says why; exit 3"`,
	})
	stdout, stderr, err := f.attest(t, "--check", "one", "--check", "broken", "--check", "two", "--timings")
	if err == nil {
		t.Fatalf("a run with a failing check succeeded:\n%s%s", stdout, stderr)
	}
	for _, want := range []string{checkLine("one", "passed"), checkLine("broken", "failed"), checkLine("two", "passed")} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the run did not report %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "\n--- broken ---\nthe broken check says why\n") {
		t.Errorf("the failing check's log was lost:\n%s", stderr)
	}
	if !strings.Contains(stderr, "1 of 3 checks failed; nothing published") {
		t.Errorf("the run did not say that nothing was published:\n%s", stderr)
	}
	if refs := f.published(t); refs != "" {
		t.Errorf("a run with a failing check published:\n%s", refs)
	}

	// Each check is logged on its own line, with the fields it always had
	// and its evaluation time. The two that were built share one duration:
	// the build's.
	data, err := os.ReadFile(filepath.Join(f.state, "valley", "timings.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("the timing log holds %d lines, want 3:\n%s", len(lines), data)
	}
	results := map[string]string{}
	durations := map[int64]bool{}
	for _, line := range lines {
		var timing checkTiming
		if err := json.Unmarshal([]byte(line), &timing); err != nil {
			t.Fatal(err)
		}
		results[timing.Check] = timing.Result
		durations[timing.DurationMS] = true
		if timing.EvalMS <= 0 || timing.EvalMS > timing.DurationMS || timing.Satisfied != satisfiedRan {
			t.Errorf("a pure check's timing reads %s", line)
		}
	}
	if results["one"] != "passed" || results["two"] != "passed" || results["broken"] != "failed" {
		t.Errorf("the timing log records the results %v", results)
	}
	if len(durations) != 1 {
		t.Errorf("checks built together logged different durations: %s", data)
	}

	// The two that passed, decided in the batch with the failing one, are
	// the outcomes the sequential run made of them.
	tree := t.TempDir()
	if err := exportTree(f.repo, "HEAD", tree); err != nil {
		t.Fatal(err)
	}
	subj, err := composeSubject(f.repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	specs := []checkSpec{{name: "one", attribute: "one"}, {name: "broken", attribute: "broken"}, {name: "two", attribute: "two"}}
	runs := runPure(tree, nixSystem(), specs, false)
	for i, r := range runs {
		if r.err != nil {
			t.Fatalf("%s: %v", specs[i].name, r.err)
		}
	}
	if runs[1].out.passed || !strings.Contains(runs[1].out.log, "the broken check says why") {
		t.Errorf("the failing check passed, or lost its log: %+v", runs[1].out)
	}
	sequential := map[string]string{}
	for _, i := range []int{0, 2} {
		want := statementText(t, subj, sequentialOutcome(t, tree, specs[i]))
		if got := statementText(t, subj, runs[i].out); got != want {
			t.Errorf("%s in a batch:\n%s\nalone:\n%s", specs[i].name, got, want)
		}
		sequential[specs[i].name] = want
	}

	// Without the failing check, the run publishes, and what it signed is
	// what the sequential run would have.
	stdout, stderr, err = f.attest(t, "--check", "one", "--check", "two")
	if err != nil {
		t.Fatalf("a run whose checks pass failed: %v\n%s%s", err, stdout, stderr)
	}
	host, err := loadSigner(f.key, f.name)
	if err != nil {
		t.Fatal(err)
	}
	ref := attestationRef(subj.Digest[subj.Primary], fmt.Sprintf("%08x", host.hash))
	for name, want := range sequential {
		note := f.git(t, f.origin, "cat-file", "blob", ref+":"+name+"/statement.note") + "\n"
		text, _, err := openNote([]byte(note), []verifierKey{host.verifierKey()})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(text) != want {
			t.Errorf("%s was published as:\n%s\nthe sequential run signs:\n%s", name, text, want)
		}
	}
}

// Two slow checks build side by side: the run takes about as long as one
// of them, not as long as both.
func TestABatchBuildsChecksSideBySide(t *testing.T) {
	// The sandbox's /bin/sh may have no sleep, so each check waits on a
	// read that never ends a line and times out.
	const sleep = 5 * time.Second
	script := fmt.Sprintf("read -t %d line < /dev/zero; echo done > $out", int(sleep.Seconds()))
	f := newNixFixture(t, map[string]string{
		"slow-one": fmt.Sprintf("check %q %q", "slow-one", script),
		"slow-two": fmt.Sprintf("check %q %q", "slow-two", script),
	})
	began := time.Now()
	stdout, stderr, err := f.attest(t, "--check", "slow-one", "--check", "slow-two")
	took := time.Since(began)
	if err != nil {
		t.Fatalf("the slow checks did not pass: %v\n%s%s", err, stdout, stderr)
	}
	if took < sleep || took >= 2*sleep-time.Second {
		t.Errorf("two %v checks took %v together", sleep, took)
	}
}

// A check that does not evaluate is an error for that check alone: every
// other check is still built and reported, and nothing is published. An
// error nix can catch leaves the shared evaluation standing; one it
// cannot, such as a type error, ends it, and every check is evaluated
// alone.
func TestAnEvaluationErrorStopsNoOtherCheck(t *testing.T) {
	for name, broken := range map[string]string{
		"a throw":      `throw "the broken check does not evaluate"`,
		"a type error": `check "broken" (1 + "the broken check does not evaluate")`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newNixFixture(t, map[string]string{
				"fine":   `check "fine" "echo fine > $out"`,
				"broken": broken,
				"also":   `check "also" "echo also > $out"`,
			})
			stdout, stderr, err := f.attest(t, "--check", "fine", "--check", "broken", "--check", "also")
			if err == nil {
				t.Fatalf("a run with a check that does not evaluate succeeded:\n%s%s", stdout, stderr)
			}
			for _, want := range []string{
				checkLine("fine", "passed"),
				checkLine("also", "passed"),
				fmt.Sprintf("time    %-24s %-8s ", "broken", "error"),
			} {
				if !strings.Contains(stdout, want) {
					t.Errorf("the run did not report %q:\n%s", want, stdout)
				}
			}
			if !strings.Contains(stderr, "evaluating ") || !strings.Contains(stderr, "the broken check does not evaluate") {
				t.Errorf("the evaluation error is not reported:\n%s", stderr)
			}
			if refs := f.published(t); refs != "" {
				t.Errorf("a run with a check that does not evaluate published:\n%s", refs)
			}
		})
	}
}
