package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The timing log is a file other tools will read line by line, so its
// lines are pinned here: one JSON object per check, with exactly these
// fields, appended to whatever the file already holds.
func TestTimingLogAppendsOneLinePerCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "valley", "timings.jsonl")
	var warned bytes.Buffer
	log := &timingLog{path: path, warn: &warned}
	log.append(checkTiming{
		Time: "2026-10-20T12:00:00.000Z", Repository: "ssh://git@host/project.git",
		Change: "kqxmkqxmkqxmkqxmkqxmkqxmkqxmkqxm", Version: 2, Head: strings.Repeat("a", 40),
		Check: "prose-format", Runner: "nix", Cost: "quick", Result: "passed", Satisfied: satisfiedCached,
		DurationMS: 1234,
	})
	// A plain branch has no change ID or version.
	log.append(checkTiming{
		Time: "2026-10-20T12:00:01.234Z", Repository: "/srv/project", Head: strings.Repeat("b", 40),
		Check: "tree-ok", Runner: "command", Result: "failed", Satisfied: satisfiedRan,
	})
	if warned.Len() != 0 {
		t.Fatalf("a writable log warned: %s", warned.String())
	}
	// Only its owner can read it.
	for p, want := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		if info, err := os.Stat(p); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s was created %v, want %v", p, info.Mode().Perm(), want)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("the log holds %d lines, want 2:\n%s", len(lines), data)
	}
	want := [][]string{
		{"change", "check", "cost", "duration_ms", "head", "repository", "result", "runner", "satisfied", "time", "version"},
		{"check", "duration_ms", "head", "repository", "result", "runner", "satisfied", "time"},
	}
	for i, line := range lines {
		var fields map[string]any
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			t.Fatalf("line %d is not JSON: %v: %s", i+1, err, line)
		}
		var keys []string
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if !reflect.DeepEqual(keys, want[i]) {
			t.Errorf("line %d has the fields %v, want %v", i+1, keys, want[i])
		}
	}
	var first checkTiming
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.DurationMS != 1234 || first.Version != 2 || first.Satisfied != "cached" {
		t.Errorf("the first line reads back as %+v", first)
	}
}

// A log that cannot be written says so once and goes on: the run it
// belongs to is not failed by it, and is not made to repeat the warning
// for every check after the first.
func TestAnUnwritableTimingLogOnlyWarns(t *testing.T) {
	// A directory that is a regular file cannot be written under, whoever
	// runs the test.
	blocker := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(blocker, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var warned bytes.Buffer
	log := &timingLog{path: filepath.Join(blocker, "valley", "timings.jsonl"), warn: &warned}
	for _, check := range []string{"one", "two", "three"} {
		log.append(checkTiming{Check: check, Result: "passed"})
	}
	lines := strings.Split(strings.TrimSuffix(warned.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "attest: warning: timings not logged: ") {
		t.Fatalf("an unwritable log warned %q, want one warning line", warned.String())
	}
}

// The log is a regular file. Anything else at its path is refused at once,
// with one warning, and is written nothing. A FIFO nobody reads would hold
// the open forever, and one somebody reads would hand them the log.
func TestATimingLogThatIsNotARegularFileIsRefused(t *testing.T) {
	for _, reading := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "timings.jsonl")
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}
		var reader *os.File
		if reading {
			fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				t.Fatal(err)
			}
			reader = os.NewFile(uintptr(fd), path)
			defer reader.Close()
		}
		var warned bytes.Buffer
		log := &timingLog{path: path, warn: &warned}
		began := time.Now()
		for _, check := range []string{"one", "two"} {
			log.append(checkTiming{Check: check, Result: "passed"})
		}
		if took := time.Since(began); took >= timingDeadline {
			t.Errorf("a FIFO at the log's path (read: %v) held the run for %v", reading, took)
		}
		if lines := strings.Split(strings.TrimSuffix(warned.String(), "\n"), "\n"); len(lines) != 1 ||
			!strings.HasSuffix(lines[0], path+" is not a regular file") {
			t.Errorf("a FIFO at the log's path (read: %v) warned %q, want one warning", reading, warned.String())
		}
		if reading {
			if n, _ := reader.Read(make([]byte, 1)); n != 0 {
				t.Errorf("a FIFO at the log's path was written to")
			}
		}
	}
}

// Timing output to a standard stream nobody reads is dropped, and is never
// what ends a run or holds it up. Go ends a process whose write to its own
// standard output fails because nobody reads it, so each case runs in a
// child: this test again, with both standard streams one pipe. In one, the
// pipe's reader has gone. In the other, the pipe is full and its reader
// never reads.
func TestTimingOutputNobodyReadsIsDropped(t *testing.T) {
	const survived = 3
	if os.Getenv("ATTEST_TIMING_CHILD") != "" {
		fmt.Fprintln(timingStdout(), "time    one                      passed   1ms (ran)")
		fmt.Fprintln(timingStderr(), "attest: warning: timings not logged: a test")
		fmt.Fprintln(timingStdout(), "time    two                      passed   1ms (ran)")
		os.Exit(survived)
	}
	for _, full := range []bool{false, true} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		if full {
			// The write end is non-blocking until the child is given it,
			// so it fills until a write would wait.
			w.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
			for err == nil {
				_, err = w.Write(make([]byte, 1<<16))
			}
			w.SetWriteDeadline(time.Time{})
		} else {
			r.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTimingOutputNobodyReadsIsDropped$")
		cmd.Env = append(os.Environ(), "ATTEST_TIMING_CHILD=1")
		cmd.Stdout, cmd.Stderr = w, w
		began := time.Now()
		cmd.Run()
		took := time.Since(began)
		cancel()
		w.Close()
		r.Close()
		if code := cmd.ProcessState.ExitCode(); code != survived {
			t.Errorf("timing output to a pipe nobody reads (full: %v) ended the run: %v", full, cmd.ProcessState)
		}
		// Three writes: the first to each stream waits out its deadline,
		// and the second to stdout is dropped at once.
		if took > 2*timingDeadline+5*time.Second {
			t.Errorf("timing output to a pipe nobody reads (full: %v) held the run for %v", full, took)
		}
	}
}

