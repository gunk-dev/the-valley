package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// flowRun is what the stand-in valley-flow (fakeFlow) saw.
type flowRun struct {
	Argv  []string          `json:"argv"`
	Stdin string            `json:"stdin"`
	Env   map[string]string `json:"env"`
}

// flowEnvNames are the variables fakeFlow reports, when set.
var flowEnvNames = []string{"VALLEY_TEST_CALLER", "VALLEY_VERSION", "GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE"}

// fakeFlow stands in for valley-flow when this test binary runs under that
// name (TestMain): it prints its argv, its stdin and the variables of
// flowEnvNames it was given, and exits with $VALLEY_TEST_FLOW_STATUS.
func fakeFlow() int {
	stdin, _ := io.ReadAll(os.Stdin)
	seen := flowRun{Argv: os.Args, Stdin: string(stdin), Env: map[string]string{}}
	for _, name := range flowEnvNames {
		if v, ok := os.LookupEnv(name); ok {
			seen.Env[name] = v
		}
	}
	json.NewEncoder(os.Stdout).Encode(seen)
	status, _ := strconv.Atoi(os.Getenv("VALLEY_TEST_FLOW_STATUS"))
	return status
}

// valleyAsMain runs this test binary as valley's main (TestMain), in a
// directory of its own outside any repository, with PATH holding bin
// alone unless extra sets it, and extra added to the environment, and
// returns its stdout, stderr and exit status.
func valleyAsMain(t *testing.T, bin string, stdin string, extra []string, args ...string) (string, string, int) {
	t.Helper()
	return valleyAsMainIn(t, t.TempDir(), bin, stdin, extra, args...)
}

// valleyAsMainIn is valleyAsMain in dir.
func valleyAsMainIn(t *testing.T, dir, bin string, stdin string, extra []string, args ...string) (string, string, int) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, args...)
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); !slices.Contains(flowEnvNames, name) {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "VALLEY_TEST_AS_MAIN=1", "PATH="+bin,
		"GIT_CEILING_DIRECTORIES="+filepath.Dir(dir), "HOME="+dir)
	cmd.Env = append(cmd.Env, extra...)
	// TestMain reads one byte before it runs main.
	cmd.Stdin = strings.NewReader("\x00" + stdin)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	status := 0
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
		status = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), status
}

// flowBin is a PATH directory whose valley-flow is this test binary.
func flowBin(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(self, filepath.Join(bin, flowHelper)); err != nil {
		t.Fatal(err)
	}
	return bin
}

// valley flow runs valley-flow from PATH, outside any checkout, with every
// argument after flow, help and version flags included, its stdin and the
// environment valley was started in less the stamp, and exits with its
// status.
func TestFlowRunsTheHelper(t *testing.T) {
	bin := flowBin(t)
	for _, args := range [][]string{
		{},
		{"--help"},
		{"-h"},
		{"--version"},
		{"launch", "--help"},
		{"launch", "--repo", "app", "--", "fix the build", "-h"},
	} {
		out, errs, status := valleyAsMain(t, bin, "", nil, append([]string{"flow"}, args...)...)
		var seen flowRun
		if err := json.Unmarshal([]byte(out), &seen); err != nil || status != 0 || errs != "" {
			t.Fatalf("valley flow %v: exit %d, stdout %q, stderr %q", args, status, out, errs)
		}
		if want := append([]string{flowHelper}, args...); !slices.Equal(seen.Argv, want) {
			t.Errorf("valley flow %v: the helper's argv is %q, want %q", args, seen.Argv, want)
		}
	}

	// Its exit status, stdin and environment.
	out, errs, status := valleyAsMain(t, bin, "the prompt\n", []string{
		"VALLEY_TEST_FLOW_STATUS=7", "VALLEY_TEST_CALLER=kept", "VALLEY_VERSION=0.1.0-0000000",
		"GIT_REPLACE_REF_BASE=refs/elsewhere/",
	}, "flow", "status")
	if status != 7 || errs != "" {
		t.Fatalf("a helper exiting 7: exit %d, stderr %q", status, errs)
	}
	var seen flowRun
	if err := json.Unmarshal([]byte(out), &seen); err != nil {
		t.Fatalf("stdout %q: %v", out, err)
	}
	if seen.Stdin != "the prompt\n" {
		t.Errorf("the helper read %q on stdin", seen.Stdin)
	}
	want := map[string]string{"VALLEY_TEST_CALLER": "kept", "GIT_REPLACE_REF_BASE": "refs/elsewhere/"}
	if len(seen.Env) != len(want) || seen.Env["VALLEY_TEST_CALLER"] != "kept" || seen.Env["GIT_REPLACE_REF_BASE"] != "refs/elsewhere/" {
		t.Errorf("the helper's environment has %v, want %v: the caller's, less the stamp and with git's as given", seen.Env, want)
	}

	// A valley-flow in a relative PATH entry, ahead of bin, is passed
	// over, even where Go would run it.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "rel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rel", flowHelper), []byte("not a program\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, errs, status = valleyAsMainIn(t, dir, bin, "", []string{"PATH=rel:" + bin, "GODEBUG=execerrdot=0"}, "flow", "--version")
	if err := json.Unmarshal([]byte(out), &seen); err != nil || status != 0 || errs != "" {
		t.Errorf("a valley-flow in a relative PATH entry ahead of bin: exit %d, stdout %q, stderr %q", status, out, errs)
	}
}

// Without valley-flow on PATH, flow says so in one line and exits 127,
// whatever its arguments, help flags included. A valley-flow that cannot
// be executed exits 126.
func TestFlowWithoutHelper(t *testing.T) {
	empty := t.TempDir()
	for _, args := range [][]string{{"flow"}, {"flow", "--help"}, {"flow", "launch", "-h"}} {
		t.Setenv("PATH", empty)
		status, out, errs := command(t, args...)
		if status != 127 || out != "" || errs != "valley: flow runs valley-flow, which is not on PATH\n" {
			t.Errorf("valley %s: %d %q %q", strings.Join(args, " "), status, out, errs)
		}
	}

	broken := t.TempDir()
	helper := filepath.Join(broken, flowHelper)
	if err := os.WriteFile(helper, []byte("not a program\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, errs, status := valleyAsMain(t, broken, "", nil, "flow", "--version")
	if status != 126 || out != "" || !strings.HasPrefix(errs, "valley: "+helper+": ") || strings.Count(errs, "\n") != 1 {
		t.Errorf("an unexecutable valley-flow: exit %d, stdout %q, stderr %q", status, out, errs)
	}
}
