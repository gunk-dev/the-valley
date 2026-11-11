package main

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

// A reader that stops early leaves help's exit status alone: valley
// exits 0 for each help path, and 1 for the unknown verb, whose usage
// goes to stderr. The child is this test binary running valley's main
// (TestMain), with the write end of a real pipe as the stream under test
// and its read end closed before the child writes, so its write finds no
// reader, as one into `head -n 1` that has already exited does.
func TestHelpSurvivesAReaderThatStops(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args   []string
		stderr bool // the stream whose reader stops
		status int
	}{
		{[]string{"help", "review"}, false, 0},
		{[]string{"help"}, false, 0},
		{[]string{"review", "--help"}, false, 0},
		{[]string{"self-update", "-h"}, false, 0},
		{[]string{"zx81q"}, true, 1},
	} {
		elsewhere(t)
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		release, hold, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(self, c.args...)
		cmd.Env = append(os.Environ(), "VALLEY_TEST_AS_MAIN=1")
		cmd.Stdin = release
		if c.stderr {
			cmd.Stderr = w
		} else {
			cmd.Stdout = w
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		r.Close()
		w.Close()
		release.Close()
		hold.Write([]byte{0})
		hold.Close()
		err = cmd.Wait()
		got := 0
		if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
			got = ee.ExitCode() // -1 when a signal ended it
		} else if err != nil {
			t.Fatal(err)
		}
		if got != c.status {
			t.Errorf("valley %v with its reader gone: exit %d, want %d", c.args, got, c.status)
		}
	}
}