// A run without --timings asks the nix store nothing beyond what its
// statements need. A run with it asks whether each output is already
// there; a store that does not answer in time makes the answer "unknown",
// and the check is built all the same. nix and nix-store are stand-ins
// that log what they are asked.
func TestOnlyATimedRunAsksTheStoreAboutOutputs(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	drv := filepath.Join(dir, "check.drv")
	built := filepath.Join(dir, "out")
	bin := filepath.Join(dir, "bin")
	for path, content := range map[string]string{
		drv:                            "Derive()",
		filepath.Join(built, "passed"): "",
		filepath.Join(bin, "nix"): `#!/bin/sh
echo "nix $*" >> "$STUB_CALLS"
case "$1" in
eval) echo "$STUB_DRV" ;;
build) echo "$STUB_OUT" ;;
esac
`,
		filepath.Join(bin, "nix-store"): `#!/bin/sh
echo "nix-store $*" >> "$STUB_CALLS"
case "$1 $2" in
"--query --references") echo /nix/store/00000000000000000000000000000000-input ;;
"--query --outputs") [ -z "$STUB_HANG" ] || exec sleep 30; echo "$STUB_OUT" ;;
esac
`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("STUB_CALLS", calls)
	t.Setenv("STUB_DRV", drv)
	t.Setenv("STUB_OUT", built)
	deadline := probeDeadline
	probeDeadline = 200 * time.Millisecond
	t.Cleanup(func() { probeDeadline = deadline })

	run := func(probe bool) (checkOutcome, string) {
		t.Helper()
		os.Remove(calls)
		out, err := runPure(dir, "x86_64-linux", checkSpec{name: "tree-ok", attribute: "tree-ok"}, probe)
		if err != nil {
			t.Fatal(err)
		}
		if !out.passed {
			t.Fatalf("the check did not pass: %s", out.log)
		}
		asked, err := os.ReadFile(calls)
		if err != nil {
			t.Fatal(err)
		}
		return out, string(asked)
	}
	probed := func(asked string) bool {
		return strings.Contains(asked, "--outputs") || strings.Contains(asked, "--check-validity")
	}

	if out, asked := run(false); probed(asked) || out.satisfied != "" {
		t.Errorf("a run without --timings asked about outputs (satisfied %q):\n%s", out.satisfied, asked)
	}
	if out, asked := run(true); !probed(asked) || out.satisfied != satisfiedCached {
		t.Errorf("a run with --timings did not find its output cached (satisfied %q):\n%s", out.satisfied, asked)
	}
	t.Setenv("STUB_HANG", "1")
	began := time.Now()
	if out, asked := run(true); !strings.Contains(asked, "nix build") || out.satisfied != satisfiedUnknown {
		t.Errorf("a store that does not answer left satisfied %q:\n%s", out.satisfied, asked)
	}
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("a store that does not answer held the check for %v", took)
	}
}

func TestTimingsPathFollowsXDGStateHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fallback := filepath.Join(home, ".local", "state", "valley", "timings.jsonl")
	for _, c := range []struct{ state, want string }{
		{"/var/lib/someone/state", "/var/lib/someone/state/valley/timings.jsonl"},
		{"", fallback},
		// The specification ignores a relative path.
		{"relative/state", fallback},
	} {
		t.Setenv("XDG_STATE_HOME", c.state)
		got, err := timingsPath()
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("XDG_STATE_HOME=%q: %s, want %s", c.state, got, c.want)
		}
	}
}

// A remote can carry a token, in its user name, its password, its query or
// its fragment, and the log is not the place for any of them. What names
// the repository is kept, and a remote that cannot be read as one of the
// forms git takes is withheld.
func TestTheRepositoryNameCarriesNoCredentials(t *testing.T) {
	for remote, want := range map[string]string{
		"https://someone:ghp_token@forge.example/project.git":                  "https://forge.example/project.git",
		"https://forge.example/project.git?access_token=SECRET":                "https://forge.example/project.git",
		"https://forge.example/project.git#SECRET":                             "https://forge.example/project.git",
		"https://someone:SECRET@forge.example/project.git?token=SECRET#SECRET": "https://forge.example/project.git",
		"ssh://git@valley.example:2222/project.git":                            "ssh://valley.example:2222/project.git",
		"file:///srv/git/project.git":                                          "file:///srv/git/project.git",
		"git@valley.example:project.git":                                       "valley.example:project.git",
		"/srv/git/project.git":                                                 "/srv/git/project.git",
		"../project.git":                                                       "../project.git",
		"someone:SECRET@valley.example:project.git":                            "",
		"valley.example:project.git?token=SECRET":                              "",
		"https://forge.example:SECRET/project.git":                             "",
		"persistent-https::https://someone:SECRET@forge.example/project.git":   "",
		"ext::ssh -i SECRET valley.example %S project.git":                     "",
	} {
		if got := remoteIdentifier(remote); got != want {
			t.Errorf("%s: %q, want %q", remote, got, want)
		}
	}

	// A withheld origin names the repository by its main working tree.
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet", repo},
		{"-C", repo, "remote", "add", "origin", "someone:SECRET@valley.example:project.git"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	want, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := repositoryName(repo); got != want {
		t.Errorf("a repository whose origin is withheld is named %q, want %q", got, want)
	}
}
