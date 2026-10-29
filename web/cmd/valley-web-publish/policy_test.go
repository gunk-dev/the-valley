package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// The authorization counter reads no clock and survives the process: each
// publisher over one state directory allocates a sequence greater than
// every one allocated before, a floor from the receiver raises it, and a
// counter that cannot be read is an error, never a fresh start at one.
func TestTheAuthorizationSequenceIsDurable(t *testing.T) {
	state := t.TempDir()
	next := func(floor int64) (int64, error) {
		p := &publisher{cfg: config{state: state}, stdout: io.Discard, stderr: io.Discard}
		return p.nextSequence(floor)
	}
	for i, c := range []struct{ floor, want int64 }{{0, 1}, {0, 2}, {10, 11}, {3, 12}, {0, 13}} {
		got, err := next(c.floor)
		if err != nil || got != c.want {
			t.Fatalf("allocation %d with floor %d gave %d, %v; want %d", i+1, c.floor, got, err, c.want)
		}
	}
	data, err := os.ReadFile(filepath.Join(state, authorizationSequenceFile))
	if err != nil || string(data) != "13\n" {
		t.Errorf("the counter holds %q, %v; want 13", data, err)
	}
	for _, bad := range []string{"", "x\n", "-4\n"} {
		if err := os.WriteFile(filepath.Join(state, authorizationSequenceFile), []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := next(0); err == nil {
			t.Errorf("a counter holding %q gave sequence %d", bad, got)
		}
	}
}
